# 内置功能模块化与 Runtime 配置重组设计

日期：2026-09-19。

依据：[requirements.md](requirements.md) 的 R1–R7。

状态：技术设计已于 2026-09-19 确认；按 tasks.md 进入实施。

## 1. 设计结论

本次不把三个功能简单包装成现有 `modules.Instance`。现有模块只能返回同步 Hook，
无法正确拥有消息缓存、索引、命令、后台扫描和跨模块依赖。应先把模块系统扩展为
“完整功能模块运行时”，再逐个迁移功能。

核心决策如下：

1. `modules` 仍是唯一模块配置入口，`options` 仍由模块严格解析。
2. 消息合法性、消息 ID 去重、身份解析和最终寻址判断属于核心 Admission；
   未提及消息的等待、取消和主动回复属于 `native.proactive_reply`。
3. `native.structured_search` 同时拥有索引、通过通用 LLM Tool 目录挂载的
   `search_docs`、`/search`、重载和状态；Engine 与 Runtime Manager 不再对 Search
   或工具名做具体类型特判。
4. `native.knowledge_reflection` 通过只读观察器和后台任务工作，只依赖强类型能力，
   不直接引用结构化检索模块 ID。
5. 模块构建阶段完成所有可能失败的准备；发布后的 `Start` 只启动已经准备好的循环，
   不再返回错误，避免 generation 交换后出现半激活回滚。
6. 外部 FPP 暂不实现。新增 Go 接口是宿主内部模型，不直接作为未来线协议。

## 2. 目标依赖关系

```text
app composition root
  -> registers native module factories
  -> runtime.Manager builds a generation
       -> core Admission
       -> modules.Set
            -> native.reply_experience
            -> native.proactive_reply
            -> native.structured_search
            -> native.knowledge_reflection
       -> bot.Engine
       -> operations.Router

native.knowledge_reflection
  -> requires agent.completer capability
  -> requires knowledge.publisher_factory capability
  -> requires search.control capability

native.structured_search
  -> provides search.control capability
  -> provides knowledge.publisher_factory capability
```

`runtime` 可以依赖模块和能力契约，但不得导入三个模块的具体实现包；工厂只在
`internal/app` 组合根注册。`bot.Engine` 只接收编译后的注册链、工具目录和命令目录，
不读取模块配置，也不知道模块 ID。

## 3. Module Runtime 第二阶段

### 3.1 描述、构建和依赖解析

`Descriptor` 增加 `Provides`、`Requires` 和允许的 Registration 类型。构建顺序按
capability 拓扑排序；同一层按来源、注册顺序和模块 ID 稳定排序。循环依赖、重复
提供者、缺失依赖以及返回未声明注册类型均拒绝候选 generation。

Factory 演进为接收两个受限上下文：

- `ValidationContext`：只包含可引用的逻辑群组 ID 等目录，不包含完整 RuntimeConfig；
- `BuildContext`：只包含已构建 capability 的只读 Resolver、Logger、受限事件上报、
  generation 标识和主动回复所需的 DeferredResponder。

模块不得取得 Runtime Manager、ConfigStore、SecretStore、ConnectorSet 或裸 Sender。
LLM 由宿主以 `agent.completer.v1` capability 提供；依赖它的模块在能力不存在时构建
失败。

### 3.2 强类型 capability

新增独立 `internal/capability` 包。内部以稳定 ID 存储值，提供方和消费方使用带
泛型的 Key 解析明确 Go 接口，而不是传递任意 map 或完整模块实例。

首期能力：

| Capability | 提供方 | 使用方 |
| --- | --- | --- |
| `agent.completer.v1` | 宿主 | 反思、显式搜索回答整理 |
| `search.control.v1` | 结构化检索 | Runtime 管理入口、反思 |
| `knowledge.publisher_factory.v1` | 结构化检索 | 反思 |
| `proactive.control.v1` | 主动介入 | Runtime 状态与管理入口 |
| `reflection.control.v1` | 反思 | Runtime 手动扫描入口 |

未来关闭原生模块并接入外部实现时，只要技术 capability 可解析，宿主不验证其业务
是否与原生实现等价。

### 3.3 Registration 集合（仅限内置模块）

`Instance.Registrations()` 扩展为以下强类型集合：

- 现有 `AgentInput`、`BeforeTool`、`BeforeReply` Hook；
- `InboundParticipant`：放行或消费消息，用于主动介入；
- `InboundObserver`：只读且不能修改或阻断消息，用于反思；
- `ToolRegistration`：工具定义、执行器及每轮最大调用次数，统一进入宿主 LLM
  工具目录；
- `CommandRegistration`：内置模块提供的普通 Bot 功能命令及处理器；本期不将其
  定义为外部插件 SDK 或稳定 FPP 协议；
- `BackgroundJob`：随 generation 启停的后台任务；
- capability 提供值；
- `StatusProvider`：返回脱敏模块状态。

Inbound Participant 只能返回 `continue` 或 `consume(accepted, verdict)`，不能改身份、
伪造管理员或直接发送消息。主动介入 flush 使用宿主提供的 DeferredResponder 请求
一次普通 Agent 回复；该接口固定群组、消息和 generation，不是通用 Sender。

Observer 在调用线程内只做有界内存复制，不执行网络或磁盘 I/O。反思 Observer
直接写入现有有界 buffer；定时 LLM 和文件工作由 Background Job 执行。

工具目录在 generation 构建时一次编译：重复工具名失败，不再由 MultiExecutor
静默保留第一个。`max_queries_per_turn` 作为 `search_docs` Tool Provider 的
私有预算；Engine 只按通用 ToolRegistration 的预算执行，不认识检索工具名称。

### 3.4 生命周期

模块实例提供 `Registrations`、`Start`、`Retire` 和 `Close`：

1. `ValidateOptions` 纯配置校验；
2. `Build` 构建索引、buffer、scanner、命令和 capability，可失败但无对外副作用；
3. 编译注册链，完成依赖和重复标识校验；
4. 持久化已验证配置；
5. 原子交换 active generation；
6. `Set.Start(runCtx)` 启动已准备 Job，不执行可失败初始化；
7. 旧 Set `Retire`，停止接收新异步工作并请求取消；
8. 旧请求结束后按模块逆序 `Close`。

为了保证 Build 无文件写副作用，Markdown Publisher 构造只验证路径；`extra` 目录
延迟到活动模块第一次真正发布文档时创建。

## 4. 入站处理顺序

目标顺序：

```text
connector normalized message
  -> identity resolution
  -> core Admission validation + deduplication
  -> module read-only observers
  -> operator command router
  -> built-in feature command registry (/search, supplied by native modules)
  -> inbound participant chain (proactive reply)
  -> core addressed check
  -> input hooks
  -> LLM / tool loop
  -> reply hooks
  -> connector sender
```

关键语义：

- 管理命令和已注册功能命令优先于未提及消息缓存。
- 未注册的普通未提及消息只有被主动介入模块消费，不能直接进入 LLM。
- 主动介入关闭或群组未启用时，核心 addressed check 返回 `ignored`。
- Observer 位于命令路由之前，因此反思仍能看到身份已解析的管理员回复；Admission
  已先过滤非法或重复事件。
- DeferredResponder 产生独立 `trigger=proactive` run，复用输入、工具和回复 Hook，
  但不再次进入 Admission、Observer 或主动介入链。

## 5. 三个模块的具体设计

### 5.1 `native.proactive_reply`

包布局：

- `internal/admission`：从 Gate 拆出的输入校验和 dedup，永远由 generation 持有；
- `internal/proactive`：协议无关缓存、静默窗口、管理员取消和 flush；
- `internal/modules/proactivereply`：options、Factory、注册和 capability 适配。

模块 options：

```json
{
  "silence": "4s",
  "max_window": "25s",
  "max_buffered": 50,
  "max_text_len": 500,
  "max_concurrent": 4,
  "groups": {
    "support": {"enabled": true}
  }
}
```

`max_count` 不迁移。现有实现从未读取它来触发 flush，现有文档也明确说明它不会
提前触发；保留只会制造错误预期。`dedup_ttl/dedup_max` 迁到核心 `admission`。

模块提供 Inbound Participant、DeferredResponder 驱动的 flush、状态以及
`proactive.control.v1`。管理命令改为 `/proactive on|off`，实际修改
`modules.native.proactive_reply.enabled`。项目尚未发布，不保留旧 `/gate` 别名，
避免已经拆除的 Gate 混合概念继续成为公共 API。

### 5.2 `native.structured_search`

`internal/search` 和 `internal/search/structured` 保留协议中立契约与索引实现；新增
`internal/modules/structuredsearch` 负责 options、索引生命周期、工具、命令、
capability 和状态。

模块在 Build 阶段构建完整索引，随后向宿主通用能力注册：

- `search_docs` Tool，预算来自 `max_queries_per_turn`，通过 LLM Tool 目录暴露；
- `/search` Command；
- `search.control.v1`，负责并发受控的 Reload 和 Status；
- `knowledge.publisher_factory.v1`，只允许在配置文档根目录内创建受控 Publisher；
- documents、sections、warnings、最近重载时间及错误状态。

显式 `/search` 可选择使用 `agent.completer.v1` 整理答案；缺少 LLM 时仍返回格式化
结果，因此该 capability 对 Search 是可选依赖。普通消息仍只在 LLM 主动调用
`search_docs` 时检索。

Runtime Manager 的 `ReloadSearch` 保留为管理门面，但内部只解析
`search.control.v1`。Engine 删除 Retriever、SearchPolicy、显式搜索函数和
`search_docs` 名称过滤；它只接收由模块编译出的通用 Tool 定义与执行器。

### 5.3 `native.knowledge_reflection`

现有 `internal/reflection/buffer|scanner|reflector|pipeline` 保持领域实现；新增
`internal/modules/knowledgereflection` 负责 options、依赖解析、观察、Job、状态和
手动扫描 capability。

模块 Build 时：

1. 解析 `agent.completer.v1`、`knowledge.publisher_factory.v1` 和
   `search.control.v1`；任何必需能力缺失均拒绝候选。
2. 创建新的有界 buffer、Reflector、Publisher、Pipeline 和 Scanner，但不启动扫描、
   不创建目录、不写文件。
3. 注册只读 Observer、定时 Job、Status Provider 和 `reflection.control.v1`。

Job 只在 Set Start 后启动。发布一批 Markdown 后调用 Search Control 一次 Reload；
Reload 自己负责并发互斥和运维事件。手动 `/v1/admin/reflection/scan` 通过
`reflection.control.v1` 执行，不再访问 Manager 字段。

generation 替换时允许丢弃尚未升格的短期反思 buffer；它本来就是不持久化的临时
观察数据。旧 generation 已开始的扫描按有界退场规则完成或取消。若以后确有跨
generation 保留候选的需求，再设计模块私有状态迁移协议，不在首期加入无类型
`any` 状态交接。

## 6. 最终 Runtime JSON

最终顶层结构：

```json
{
  "log": {},
  "bot": {},
  "admission": {
    "dedup_ttl": "60s",
    "dedup_max": 10000
  },
  "llm": {},
  "platforms": {},
  "administration": {},
  "operations": {},
  "extensions": {},
  "modules": {
    "native.reply_experience": {"enabled": true, "options": {}},
    "native.proactive_reply": {"enabled": false, "options": {}},
    "native.structured_search": {"enabled": false, "options": {}},
    "native.knowledge_reflection": {"enabled": false, "options": {}}
  }
}
```

移除：

- 顶层 `gate`、`search`、`reflection`；
- `platforms.groups[].auto_reply_unmentioned`；
- Search 的 `policy/backend`；
- Reflection 的 `mode`；
- Gate 的无效 `max_count`。

基础 `RuntimeConfig.Validate` 只验证核心字段和 modules 容器。模块工厂使用
ValidationContext 严格拒绝未知 options 字段，并校验群组引用、路径、扩展名、预算
和跨模块 capability。旧字段通过严格 JSON 解码直接失败，不双读、不迁移。

Descriptor 决定缺省值：`native.reply_experience` 默认启用，另外三个模块默认关闭。
Public Config 始终补齐所有已注册内置模块的有效默认值，便于未来 Web 面板编辑。

## 7. Runtime、管理接口和状态收口

Runtime Manager 删除功能型字段和工厂：retriever、reloadingSearch、共享 reflection
buffer/scanner/pipeline、Builder.SearchFactory、Builder.ReflectionFactory，以及 Search
和 Reflection 的特判构建与发布逻辑。这些资源改由 modules.Set 拥有。

Manager 只通过 capability 实现功能管理方法：

- ReloadSearch -> search.control.v1；
- ScanReflection -> reflection.control.v1；
- ProactiveStatus -> proactive.control.v1。

Search Reload 与 Reflection Scan 的管理 HTTP 路径保留。旧 `/v1/gate/stats`
删除：核心去重统计进入 Runtime Status 的 admission 字段，主动介入统计进入
modules[].details。旧顶层 Search 和 Reflection 状态字段删除，`/status` 管理命令
改为读取模块状态。

配置分类不再有 ActionReplaceSearch。任意模块开关或 options 变化统一归类为
replace_modules 并构建候选 generation；Connector、纯日志和 Secret 的既有专门
路径保持不变。

## 8. 失败和事务语义

- options 非法、capability 缺失、依赖成环、重复工具/命令/capability、索引构建失败
  或反思组件准备失败：候选失败，当前配置版本和 generation 不变。
- 配置通过候选构建但持久化失败：关闭候选模块和资源，当前 generation 不变。
- Search Reload 失败：旧索引继续服务，模块状态记录错误并发送既有运维通知。
- Background Job 运行失败：不替换 generation；记录模块状态并通过事件接口上报。
- Retire/Close 超时：新 generation 继续服务，旧模块标记关闭异常并通知，不切回已
  退场 generation。
- 模块关闭时不要求存在业务替代；只有启用模块自己声明的技术依赖必须满足。

## 9. 测试策略

模块运行时覆盖 capability 拓扑、缺失、重复、循环依赖、注册越权、Build 失败清理、
Start/Retire/Close 顺序、候选无副作用和在途 generation 固定性。

主动介入覆盖模块关闭、群未启用、未提及消息不调用 LLM，明确提及和命令不受影响，
静默 flush、管理员引用取消、容量/并发限制、核心 Admission 去重和退场清理。

结构化检索覆盖普通消息不预检索、工具预算、/search、无 LLM 回退、Reload 原子替换、
Markdown/MDX/TXT、路径资源限制，以及关闭模块后无工具、命令、索引和 capability。

反思覆盖 Observer 范围、关闭后不观察、依赖缺失时失败、Job 激活和互斥扫描、管理员
引用配对、置信度、去重、原子 Markdown、每批一次 Reload 和退场取消。

配置与端到端覆盖旧 gate/search/reflection/auto_reply_unmentioned 字段严格拒绝，
JSON Store 更新回滚、Public Config、OneBot 引用文字/图片、空 @、图片问答和管理命令。
最终执行 go test ./...、go vet ./... 和关键并发测试。

## 10. 外部拓展预留（后续议题）

外部插件的 CommandProvider、ConfigSurface/ConfigSurfaceProvider、安装握手、权限
声明和 FPP 传输协议暂不属于本分支。它们可以在后续规格中独立讨论；本设计只要求
内部模块注册模型不要把内部 Go 接口直接序列化为外部协议。

本期不实现：

- 外部插件注入自定义命令；
- 插件声明 Web 配置表单、binding 或 provider 读写模式；
- Web 管理面板对模块 schema 的通用渲染；
- 外部插件加载、安装、签名或市场。

内置结构化检索的 `/search` 仅作为本期 CommandRegistration 的纵向验证，不代表
已经承诺外部插件命令 API。

## 11. 计划中的提交边界

1. feat: 扩展功能模块运行时与能力注册
2. refactor: 拆分核心准入与主动介入模块
3. refactor: 将结构化检索迁入内置模块
4. refactor: 将知识反思迁入内置模块
5. refactor: 收口运行时模块配置与管理状态
6. docs: 更新内置模块配置与架构说明

每个提交必须独立编译并通过相关测试。迁移期间允许尚未迁移的功能暂留旧路径，但
同一功能在任一提交中只能有一个活动实现；最终不保留旧配置兼容代码。

## 12. 明确不做

- 不把内部 capability/registration 直接序列化为 FPP；
- 不增加模块私有状态的通用 any 迁移协议；
- 不共享完整 RuntimeConfig 或 ConfigStore 给模块；
- 不为模块提供通用消息发送、Shell、文件系统或 Secret 权限；
- 不调整 Connector 协议、不引入向量检索、不开始 Web 管理面板。
