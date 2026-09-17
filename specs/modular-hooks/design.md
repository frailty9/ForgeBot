# 模块级声明式 Hook 挂载设计

日期：2026-09-17。依据：[requirements.md](requirements.md) 的 R1–R9。

状态：用户已确认设计并授权在 feat/modular-hooks 连续实施。首版已实现，任务与验证结果见 [tasks.md](tasks.md)。文中的后续能力不因此视为已经实现。

## 1. 最优先改动与交付范围

最优先的是建立“模块级声明式挂载 + 内部强类型 Hook 链”，并用一个真实原生模块完成纵向验证。不是先重命名 ConfigStore、拆配置文件、实现完整 FPP 或新增 Session。

首个迁移对象建议为 native.reply_experience（回复辅助）：

- 空 @ 补充自我介绍请求。
- 纯图片输入补充图片分析提示。
- 真正执行 search_docs 工具前，发送一次检索预回复。

理由：三者都是现有回复体验策略，当前集中硬编码在 Engine；前两项位于输入准备节点，第三项位于工具执行前，能验证一个模块跨多个 Hook 整体启停。它不拥有后台任务、文档写入或跨轮状态，迁移风险小于 Gate/反思。

本交付只把这个原生功能模块化。Gate、反思、管理命令继续沿用现有路径和配置开关，后续逐项迁移；不得同时增加第二套开关。外部来源在测试中用替身验证排序和挂载模型，不接入真实 FPP。

## 2. 当前代码事实

源码基线 a8f63ed，以下路径相对仓库根目录：

- internal/runtime/manager.go：身份标记后先调用反思 Observe，再调用 operations.Router；Router 外围还有暂停控制。generation 当前持有具体 Engine、Router、反思缓冲和扫描器。
- internal/bot/engine.go：HandleInbound 校验、Gate 判定、replyText、reply；replyText 和 userMessage 内置空 @、图片提示；工具循环内部内置 searchPreReply。
- 显式 /search 走独立检索路径，当前不发送上述工具预回复；迁移不能改变这一点。
- Gate 的主动 flush 直接进入 replyText/reply，也必须使用对应 generation 的 Hook，不允许绕过。
- internal/settings/store.go 已定义 Current、RuntimeAt、Update、Rollback、History；JSONFileStore 实现该接口，admin 已依赖接口。
- Config 公共视图是手工构造的脱敏对象；不能自动把任意模块 options 原样透出。

当前功能未被模块化不代表没有实现。尤其不在本次重写反思、扫描、发布和索引更新。

## 3. 模块边界与依赖方向

建议新增包：

| 包 | 职责 | 不得依赖 |
| --- | --- | --- |
| internal/hooks | 类型化输入输出、注册集合、排序、执行与结果校验 | config、runtime、具体模块、MCP |
| internal/modules | 模块描述、工厂目录、配置解析、实例集合与资源所有权 | bot、runtime、server、具体原生模块 |
| internal/modules/replyexperience | 回复辅助模块及 options 校验 | runtime、ConfigStore、具体平台 |

由 app 组合根显式注册内置工厂并注入 Runtime Builder，不用 init() 隐式全局注册。Bot 只接收准备好的 hooks.Chain，不查模块 ID，也不读 modules 配置。

配置层只负责通用模块配置封装。模块 options 的语义校验由对应工厂提供；admin/runtime 共用该纯校验入口，避免 config 导入模块实现形成环。

内部概念接口如下，具体 DTO 字段在任务实现时按本设计落实，不代表 FPP 线协议：

```go
type Factory interface {
    Descriptor() Descriptor
    ValidateOptions(json.RawMessage) error
    Build(context.Context, json.RawMessage) (Instance, error)
}

type Instance interface {
    Registrations() hooks.Registrations
    Close(context.Context) error
}

type Registrations struct {
    AgentInput []AgentInputRegistration
    BeforeTool []BeforeToolRegistration
    BeforeReply []BeforeReplyRegistration
}
```

- Descriptor：稳定模块 ID、默认启用状态、来源、允许的注册类型和宿主限定顺序。来源由装配层赋值，不能由外部声明成 native。
- Instance：一个模块一个实例，承载多个处理器；必须支持不同请求并发调用。
- Registration：稳定 handler_id、类型化处理器、顺序、失败策略。不含用户级独立启停开关。
- ModuleSet：拥有本 generation 的实例及只读 Chain；失败时反向 Close 已构建实例。
- 同一实例内 registration_id = module_id + handler_id，禁止技术标识重复，不根据业务职责判断重复或替代关系。
- 不给模块注入全局 ConfigStore、Runtime Manager 或直接 Sender。本期模块返回受限效果，由宿主执行。

首版 Instance 不启动业务后台循环，Build 只准备资源。命令、Job、异步观察器后续有真实模块需要时增加专门注册表；本版声明不支持的注册类型明确拒绝，不能静默丢弃，也不承诺已实现 R1 的全部后续能力。

## 4. 首批 Hook 节点

不一次公开所有想象中的节点。首批 3 个都是同步、阶段明确的节点：

| 节点 | 精确位置 | 可修改或返回 | 不允许 |
| --- | --- | --- | --- |
| agent.input.prepare | 现有命令/显式搜索分流之后、普通问答第一次 LLM 调用之前；媒体已转换为通用内容部件 | 替换用户内容中的文本部分，或显式停止本次普通问答 | 改身份、角色、媒体、系统提示、模型、工具权限、预算 |
| tool.before_execute | 工具名白名单、开关与剩余预算校验通过后，实际 Execute 前 | 请求一次受限的进度提示；保持执行或拒绝本次调用 | 改工具名/参数、放行已拒绝工具、直接发送消息 |
| reply.before_send | Engine 普通回复/主动回复/检索预回复调用 Sender 之前 | 修改文本或抑制本次发送 | 改群组、接收者、引用关系、生成新媒体、递归请求发送 |

原生回复辅助使用前两个 Hook。第三个节点用于验证输出转换与抑制契约，默认没有处理器，因此不改变现有回复。

本版 reply.before_send 只覆盖 Engine 发送，不覆盖管理命令、运维通知。名字和文档明确这一边界，不能宣称全局出站中间件。未来统一投递阶段再扩展覆盖范围。

流程：身份/现有反思观察 → 管理命令/暂停 → 校验/Gate → 显式搜索分流或输入 Hook → LLM → 工具权限与预算 → 工具 Hook → 执行工具 → LLM 汇总 → 回复 Hook → 最终出站校验 → Sender。

首版保留当前一次工具回合，不顺便实现 Agent Loop。后续每次工具执行继续复用同一节点，agent.input.prepare 默认只针对本次用户输入执行一次，不在工具续轮重复追加提示。

### 类型化结果，而不是万能 action

- AgentInputResult：可选的新用户内容，或 stop 原因；两者互斥。未修改返回零值。
- BeforeToolResult：是否拒绝，以及可选 ProgressEffect；拒绝时不同时产生提示。被拒绝调用形成正常 tool result，不能丢失工具调用 ID。
- BeforeReplyResult：可选新文本，或 suppress 原因；两者互斥。
- 每种结果仅适用于自己的节点。无任意 JSON Patch、任意字段写入、任意宿主方法调用。
- 输入用每次调用的独立快照，嵌套 slice/map 不与主流程共享可写内存；候选结果通过校验后才替换工作副本。可写 DTO 不暴露媒体 Data；需要的只读摘要按需复制。
- identity、generation 和发送目标由宿主持有，不从模块结果回填。输入规范化内容不反写原始群消息，也不污染已有反思观察。

### 一次请求的执行上下文

宿主生成 ExecutionContext：run_id、generation 标识、逻辑群组/用户/消息标识、触发来源（direct/proactive）、取消上下文与本轮效果去重状态。

- 不能让模块用全局变量记录“已预回复”，否则不同群组会互相影响。
- 直接请求与 Gate flush 分别创建自己的 run；同一 run 的输入、工具、预回复及最终回复共享它。
- run_id 不等于跨重启投递幂等键，本版不承诺持久投递。
- 声明式 ProgressEffect 不赋予 LLM 发送消息工具，工具目录保持不变。

## 5. 首个原生模块的行为

native.reply_experience 默认启用，整体挂载 input_defaults 与 search_progress 两个处理器。

| 输入/操作 | 启用时 | 禁用且无替代时 |
| --- | --- | --- |
| 无正文、无媒体、Mentioned=true | 补当前自我介绍请求 | 输入链结束后仍为空则返回 empty_input，不调用 LLM、不记录入站 ERROR |
| 有可用图片、无正文 | 在图片内容后追加当前分析提示 | 保留图片，不追加提示，仍允许多模态请求 |
| 普通文字/图文 | 不改变内容 | 不改变内容 |
| 获准执行 search_docs | 本轮尝试一次当前检索预回复 | 不预回复，正常执行检索 |
| 显式 /search | 保持现有独立路径，不新增预回复 | 同左 |

- 空 @ 的结构化入站校验继续允许通过；empty_input 判断在所有输入 Hook 后执行，外部替代仍有机会填入内容。
- 只含不支持的表情且未 @ 的 OneBot 消息继续由适配器忽略，不在本模块重新实现。
- 纯图片判断基于可用媒体内容，不以未解析的文件引用数量冒充可用图片。
- 无 LLM 的既有 echo 回退单独回归：空 @ 保持当前补词效果，纯图片不因模块迁移改变原有 echo 文案；不把这个回退扩展成新产品功能。
- 预回复效果由 Chain 先收集；若后续处理器拒绝工具，本次候选提示丢弃。全链通过后宿主尝试发送，再执行工具。
- 一次 run 至多一次进度提示尝试（包括发送失败），延续现有避免重复提示的行为；送达失败记警告，不阻断工具。
- 预回复使用同一 run 的 reply.before_send，标记 reply_kind=progress；不再次触发输入或工具 Hook。
- 默认提示文案从 Engine 移入模块；Engine 不保留按 module_id 或 search_docs 名称发送预回复的业务分支。
- 权限/预算过滤仍属于工具执行边界，拒绝的工具不得因预回复 Hook 而变成可执行。

## 6. 模块配置与 ConfigStore

示意配置，字段名待本设计确认：

```json
{
  "modules": {
    "native.reply_experience": {
      "enabled": true,
      "options": {
        "empty_mention_prompt": "请简单介绍一下你自己。",
        "image_only_prompt": "请读取图片中的文字、界面和错误信息，将其视为用户本次输入，并直接处理图片中提出的问题。",
        "search_progress_text": "正在检索相关文档，请稍候。"
      }
    }
  }
}
```

- modules 以 module_id 为键。单一 enabled 控制整个模块，不设置逐处理器开关。
- 未配置该原生模块时用 Descriptor 默认值，保持旧配置行为；显式 enabled=false 必须保留，不能被默认值覆盖。
- enabled 缺省与 false 在解析时可区分；options 缺省使用默认值，已填写文本必须非空且有长度上限。禁用不删除 options。
- 首版不开放用户可调阶段、tier、逐 Hook 排序、任意失败策略或 scope DSL。后续出现需求再在允许范围开放。
- 配置 envelope 可采用 map[string]ModuleConfig 与 json.RawMessage；注册工厂负责严格解析已知 options、拒绝未知字段并提供安全公开视图。RawMessage 只用于配置扩展，不作为 Hook 输入输出类型。
- 首版 unknown module_id 报技术性配置错误；关闭已知原生模块却没有业务替代则合法。以后外部模块目录如何加载由 FPP 阶段定义。
- config 层验证 envelope 和大小限制；app 注入的模块目录负责 options 纯校验。启动、admin validate、提交和回滚共用同一纯校验流程，候选构建另做资源准备检查。
- 有效默认值在配置解析后的独立模块计划中计算，不在持久化回调后改写候选 Runtime；避免破坏现有 snapshot DeepEqual 校验。存储仍保存同一份提交配置。

### Store 保留现有接口

本期沿用 settings.Store 的名字、完整快照 API 与 JSONFileStore 实现。不新增平行 ConfigStore，不增加模块独立文件，不做 DBStore，也不引入数据库依赖。

关系：admin/operator → settings.Store 接口 → JSONFileStore；以后 DBStore 实现同一契约。业务模块不直接接触 Store。

新增 map/RawMessage 后必须检查 Current、RuntimeAt、Update、历史及 Runtime 快照边界的深拷贝：调用者修改返回配置不能篡改已存储配置或活动链。该契约对未来 DBStore 同样适用，用 Store 测试替身验证。

暂不新增 PATCH 接口。仍使用带 expected_version 的完整运行时更新与回滚。现有“脱敏 GET 不可原样 PUT”的限制保留，不把其修复混入 Hook 首版。

### 旧开关的渐进迁移

reply_experience 当前没有重复的业务开关，可直接新增模块开关。Gate、reflection 等未迁移功能继续读当前配置。未来迁移时在同一改动中移除或一次性转换旧开关，不长期建立两套优先级。此次不删除旧配置字段，也不声称所有原生功能已完成模块化。

## 7. 排序、结果提交与失败

每个节点独立构建不可变列表，按 source_rank → host_order → module_id → handler_id 排序。source_rank 首版为 native 在前、external 在后；后者仅由宿主测试替身模拟。核心保护不作为可禁用 tier 挂在链里。

host_order 来自宿主批准的描述，插件不能自行提升层级。首版不做依赖拓扑图或 requires/replaces 业务能力协商。多个业务相似模块可以共存；部署方负责重复业务效果。

- 每个处理器看到前一个成功结果；失败或非法结果不应用本处理器修改。
- stop/deny/suppress 为终态，本节点后续处理器不能恢复被终止的流程。
- 合法业务停止与运行错误分开：empty_input/module_stopped/reply_suppressed 不误报 llm_failed 或送达成功。
- 原生回复辅助的普通执行错误默认 continue，保留之前有效输入；非法越权结果中止本次处理并明确记录，不静默接受。
- 权限/格式不合法的候选注册在构建时失败，不在消息运行时临时删掉出错项。
- 首版 Hook 不能执行发送、配置写入等副作用；受限提示由宿主在节点校验后执行。后续失败不承诺撤销已经发生的合法外部副作用。

### 超时、取消与容量

建议首版采用宿主内部默认值：单处理器 200ms、一次节点链 1s（并受父 context 更短 deadline 限制），每节点最多 32 个处理器、最多 64 个启用模块。作为初版防护默认值而非性能承诺，任务实施前可依据测试调整。

- 原生调用同步执行并传递 context，不为每次调用额外开可遗留 goroutine；返回时检查取消和 deadline，过期结果不应用。
- 这只能约束协作式原生代码；无法强制中断挂死函数。原生实现不得在 Hook 中执行无界 I/O，不用“超时”冒充沙箱。
- parent context 取消/过期时停止链，不能按 fail-open 继续发起 LLM 或发送。
- panic 在调用边界转换成模块错误并记录，不宣称可恢复 fatal runtime 错误。
- 进度消息发送采用现有 Sender 的超时及父请求限制，耗时不计为处理器计算预算；它仍是一次有界网络操作。
- 首版不实现异步 observer 队列、后台 Job 注册或插件进程强杀。后续开放它们前需单独设计背压、重试、停止和副作用语义，不能直接用 go handler(event) 无限派发。

## 8. Runtime 生命周期与资源所有权

模块配置变更继续分类为现有 restart_engine，不增加立即热卸载任意单模块的承诺。Connector、Retriever 按已有规则复用；Engine、MCP 等随现有重建路径处理，本期不优化为单模块独立替换。

模块状态：disabled / prepared / active / retiring / closed；构建失败是操作结果，旧 active 实例继续保留。

构建顺序：

1. 纯校验配置，解析有效模块计划，冻结描述与顺序。
2. 构建候选模块实例并收集所有注册，验证标识、类型、权限和容量。
3. 一次性编译候选 Chain，再将它注入候选 Engine；不逐个修改当前链。
4. 其余候选依赖完成准备，任一步失败按所有权逆序释放新建资源，复用资源不得关闭。
5. 按现有配置事务完成持久化，再切换 generation。首版模块无独立 Start，不在落盘后新增会失败的业务激活步骤。
6. 新请求走新链；旧 generation 的请求和 Gate 已接受的后台处理结束后，停止 Engine 并关闭其 ModuleSet。

为兼容独立 Builder.Build 入口，ModuleSet 的释放权统一交给构建成功的 Engine：依赖中注入 Chain 和有界 closeHooks 回调，Engine.Shutdown 在请求/Gate 处理退场后调用一次；generation 只借用模块状态，并沿既有路径关闭 Engine，不重复 Close ModuleSet。Engine 构建成功之前的失败由 Builder 释放 ModuleSet，成功后转移所有权。回调避免 Engine 导入具体模块实现。

首次启动与候选失败均执行相同清理规则。模块 Close 应幂等、有界。首个原生模块无后台资源，因此 Close 可无操作；用资源型测试替身验证清理顺序及次数。

退场时禁止新请求进入旧链，但允许旧 run 在原快照下完成。超过 grace 只记录仍在退场，不强制销毁被借用的对象。不得每次重配无限叠加滞留 generation；设计要求最多保留一个超期退场实例，未释放前新重建返回 runtime_busy（日志级更新不借此关闭旧对象）。这项策略需在任务中覆盖反复更新场景。

本版停用不等于立即撤销已经进行中的旧 run。未来人工接管需要的“拦截在途回复”是另一项业务控制，不能误认为 generation 切换自动解决。

### 现有事务边界的注意事项

目前 publish 在持久化之后还会调用共享反思缓冲 UpdateConfig。不能由本次模块设计推断现有所有组件已具备完全无失败的提交后阶段。本期新增的模块路径必须在持久化前完成全部可失败检查；现有反思状态迁移问题保留为后续迁移前审计项，不在首个无状态模块中隐藏扩大改造。

## 9. 状态、配置公开与故障定位

沿用既有管理端点，不新增 Web 页面或安装 API。

- GET /v1/admin/runtime 增加 modules 摘要：module_id、source、enabled、state、已挂载节点/handler_id、有限错误摘要。
- GET /v1/admin/config 只增加本版原生模块明确允许公开的 options。将来不能直接公开未知插件 RawMessage；凭据仍单独管理。
- 调用日志记录 run_id、generation、module_id、handler_id、hook、duration、outcome；默认不记录完整提示词、媒体或密钥。
- HTTP 配置 validate 进行 options 纯校验，不启动模块或连接外部服务。真正准备失败由配置提交明确返回。
- modules map 和状态输出稳定排序，避免重启导致显示顺序漂移。

## 10. 验收矩阵

| 场景 | 预期 | 需求 |
| --- | --- | --- |
| 不写 modules，普通文字、空 @、纯图片、检索工具 | 与现有默认行为一致；多模态内容次序和预回复次数不变 | R9 |
| 同时包含多个 search_docs 调用 | 每 run 只尝试一次提示；预算/白名单拒绝的不提示 | R1、R7 |
| 关闭 native.reply_experience | 两处注册全部消失；空 @ 返回 empty_input；纯图片仍传图片；检索仍可用 | R1、R2 |
| 关闭原生后无替代或启用自定义测试模块 | 不做等价检测，不要求 replaces/capabilities 覆盖 | R2 |
| 原生和外部来源替身共存，同优先级 | 原生先执行，后续按稳定 ID；业务相似不被自动拒绝 | R4 |
| 一个模块重复注册或一个注册非法 | 整个候选集合失败，旧链不变，候选资源只释放一次 | R1、R3、R6 |
| 后置处理器拒绝工具 | 丢弃暂存进度提示，生成拒绝 tool result，不执行工具 | R3、R7 |
| 处理器修改身份/媒体/目标或返回互斥动作 | 校验拒绝，主流程副本未被污染 | R3 |
| Hook 错误、panic、deadline、父取消 | 按定义继续或停止，过期结果不应用，错误可定位 | R7 |
| 普通回复、主动回复、进度提示 | Engine 三条路径均经过 reply.before_send；管理通知明确不在本期覆盖 | R9 |
| 两群并发、两次相同问题 | run 效果状态隔离，不使用模块全局标志 | R7 |
| 持久化失败、版本冲突、候选失败 | 旧 generation 保活，无半挂载、无候选发送 | R5、R6 |
| 请求中途热更新 | 当前请求用旧链，新请求用新链；退场前不关闭旧实例 | R6 |
| Gate flush 与更新/关闭并发 | 模块不在仍有后台使用时释放 | R6 |
| 重配遇到超期退场 | 不积累无限退休实例，返回明确 busy 状态 | R6、R7 |
| 修改 Store 返回的 modules/options | 已保存与活动配置不变化；历史/回滚也保持隔离 | R5 |
| fake Store 与 JSONFileStore | 使用同一接口完成配置更新/历史/回滚，不依赖文件路径 | R5 |

实施后验证顺序：新增 hooks/modules 单测 → bot/runtime/settings/config/admin 相关回归 → go test ./... → go vet ./... → 环境支持时 go test -race ./...。race 工具链不可用时明确记录，不能宣称通过。

单独增加空链/原生链 benchmark（每调用耗时、分配），与改动前 mock LLM 基线比较；不在本阶段据此推断整机或 Prayer 资源对比。

## 11. 本阶段不冻结的后续协议

- 不用这组 Go 接口作为公开第三方 SDK，稳定后的 DTO 再定义 FPP schema/version。
- 不提前暴露任意 before_llm 请求对象、系统提示替换、工具新增、命令注册或后台任务权限。
- 原生回复辅助只是第一项迁移，不代表其他功能应组合为一个巨型插件。
- 反思的观察 Hook/扫描任务、Gate 的缓冲/flush 生命周期、Session 的跨轮状态各自在迁移前设计。
- FPP 与 MCP 复用底层收发的方向保留，本期不移动 MCP 包或改变其线上协议。

## 12. 已确认的首期范围

1. 选择回复辅助作为首个真实迁移模块，不先迁移有状态 Gate/反思。
2. 首批为 agent.input.prepare、tool.before_execute、reply.before_send；暂不实现异步观察、命令、Job 注册。
3. 新模块开关放 runtime.modules，现有未迁移配置保持原样，settings.Store 不为改名而改名。
4. 首版不开放自由阶段/层级排序；关闭回复辅助后空 @ 默认忽略，可由外部替代补充。
5. 配置更新复用 Engine 重建，旧请求按旧链完成，不承诺单模块无损热卸载。

以上范围已确认并实施，验收与环境限制见 [tasks.md](tasks.md)。后续 Agent Loop、异步观察和 FPP 仍需独立规划。

## 13. 实现补充

- Input DTO 仅包含 Text、Mentioned、可用 Images 数和原始 MediaCount，不暴露可写媒体；用于区分纯空 @ 与解析失败的媒体引用。
- Factory 额外提供 PublicOptions，管理接口只输出工厂明确允许公开的字段，不直接返回未知 RawMessage。
- generation 日志标识为进程内构建序号，与配置 version 分开；restart 使用同一配置版本也能区分执行实例。
- Gate onTimer 纳入生命周期锁，Engine 在 Gate 已启动回调退出后才关闭模块。尚未启动的待处理消息沿用现有取消/丢弃行为。
- 没有增加后台任务注册或外部进程。超期退休时 runtime_busy 映射 HTTP 409；可等待在途请求完成后重试。
- 竞态工具链不能运行，且标准库独立测试同样失败；不能将普通并发回归等同于 race 通过。
