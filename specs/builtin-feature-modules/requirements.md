# 内置功能模块化与 Runtime 配置重组需求

日期：2026-09-19。

状态：需求已于 2026-09-19 确认；下一步进入技术设计，设计确认前不实施代码重构。

## 1. 背景与目标

ForgeBot 已建立模块级声明式 Hook，但目前只有 `native.reply_experience`
使用该机制。主动介入消息缓存、结构化检索和知识反思仍由 Bot Engine 或
Runtime Manager 直接构建、调用和管理，导致业务功能继续侵入核心编排，且
`runtime.json` 同时存在 `modules`、`gate`、`search`、`reflection` 和群组级业务
开关，配置职责分散。

本阶段将这三个可选业务能力改造成随核心二进制发布、通过模块注册表挂载的
内置功能模块。它们可被整体关闭，未来可由外部插件提供类似行为；ForgeBot
不检查外部替代是否业务等价，部署方自行承担替代行为和副作用。

本阶段还应把模块系统从“仅能注册同步 Hook”扩展为足以承载真实功能的运行时
模型，但不提前设计万能插件 API，也不实现外部 FPP 线协议。

## 2. 术语和边界

- 模块：配置和生命周期的最小启停单位。一个模块可注册多个 Hook、工具、命令、
  只读观察器、后台任务和能力。
- 内置功能模块：随 ForgeBot 二进制发布，由组合根显式注册的原生模块。其行为
  与外部插件统一进入宿主挂载模型，但不因此变成进程外插件。
- 核心准入：身份与寻址判断、消息合法性校验、消息 ID 去重、命令优先路由及
  最终权限检查。它们保护宿主语义，不属于可关闭业务模块。
- 主动介入：对未明确提及 Bot 的群消息进行有界缓存，在静默窗口内等待可信
  管理员回答，并在条件满足时谨慎回复。这是可关闭业务功能。
- capability：模块向宿主登记的强类型能力。消费者依赖能力契约，不依赖具体
  模块 ID，也不得取得整个 Runtime Manager、全局配置或配置存储。

## 3. 目标配置结构

### 3.1 保留 `modules` 作为统一挂载入口

顶层继续使用 `modules`，不改名为 `features`。`modules` 表示宿主的统一运行单位，
既适用于内置功能，也能承接未来外部插件握手后形成的挂载声明；“内置功能插件”
是产品层描述，不新增第二套配置概念。

每个模块只保留一个 `enabled`，模块私有配置继续使用现有 `options`，避免仅为命名
偏好破坏已经落地的公共配置模型：

```json
{
  "admission": {
    "dedup_ttl": "60s",
    "dedup_max": 10000
  },
  "modules": {
    "native.reply_experience": {
      "enabled": true,
      "options": {}
    },
    "native.proactive_reply": {
      "enabled": false,
      "options": {
        "silence": "4s",
        "max_count": 8,
        "max_window": "25s",
        "max_buffered": 50,
        "max_text_len": 500,
        "max_concurrent": 4,
        "groups": {
          "support": {"enabled": true}
        }
      }
    },
    "native.structured_search": {
      "enabled": false,
      "options": {
        "document_root": "D:/forgebot-docs",
        "extensions": [".md", ".mdx", ".txt"],
        "default_results": 5,
        "max_results": 8,
        "min_match_ratio": 0.35,
        "query_timeout": "750ms",
        "build_timeout": "20s",
        "max_queries_per_turn": 1,
        "max_context_chars": 12000,
        "max_section_chars": 8000,
        "max_file_bytes": 4194304,
        "max_total_bytes": 268435456,
        "max_files": 10000
      }
    },
    "native.knowledge_reflection": {
      "enabled": false,
      "options": {
        "conversation_ids": ["support"],
        "scan_interval": "15m",
        "message_ttl": "24h",
        "quiet_window": "3m",
        "max_session_duration": "30m",
        "max_messages_per_conversation": 1000,
        "max_messages_total": 10000,
        "max_message_chars": 4000,
        "max_pairs_per_scan": 50,
        "min_confidence": 0.9,
        "extra_directory": "extra"
      }
    }
  }
}
```

示例是目标形态，不是最终 JSON Schema。设计阶段可以调整字段细节，但不得重新
引入重复启停来源。

### 3.2 删除分散的旧业务配置

- 删除顶层 `gate`；其中去重参数迁入核心 `admission`，其余缓存与主动回复参数
  迁入 `native.proactive_reply.options`。
- 删除顶层 `search`，其配置迁入 `native.structured_search.options`。
- 删除顶层 `reflection`，其配置迁入 `native.knowledge_reflection.options`。
- 删除 `platforms.groups[].auto_reply_unmentioned`；主动介入的群组范围只由
  `native.proactive_reply.options.groups` 管理。
- `native.structured_search` 已确定具体实现，因此不保留目前只有一个合法值的
  `backend: structured`；当前只支持按需检索，也不保留只有一个合法值的
  `policy: on_demand`。将来出现第二种真实行为时再增加有明确语义的配置。
- 本项目尚未发布，不兼容旧字段、不双读旧结构，也不保留废弃别名。示例配置、
  测试配置和文档必须在同一阶段迁移；部署方需要一次性更新现有 JSON。

## 4. 功能需求与验收标准

### R1. 核心准入与主动介入分离

1. 当 `native.proactive_reply` 关闭时，未提及 Bot 的普通群消息不得进入 LLM；明确
   提及、合法命令和其他核心准入行为仍按现有规则工作。
2. 无论主动介入模块是否启用，核心都必须执行消息 ID 去重、身份解析、寻址判断、
   输入合法性检查和命令优先路由。
3. 当主动介入模块启用但当前群未在其 `groups` 中启用时，该群未提及消息不得进入
   模块缓存或触发 LLM。
4. 当群已启用主动介入时，现有静默窗口、可信管理员回答取消、缓存上限、并发上限
   和主动 flush 行为应保持不变。
5. 当候选 generation 构建失败或模块关闭时，宿主不得遗留缓存回调、计时器或后台
   goroutine；旧 generation 在退场前仍应完成其已接受的有界工作。

### R2. 结构化检索作为完整功能模块

1. 当 `native.structured_search` 启用时，模块应整体提供 `search_docs` 工具、显式
   `/search` 能力、索引状态及索引重载能力。
2. 当模块关闭时，宿主不得构建索引、暴露 `search_docs`、接受文档重载或保留
   Engine 中按模块 ID/具体检索器类型编写的业务分支。
3. 普通消息不得固定预检索；只有显式检索或 LLM 工具调用才查询索引，保持现有
   结构化检索行为和预算。
4. 索引构建和重载继续使用原子替换；失败时旧 generation 或旧索引保持可用。
5. 模块可提供强类型的文档目录、文档发布和索引重载 capability，消费者不得依赖
   `native.structured_search` 这一具体 ID。

### R3. 反思作为观察与后台任务模块

1. 当 `native.knowledge_reflection` 启用时，模块应以只读观察器接收身份已解析的
   必要消息字段，并以受宿主管理的后台任务执行定时扫描。
2. 候选 generation 在正式发布前不得启动扫描、写文档、发送通知或产生其他业务
   副作用；只有激活后的模块可以启动任务。
3. 当模块关闭时，不得缓存新消息、调用反思 LLM、写入文档或请求索引重载。
4. 模块应依赖通用文档发布及索引重载 capability，不直接持有 Runtime Manager、
   Search 具体实现或全局配置存储。
5. 当所需 capability 不存在时，启用反思的候选配置应明确失败并保留旧 generation；
   这是技术依赖校验，不是对外部替代模块进行业务等价检测。
6. 现有有界内存、TTL、管理员引用配对、去重、置信度、原子 Markdown 发布、每批
   至多一次索引重载和失败通知语义应保持不变。

### R4. 功能模块运行时能力

模块系统应在现有同步 Hook 之外，仅增加上述真实模块所需的强类型注册能力：

- 入站参与者：可对消息作有界吸收或放行，用于主动介入；
- 只读入站观察器：不得修改、阻断消息，用于反思；
- Tool Provider：声明工具定义与执行器，用于结构化检索；
- Command Provider：声明受宿主授权和路由约束的命令能力；
- Background Job：由宿主在激活后启动、退场时取消并等待；
- Capability Provider/Consumer：以类型化契约解析模块间依赖；
- Status Provider：向管理状态提供脱敏、稳定的模块状态。

不要求所有模块实现全部接口。宿主必须按模块拥有这些注册项和资源，一个模块的
构建或激活失败不得发布半套能力。

### R5. 生命周期与热更新

1. 模块应至少区分准备、激活、退场三个阶段；准备阶段允许分配可回收资源，但不
   启动可观察的业务副作用。
2. Runtime 更新应先解析和校验全部模块配置、解析 capability、构建候选 generation，
   成功后原子发布并激活；任一步失败均保留旧配置版本和旧 generation。
3. 单次入站处理应固定使用捕获到的 generation，不在处理中途混用新旧模块。
4. 旧 generation 退场时应取消 Job、停止接受新观察或缓存输入，并在有界期限内
   等待已有请求后关闭资源。
5. 模块状态至少应区分 disabled、prepared、active、retiring、failed/closed，并能
   定位构建、激活、后台任务和关闭错误。

### R6. 配置校验和管理面

1. 模块私有 options 由对应内置工厂严格解码，拒绝未知字段、非法时长、越界预算、
   不存在的群组或会话引用。
2. 跨模块技术依赖由宿主依据 Descriptor/capability 校验，不要求用户手写依赖图。
3. 一个功能只有一个 `enabled` 来源；模块 options 不再包含第二个 `enabled`。
   反思现有 `mode` 只有 `off/auto` 两种取值，与模块启停完全重复，因此一并删除。
4. 配置仍经现有 `settings.Store` 版本、候选校验、历史和回滚流程管理；本阶段不
   拆分 JSON 文件、不实现 DBStore，也不让模块直接读写 Store。
5. 管理 API 返回经过模块定义脱敏的公共 options 和运行状态，不透出文档正文、
   缓存消息、LLM 输入或 SecretStore 内容。

### R7. 行为保持与回归

1. 迁移前已经验证的直接问答、空 @、图片输入、引用上下文、OneBot 收发、管理命令、
   按需检索、主动介入和知识反思行为均应有回归测试。
2. 模块化不得重新引入“Gate 关闭时未 @ 消息仍调用 LLM”的缺陷。
3. 结构化检索的 Markdown/MDX/TXT 支持、显式重载、失败保留旧索引及无向量依赖
   的约束保持不变。
4. 反思继续只保存升格后的 Markdown，不新增完整聊天历史持久化。
5. 默认示例配置应保持保守：主动介入、结构化检索和反思默认关闭；
   `native.reply_experience` 继续默认启用。

## 5. 推荐迁移顺序

1. 扩展模块运行时模型和生命周期，但不迁移业务，建立候选构建、激活、退场及
   capability 测试基线。
2. 迁移 `native.proactive_reply`，同时拆出不可关闭的核心 Admission；先验证最容易
   发生行为回归的未寻址消息路径。
3. 迁移 `native.structured_search`，验证 Tool、Command、可重载资源和 capability。
4. 迁移 `native.knowledge_reflection`，用 Observer、Job、文档发布和索引重载依赖
   验证完整功能模块模型。
5. 最后一次性切换目标 `runtime.json` 结构并更新文档、示例和管理状态；提交历史
   按上述阶段拆分，保证可单独定位回归。

## 6. 非目标

- 不实现外部 FPP 握手、SDK、插件市场、安装器或签名。
- 不实现向量检索、混合检索或 embedding。
- 不实现 DBStore、配置文件拆分或 Web 管理面板。
- 不新增完整聊天历史、长期会话记忆或跨重启恢复消息缓存。
- 不新增 OneBot 富媒体能力或 Prayer 的其他功能对齐项。
- 不允许模块任意取得 Sender、Runtime Manager、SecretStore、ConfigStore 或内部
  对象图；需要的效果和资源必须通过受限契约提供。
- 不验证外部替代是否满足原生模块的业务完整性，也不为部署方的替代选择兜底。

## 7. 已确认决策

用户已确认以下决策：

1. 接受核心 Admission 永远保留，仅把未 @ 缓存及主动介入迁入
   `native.proactive_reply`。
2. 接受保留顶层名 `modules` 和子字段 `options`，不再引入 `features/config` 同义层。
3. 接受把群级主动介入开关从 `platforms.groups` 移入主动介入模块。
4. 接受删除顶层 `gate/search/reflection` 和单值的 Search `policy/backend`，不兼容
   旧 JSON 字段。
5. 接受结构化检索提供 capability，反思只依赖能力而不硬编码模块 ID。
6. 接受本阶段只扩展内置功能模块运行时，不同时实现外部 FPP。
7. 接受按“运行时能力 -> 主动介入 -> 结构化检索 -> 反思 -> 配置与文档收口”的
   顺序设计和实施。
