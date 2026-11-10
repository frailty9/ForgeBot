# ForgeBot 配置手册

ForgeBot 将启动所必需且不应频繁变化的配置放在 YAML 中，将应用层配置放在可写的 JSON 中。LLM API Key 不进入任一配置文件，而是通过管理 API 写入加密的 SecretStore。

**一个部署对应一个业务。** 一个实例可以接入多个平台的多个群，但共用同一份业务范围（`bot.system_prompt`）、知识库、回答模型和模块规则；按群的配置只决定该群开启哪些功能，或填写群专属的展示内容（如欢迎语文本）。服务多个互不相关的业务时，请分别部署。

想按配置文件里的字段反查说明在哪里，见 [配置字段索引](config-index.md)。首次使用建议从 [CLI 初始化](cli.md) 开始；[默认行为表](default-behavior.md)、[群管理与欢迎](group-management.md)、[存储恢复](storage.md)分别补充行为、事件操作和持久化边界。

## 1. 配置分层

| 层级 | 介质 | 是否热更新 | 内容 |
| --- | --- | --- | --- |
| 启动配置 | YAML / 少量环境变量 | 否，修改后重启进程 | HTTP 监听、日志、运行时配置路径、密钥库路径、管理令牌、主密钥 |
| 运行时配置 | JSON / 管理 API | 是 | Bot、LLM、消息门控、检索、平台、反思和运维群 |
| LLM 密钥 | 加密 SecretStore | 是 | `llm.api_key_ref` 指向的 API Key |

ForgeBot 不加载 `.env` 文件。生产部署应由进程管理器、容器编排系统或操作系统注入环境变量。

## 2. 启动方式与优先级

```powershell
go run ./cmd/forgebot `
  -config D:/forgebot/configs/forgebot.yaml `
  -runtime-config D:/forgebot/configs/runtime.json
```

`-config` 可省略并改用 `FORGEBOT_CONFIG`。`-runtime-config` 的优先级高于环境变量和 YAML：

```text
-runtime-config > FORGEBOT_RUNTIME_CONFIG > YAML runtime_config_path
环境变量静态覆盖项 > YAML > 代码默认值
```

运行时 JSON 是独立配置层，普通环境变量不会覆盖其中字段。

路径解析规则：

- YAML 中的相对 `runtime_config_path` 和 `secret_store_path` 相对于 YAML 所在目录解析。
- 命令行或环境变量给出的路径不会相对于 YAML 目录重新定位。
- 生产环境建议全部使用绝对路径。

## 3. 环境变量

| 变量 | 用途 | 备注 |
| --- | --- | --- |
| `FORGEBOT_CONFIG` | 启动 YAML 路径 | 被 `-config` 覆盖 |
| `FORGEBOT_HTTP_ADDR` | HTTP 监听地址 | 覆盖 `server.addr` |
| `FORGEBOT_RUNTIME_CONFIG` | 运行时 JSON 路径 | 被 `-runtime-config` 覆盖 |
| `FORGEBOT_SECRET_STORE` | SecretStore 文件路径 | 覆盖 YAML 中的同类路径 |
| `FORGEBOT_ADMIN_TOKEN` | 管理 API Bearer Token | 生产环境推荐；覆盖 YAML |
| `FORGEBOT_SECRET_KEY` | SecretStore 主密钥 | 覆盖 YAML 中的 `secret_key` |
| `FORGEBOT_LOG_LEVEL` | 日志等级 | 同时覆盖 `log.console.level` 与 `log.file.level` |
| `FORGEBOT_LLM_API_KEY` | 首次初始化时的一次性 API Key | 仅被 `-init-api-key` 读取，不会写入配置文件 |

## 4. 启动 YAML

参考 [`configs/forgebot.example.yaml`](../configs/forgebot.example.yaml)：

```yaml
server:
  addr: 127.0.0.1:8080
  read_timeout: 10s
  write_timeout: 100s
  shutdown_timeout: 15s

log:
  console:
    enabled: true
    level: info
    format: text
  file:
    enabled: false
    level: info
    format: json
    path: logs/forgebot.log
    max_size_mb: 10
    rotate_after_days: 7
    max_age_days: 60
    compress: false

runtime_config_path: runtime.json
secret_store_path: secrets.json

# 管理 API 令牌与 SecretStore 主密钥；也可改由环境变量注入，环境变量优先。
admin_token: change-me
secret_key: base64-encoded-32-byte-master-key
```

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `server.addr` | `127.0.0.1:8080` | 管理 API、健康检查和测试消息入口的监听地址 |
| `server.read_timeout` | `10s` | HTTP 请求读取超时，必须大于 0 |
| `server.write_timeout` | `100s` | HTTP 响应写入超时，必须大于 0；调大 agent.timeout 时同步检查此静态限制 |
| `server.shutdown_timeout` | `15s` | 优雅停止期限，必须大于 0 |
| `log.console.enabled` | `true` | 是否输出到 stdout |
| `log.console.level` | `info` | `debug`、`info`、`warn` 或 `error`，不区分大小写 |
| `log.console.format` | `text` | `text` 或 `json` |
| `log.file.enabled` | `false` | 是否写入日志文件；关闭时仍保留下面的路径与轮转设置 |
| `log.file.level` | `info` | 同 `log.console.level` |
| `log.file.format` | `json` | `text` 或 `json` |
| `log.file.path` | 空 | 日志文件路径，开启文件输出时必填。相对路径以 YAML 所在目录为基准，目录不存在时自动创建 |
| `log.file.max_size_mb` | `10` | 单个日志文件上限（MB），写满即轮转，范围 1–10240 |
| `log.file.rotate_after_days` | `7` | 单个文件最长写入天数，到期即使未写满也轮转；`0` 表示只按大小轮转 |
| `log.file.max_age_days` | `60` | 备份保留天数，`0` 表示永久保留；不能小于 `rotate_after_days` |
| `log.file.compress` | `false` | 轮转出的备份是否压缩为 `.gz`，当前文件始终不压缩 |
| `runtime_config_path` | 空 | JSON 路径；为空时使用内置默认配置，但管理 API 无法持久化变更 |
| `secret_store_path` | 空 | 加密密钥库路径；要配置 LLM API Key 时必须设置 |
| `storage.backend` | `file` | 进程单选后端，首版仅文件实现，不支持热切换或故障切第二后端 |
| `admin_token` | 空 | 管理 API 令牌；不会出现在配置查询响应中 |
| `secret_key` | 空 | SecretStore 主密钥，Base64 编码的 32 字节；设置 `secret_store_path` 时必填，不会出现在配置查询或 doctor 输出中 |

YAML 只接受上述字段，未知字段、无效 duration 或多个 YAML document 会导致启动失败。旧的平铺写法（`log.level`、`log.format`、`log.file: 路径` 等）已不再接受，需改为 `log.console` / `log.file` 两段。

控制台与文件两路至少开启一路，否则启动失败。两路都开启时，每条日志按各自等级分别过滤：例如控制台为 `debug`、文件为 `info` 时，debug 日志只出现在控制台。

日志轮转在进程内完成：先关闭当前文件，再改名为 `forgebot-<UTC 时间>.log`（开启压缩时为 `.log.gz`），然后新建同名文件继续写，因此 Windows 与 Linux 都不需要 logrotate 等外部工具。按周期轮转约每小时检查一次；当前文件为空时不轮转。重启后沿用已有文件，开始时间取文件第一条日志的时间。备份按轮转时间清理，且只在发生轮转时清理，所以最旧的日志内容最多会比 `max_age_days` 再早一个轮转周期；长期无日志写入时旧备份也会暂时保留。同一个 `log.file.path` 只能由一个进程写入。

控制台关闭时，启动失败的错误会另写一行到 stderr；Go 的 panic 堆栈始终输出到 stderr，不进入日志文件。日志设置在进程启动时确定，修改后需要重启；排障时可临时设置 `FORGEBOT_LOG_LEVEL=debug` 再启动。

## 5. 运行时 JSON

可直接从 [`configs/runtime.example.json`](../configs/runtime.example.json) 复制。首次启动允许使用不带元数据的普通对象：

完整示例显式列出全部内置模块及其参数；只启用本地 Echo 渠道，可选功能（包括回复体验、短期记忆、主动介入、检索、反思、欢迎和群管理）明确关闭。需要的功能直接在对应位置启用；这属于完整示例的取值，不改变程序缺省行为或 `init` 生成的最小配置。

可选的 `decision.dm` **缺省关闭**：示例保留完整参数并写 `enabled=false`，改为 true 才启用；省略整个对象，或写了对象但不写 `enabled`，都等同于未启用。未启用时不解析其凭据、不发 DM 请求，直接用 LLM 作决策；已启用 DM 的调用失败直接返回错误，不改用 LLM。未启用的块字段仍需通过结构校验，避免潜藏拼写错误。DM Key 可在未启用期间通过管理接口预先写入，启用前先准备好凭据。旧版的 `disabled` 键已移除，残留会报未知字段，请改为 `"enabled": false`（原来写 `disabled: true` 的）。

**开关命名规则**：缺省启用的功能用 `disabled` 表示关闭（如 `conversation_memory.disabled`），缺省关闭的功能用 `enabled` 表示开启（如 `decision.dm.enabled`、模块与各群的 `enabled`）。看键名即可知道缺省状态，不必再查文档。

```json
{
  "bot": {"name": "ForgeBot", "system_prompt": ""},
  "llm": {
    "protocol": "openai_responses",
    "base_url": "https://api.openai.com/v1",
    "api_key_ref": "llm/default",
    "model": "gpt-5",
    "timeout": "60s"
  }
}
```

缺失字段按内置默认值补齐。通过管理 API 第一次写入后，文件会转换为带版本和历史记录的文档：

```json
{
  "schema_version": 1,
  "id": "runtime/default",
  "version": 2,
  "updated_at": "2026-09-12T10:00:00Z",
  "source": "admin",
  "runtime": {"...": "完整运行时配置"},
  "history": []
}
```

写入采用原子替换，保留最近 10 个历史版本，并将上一个有效文件保存在 `runtime.json.bak`。启动时主文件损坏而备份有效，ForgeBot 会加载备份并把配置来源报告为 `json_backup`。

日志配置已移到启动 YAML 的 `log`，见第 4 节；运行时 JSON 不再接受 `log` 字段。

### 5.2 `bot`

```json
{"name": "ForgeBot", "system_prompt": "你是项目支持助手。"}
```

- `name` 必填。
- `system_prompt` 可为空，用于设定 Bot 的全局行为。

### 5.3 `llm`

一次回答的总预算由顶层 `agent` 配置控制：`timeout` 缺省 `90s`、`max_turns=6`、`max_tool_calls=12`、`max_tools_per_turn=4`、`max_result_bytes=65536`、`max_context_bytes=131072`。它与 `llm.timeout` 的单次请求时限共同生效，采用更早截止时间；全部值可热更新。

工具串行执行并保留调用 ID。后续轮次继续通过同一 Hook、白名单与工具预算；最后一轮仅允许总结，预算不足不会自动重试。超大的工具结果替换为完整的预算错误 JSON，不截断成可能误导的半段依据；中间工具内容不直接发送给用户。预回复同次回答最多尝试一次。

```json
{
  "protocol": "openai_responses",
  "base_url": "https://api.openai.com/v1",
  "api_key_ref": "llm/default",
  "model": "gpt-5",
  "anthropic_version": "2023-06-01",
  "timeout": "60s"
}
```

| `protocol` | 请求端点 | 适用接口 |
| --- | --- | --- |
| `openai_responses` | `<base_url>/responses` | OpenAI Responses 兼容接口 |
| `openai_completions` | `<base_url>/chat/completions` | OpenAI Chat Completions 兼容接口 |
| `anthropic_messages` | `<base_url>/v1/messages` | Anthropic Messages 兼容接口 |

`base_url` 填 API 根地址，不要包含上表中的最终端点。`api_key_ref` 必填，它是 SecretStore 中的逻辑键名，不是 API Key 本身。`timeout` 必须大于 0。`anthropic_version` 仅对 Anthropic Messages 生效。

LLM 配置可热切换协议、供应商地址和模型。新配置初始化失败时，当前运行实例继续服务。

正式运行必须配置非空 `model` 和可解析的 API Key，不再提供无模型回显。
首次密钥通过 `-init-api-key` 在启动前写入；该初始化命令仍可在模型未配齐时运行。
`authentication` 缺省或 `api_key` 要求凭据；只有明确设置为 `none` 才允许无认证本地服务，且不会向该服务发送已存凭据。

`image_understanding` 为 boolean，缺省 `false`。当已经触发的问答包含当前或引用图片时，关闭状态直接回复“当前暂不支持图片”，不调用模型或检索；文字和图片混合时也不静默丢弃图片。设置 `true` 才继续多模态链路，供应商真实能力仍需验证。未 @ 消息的准入规则不因图片开关改变。这两项均随 LLM 配置热更新。

### 5.4 `admission`

### 业务范围与决策（`bot.system_prompt` / `decision`）

业务范围直接写在 `bot.system_prompt` 中，例如“只回答本项目的接口、部署与故障排查；可简短回应寒暄；其他领域的专业问题请礼貌说明不在服务范围内”。明确 @ 的消息直接交给回答模型，宿主不预先分类、拒答或澄清。这是提示词约束，属于尽力遵守：刻意构造的输入仍可能让模型越界，需要用目标模型实测。

未 @ 的主动插话另有一道判断，范围说明写在 `native.proactive_reply` 的 `scope` 选项里，见 6.6 节。

`decision` 缺省不配置专用 DM，语义判断独立使用回答 LLM；`dm_timeout=3s`、`llm_timeout=8s`、`max_concurrent=16` 可调整。专用 Jev 示例：

```json
{
  "decision": {
    "dm": {
      "enabled": true,
      "protocol": "jev",
      "endpoint": "https://api.typesafe.ai",
      "model": "jev-latest",
      "api_key_ref": "decision/default",
      "min_confidence": 0.6
    }
  }
}
```

`protocol` 选的是接口格式，目前只支持 `jev`（TypeSafe System One 的 `/v1/systemone` 请求和 choice 结果格式，与 `llm.protocol` 同类）；具体是哪家服务由 `endpoint` 决定，所以兼容这套格式的服务只需改 `endpoint`。旧版的 `provider` 键已改名为 `protocol`，残留会报未知字段。

DM Key 与回答 Key 一样存入加密凭据仓储。配置 DM 后可以通过受认证的 `GET/PUT/DELETE /v1/admin/decision/api-key` 查看状态、写入或删除，PUT 格式为 `{"api_key":"..."}`；GET 不返回明文。缺少 DM Key、DM 超时或结构无效时直接返回错误，不改用 LLM；合法 unknown 或低置信也不换模型争取肯定结果。当前 Jev 适配不启用图片资产，选用 DM 时含图决策返回 unsupported，主动插话因此保持沉默。

检索由回答模型按工具描述和系统提示词自行选择，宿主不做预检索。建议在 `bot.system_prompt` 中写明知识库覆盖哪些内容、涉及时先调用 `search_docs`，以及查不到时如何回答（例如说明不知道或建议联系管理员），减少模型不查就答的情况。

顶层 `conversation_memory` 配置进程内短期问答：缺省启用（`disabled=false`），`ttl=20m`、`max_pairs=6`、`max_sessions=1000`、每会话等待队列 `max_pending=8`、`max_session_bytes=65536`、`max_total_bytes=8388608`。配置可以热更新；关闭会清空已有上下文，重新打开不恢复旧内容。

上下文按逻辑群＋用户隔离，同会话按到达会话服务的顺序执行，排队计入 `agent.timeout`；超出等待或会话容量返回 busy。只保存已确认发送成功的必要问答（含回复格式 Hook 后实际发送的文字）；发送失败、结果未知、被抑制、工具中间数据与主动介入混合群消息均不保存。超出单会话容量的问答整对不保留，不静默截成错误依据。

顶层 `message_history` 配置**核心近期消息容器**：按群保存最近的群消息，包括收到的每条消息（普通成员、管理员、@ Bot 的消息和命令）和 Bot 发送成功的每条消息（回答、主动回复、欢迎语、运维回复）。容器**始终启用，没有开关**，只能调整上限：`max_messages=50`（每群条数，1..1000）、`ttl=30m`（保留时长，最长 24h）、`max_text_chars=1000`（每条文本截断长度）。写 `enabled` 或 `disabled` 会报未知字段。只记录已绑定的群；解绑后该群记录立即丢弃。只在进程内存中，不落盘、不保存图片内容（只记“含媒体”），热更新保留，重启清空。

群满后逐条滑动：每进一条，挤出最旧的一条；过期记录读不到，在下次写入时顺带清理。模块通过只读能力 `history.reader.v1` 读取（最近 N 条、某序号之后的记录、按平台消息 ID 查找），每条记录有群内递增的序号，可用“群 + 序号”稳定指向；并可订阅“新消息进入”和“消息移出”（带移出原因：满了、过期或调小上限）两种事件，事件在写入后按顺序同步投递，只投递给当前一代模块。适配器已解析的引用原文也保留为有界文本，引用 ID 必须与当前引用目标一致，正文和引用文本分别受 `max_text_chars` 限制，不保存引用媒体内容。Bot 发出的消息目前没有平台消息 ID；2026-11-10 已取消通用发送回执和按发送响应补全 history ID 的方案。原有成功出站正文记录保留，不扩展为跨连接器保证自身消息完整。它和短期问答（`conversation_memory`）是两回事：短期问答按“群 + 用户”只存某个用户与 Bot 之间成功的问答，供连续追问；近期消息容器按群保存整个群最近发生的对话，供模块判断上下文。

图片二进制和 data URL 不保存；每条用户上下文最多保留 4 个、每个不超过 2048 字节的图片 URL，是否仍有效由模型服务决定。后续关闭图片理解时，不再提交历史图片。无关配置热更新与 Bot 线程重启保留短期记忆，进程退出后清空；后端不写入完整群聊历史。有记忆仍然需要 @ 或现有独立触发策略，不自动回复未 @ 追问。

```json
{
  "dedup_ttl": "60s",
  "dedup_max": 10000
}
```

- `dedup_ttl`、`dedup_max`：消息去重有效期和容量，必须为正数。

消息合法性、身份解析、寻址判断和去重属于不可关闭的核心 Admission。未 @ 消息的
缓存、静默窗口和主动介入由 `native.proactive_reply` 模块配置。

### 5.5 `modules.native.structured_search`

```json
{
  "enabled": true,
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
}
```

- 启用时 `document_root` 必须是绝对路径。
- 扩展名只允许 `.md`、`.mdx`、`.txt`，且不能重复。
- `default_results` 不得大于 `max_results`；`min_match_ratio` 范围为 `(0, 1]`。
- `max_file_bytes` 不得大于 `max_total_bytes`；其余超时和资源上限必须为正数。

该模块拥有索引生命周期，并通过通用 LLM Tool 目录注册只读的 `search_docs`。普通
消息不会自动预检索；模型按需调用工具，`max_queries_per_turn` 限制单轮调用次数。
模块提供索引状态与重载 capability，不再注册 `/search` 命令。配置更新时会
先构建候选索引，成功后才切换；构建失败不会替换当前 generation。

### 5.6 `platforms`

OneBot 表情若附带 `summary`、`description`、`text` 或 `name`，适配器会保留为有界文字说明。没有可靠描述的表情继续忽略，不猜测 ID 对应含义；CQ 转义仅还原一层。文字、图片、引用和空 @ 的核心防护继续有效。

`modules` 决定协议实现是否装载；`platforms.connectors` 声明实际账号或端点实例；
`platforms.groups` 将外部目标映射为 Bot 核心使用的逻辑会话。三者职责不同，不能把
某个 QQ 账号或群号写进 `native.onebot.options`。

```json
{
  "modules": {
    "native.echo": {"enabled": true, "options": {}},
    "native.onebot": {
      "enabled": true,
      "options": {
        "timeout": "15s",
        "replay_ttl": "5m",
        "replay_max": 10000
      }
    }
  },
  "platforms": {
  "connectors": [
    {"id": "echo-local", "type": "echo", "enabled": true, "options": {}},
    {
      "id": "qq-main",
      "type": "onebot",
      "enabled": true,
      "prompt_suffix": "当前回复将发送到不支持 Markdown 的聊天渠道，请使用纯文本。",
      "options": {
        "api_url": "http://127.0.0.1:5700",
        "send_token": "",
        "receive_secret": "",
        "webhook_addr": "127.0.0.1:8090",
        "webhook_path": "/onebot/qq-main",
        "request_limit": 1048576,
        "timeout": "15s",
        "replay_ttl": "5m",
        "replay_max": 10000
      }
    }
  ],
  "groups": [
    {"id": "local", "connector_id": "echo-local", "target": "local"},
    {"id": "support", "connector_id": "qq-main", "target": "10001", "prompt_suffix": "这是技术交流群，请优先给出简洁、可执行的步骤。"}
  ]
  }
}
```

逻辑群 ID 如何被入群审批、欢迎、主动插话、管理员和运维群引用，见 [群管理：先把群绑定到 Bot](group-management.md#先把群绑定到-bot)；完整可加载的 QQ 示例见 [`configs/runtime.onebot.example.json`](../configs/runtime.onebot.example.json)。

`connectors` 支持同时启用多个实例。`type` 是 Provider 的稳定查找键；当前内置
`native.echo` 提供 `echo`，`native.onebot` 提供 `onebot`。连接器 ID 必须唯一。启用
实例所需的 Provider 未启用或不存在时，配置校验会拒绝该配置；仅启用 Provider 而不
创建实例是合法状态。启用的 OneBot 实例必须至少绑定一个群组。

`groups[].id` 是 Bot 核心使用的协议无关会话 ID；`connector_id` 指向已启用的连接器；`target` 是平台群号。同一连接器与 target 的组合不能重复。未提及消息的主动介入不再由平台群组字段控制，而由 `modules.native.proactive_reply.options.groups` 按逻辑群组配置。模块关闭或群组未启用时，未提及 Bot 的普通消息和媒体均不会调用 LLM；明确提及 Bot 和命令仍正常处理。

`connectors[].prompt_suffix` 和 `groups[].prompt_suffix` 都是可选的系统提示词追加内容。连接器级内容用于渠道通用约束（例如 QQ 使用纯文本），群组级内容用于该群组的语气、主题或行为约束。两者都存在时按“连接器后群组”的顺序拼接；都为空时不追加。它们属于运行时配置，可通过管理 API 热更新。

OneBot 的模块 options 可作为多个 OneBot 实例的默认值；实例 `options` 中出现的字段
逐字段覆盖模块默认值。建议将 `send_token` 和 `receive_secret` 放在实例层，以避免
不同账号意外共享凭据。实例可显式写空字符串覆盖模块层的 Token 或 Secret。

OneBot `options` 字段：

- `api_url`：OneBot 11 HTTP API 根地址。
- `send_token`：出站请求的 Bearer Token。
- `receive_secret`：使用 HMAC-SHA1 校验入站 `X-Signature`。
- `webhook_addr`、`webhook_path`：回调监听地址和路径，路径必须以 `/` 开头。
- `request_limit`：回调请求体字节上限。
- `timeout`：出站请求超时。
- `replay_ttl`、`replay_max`：入站事件防重放窗口和容量。

OneBot 入站消息包含引用段时，ForgeBot 会通过 `get_msg` 解析被引用消息，并将引用文字和图片作为独立上下文传给 LLM；引用内容不会并入当前消息文本，因此不会影响命令识别、消息准入或去重。

注意：当前 `send_token` 和 `receive_secret` 仍以明文保存在运行时 JSON 中；管理 API
的公开配置响应只显示它们是否已配置。回答/DM、MCP、外部审批及 QQ 官方 AppSecret 使用加密 SecretStore。未知
且禁用的未来 Provider 实例不会在公开响应中回显其原始 `options`。

QQ 官方机器人使用独立的 `qqbot` 类型和 `native.qqbot` 模块，默认关闭，Webhook、AppID/SecretStore、被动回复及撤回边界见 [QQ 官方接入](qqbot.md)。完整示例是 [`runtime.qqbot.example.json`](../configs/runtime.qqbot.example.json)，不要将其 options 混入 OneBot 实例。字段严格解析，secret_ref 不写真实 AppSecret。

#### OneBot 配置迁移（破坏式）

未发布版本不兼容旧字段。把每个 `connectors[].onebot` 对象原样移动为该连接器的
`connectors[].options`，并在 `modules` 中启用 `native.onebot`。旧 `onebot` 字段会被
严格拒绝，而不是静默兼容。

```text
旧：platforms.connectors[].onebot.api_url
新：platforms.connectors[].options.api_url
```

同理适用于全部 OneBot 私有字段。迁移后通过 `POST /v1/admin/config/validate` 验证，再
以完整 Runtime JSON 执行热更新。

### 5.7 `operator`

```json
{
  "operator": {
    "conversation_id": "ops",
    "notifications": true,
    "commands": ["status", "restart", "pause", "resume", "proactive", "reload_docs"]
  }
}
```

`operator` 指定**唯一的运维群**，不配置则不开放运维命令和故障通知。`conversation_id` 必须引用已绑定的逻辑群。**运维群里的所有成员都可以执行允许的运维命令**，所以这个群里只应该有可信的管理人员；需要多人管理时，把这些人拉进同一个运维群，而不是配置多个管理者。可授权命令只有：

- `/status`
- `/restart`
- `/pause`
- `/resume`
- `/proactive on|off`
- `/proactive yield <群ID>`、`/proactive resume <群ID>`：让指定业务群的主动插话暂停 5 分钟或提前恢复；不写群 ID 时作用于运维群本身
- `/reload-docs`

`notifications: true` 时，运维群接收 Runtime、Connector 和 LLM 故障通知。通讯侧运维能力刻意小于 Web 管理 API。目前 OneBot 适配器只处理群消息，因此运维管理者只能是一个群，不能是单个用户的私聊。

主动插话判断“管理员已接答”、知识沉淀收集“管理员回答”时，可信答疑人以连接器提供的平台身份为准：业务群里的群主和群管理员。这个身份不授予运维命令、ForgeBot 配置或凭据权限。旧配置中的 `administration` 与 `operations` 已删除，残留会导致配置加载失败。

### 5.8 `modules.native.knowledge_reflection`

```json
{
  "enabled": true,
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
```

反思模块只订阅核心近期消息容器的“新消息进入”。群主或管理员引用成员问题时，先查同群 history；问题已经滑出时，使用适配器已带回的引用原文，两处都没有正文才放弃本次沉淀。同群同一管理员在安静窗口内的后续答复归入该会话；普通成员消息不会再复制到独立的 24 小时候选问题池。

已锚定的问题与回答在模块中保存有界快照，即使原记录随后滑出 history，也可继续处理；模块不依赖“消息移出”事件，不向容器写业务标记或钉住记录。模块自己的定时任务处理静默或达到最长时长的会话，让 LLM 判断是否可沉淀为通用知识。接受的内容以托管 Markdown 原子写入 `extra_directory/*.md`，每批写入后统一重载索引；模型提炼、置信度、去重和发布行为保持不变。

v0.2.0 保留上述配置键，调整其约束对象：

| 字段 | 含义 |
| --- | --- |
| `scan_interval` | 检查已形成会话的批处理周期，默认 15m；达到安静窗口后在下一批处理，不是立即执行 |
| `quiet_window`、`max_session_duration` | 最后答复后等待时长与单会话最长时长，先到者决定处理资格 |
| `message_ttl` | 会话最后答复后的保留时长，及处理标记保留时长；会话另受“开始时间 + message_ttl + max_session_duration”限制，独立于 history 的 TTL |
| `max_messages_per_conversation`、`max_messages_total` | 已形成会话的问题与回答总条数上限，每个问题也计一条；超限释放整个最旧的非在途会话；处理标记分别受同样的每群/全局条数上限约束 |
| `max_message_chars` | 参与会话的问题或单条回答的最大字符数，超限不采纳；history 已保存的文本还受其 `max_text_chars` 截断上限约束 |
| `max_pairs_per_scan` | 单批最多处理的会话数；后台和手动批处理互斥 |

生成或发布失败时保留未过期的会话，后续批次再处理；成功、拒绝、低置信度和重复均结束该会话，释放正文并保留有界处理标记。容量释放和过期计数在状态中可见。模块禁用、热替换或重启不保留待处理会话，新运行代不重放旧回答，新到达的回答仍可引用容器保留的问题。

启用条件：

- 模块 `enabled: true`，不再存在重复的 `mode` 开关。
- `native.structured_search` 已启用，且 `extensions` 包含 `.md`。
- `conversation_ids` 非空、无重复并引用已绑定群组。
- `extra_directory` 可省略（默认 `extra`）。相对路径相对于
  `modules.native.structured_search.options.document_root`；绝对路径直接作为输出目录。
- LLM 模型及其 API Key 可用。
- 所有 duration 和容量为正数，`min_confidence` 范围为 `(0, 1]`。

可通过 `POST /v1/admin/reflection/scan` 立即处理一次已满足静默/最长时长条件的批次，不强行结束仍在回答的会话。状态由原来的 `buffer/scanner` 改为 `pending/worker/pipeline`，展示会话与标记数量、丢弃/过期计数及处理结果，不返回问题或回答正文。`init` 仍生成最小配置；完整示例的反思字段名和值保持有效，`doctor` 校验同一套模块配置和依赖。

#### 沉淀文件的保存、去重与更新

**每条成功沉淀的知识单独生成一个 Markdown 文件，不向一个总文件持续追加。** 一个问题和管理员的多条答复先归为答题会话，再由模型整理成一组问题与答案；只有被接受且达到 `min_confidence` 的结果才进入发布器。不是每收到一条问题或回答就立即建文件。

保存规则：

- 目录由 `extra_directory` 指定，默认知识库下的 `extra/`；相对路径基于检索模块的 `document_root`，绝对路径直接使用。
- 文件名为 SHA-256 哈希的前 16 位加 `.md`，例如 `extra/0123456789abcdef.md`；不用问题标题作文件名。哈希包含来源群、问题消息 ID、首条回答消息 ID，以及规范化后的标题、问题、答案和标签，不是纯文本内容的全局去重键。
- 每个文件包含 YAML frontmatter（标题、标签、`managed_by: forgebot`、来源群/问题/首条回答消息 ID、完整哈希、`created_at`），正文包含标题、问题和答案。`created_at` 使用答题会话开始时间；保存的是提炼后的知识，不是完整聊天记录。
- 写入先经过同目录临时文件，完整写入并同步后再重命名；发现目标文件名已被其他文件占用时报告冲突，不覆盖该文件，也不做半段追加。一批产生新文件后统一重载检索索引。

发布前只检查输出目录里的 ForgeBot 托管 Markdown：完整哈希相同，或者“来源群 + 问题消息 ID + 首条回答消息 ID”相同，即判为重复并跳过，沿用已有文件，不追加、不覆盖。即使同一来源重新提炼出了不同措辞或答案，来源三项相同仍跳过，不自动修订旧文件。

相同问题出现在另一条消息、另一群，或者对应另一条首回答消息时，来源身份不同，可能生成另一个文件。当前没有语义去重、跨会话知识合并、追加补充答案、版本替换或“新答案自动取代旧答案”的机制，也不会对手工知识文档做语义比对。需要修订已有知识时，由部署者维护文件后通过文档重载刷新索引；本次 history 重构沿用上述保存策略，没有修改发布器算法。

## 6. 配置 LLM API Key

`init` 会自动生成主密钥并写入 YAML 的 `secret_key`。手动配置时，先在 YAML 中设置 `secret_store_path`，再生成主密钥写入 `secret_key`：

```powershell
$keyBytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Fill($keyBytes)
[Convert]::ToBase64String($keyBytes)
```

希望主密钥不落在配置文件里的部署，可以改用环境变量 `FORGEBOT_SECRET_KEY` 注入，它优先于 YAML。无论放在哪里都必须妥善备份：丢失后无法解密原 SecretStore；更换该值前应先规划密钥迁移。

### 6.1 进程启动前初始化

不能提供一个可提交的 `secrets.json` 示例：该文件必须使用你自己的
主密钥加密，手写明文或复制他人的密文都不可用。首次启动
前，使用一次性 CLI 引导命令创建并写入 SecretStore：

```powershell
$env:FORGEBOT_LLM_API_KEY = "replace-with-provider-key"

go run ./cmd/forgebot `
  -config configs/forgebot.local.yaml `
  -init-api-key llm/default

Remove-Item Env:FORGEBOT_LLM_API_KEY
```

命令读取 YAML/环境变量中的 `secret_store_path`，将 `-init-api-key` 后的参数作为目标引用。
例如 `-init-api-key decision/default 'YOUR_KEY'`、`-init-api-key mcp/custom 'YOUR_KEY'`；引用参数必填，不再从 `llm.api_key_ref` 隐式选择。原值可直接跟在引用后，省略时从 `FORGEBOT_LLM_API_KEY` 读取；所有配置开关应放在原值之前。加密写入后立即退出，不改写运行配置。若该引用已经存在，命令
会拒绝覆盖，避免误操作；替换已有 Key 请使用下面的认证管理 API。

### 6.2 运行中替换

启动 ForgeBot 后，通过单用途接口写入或替换 API Key：

```powershell
$headers = @{ Authorization = "Bearer $env:FORGEBOT_ADMIN_TOKEN" }

Invoke-RestMethod `
  -Method Put `
  -Uri http://127.0.0.1:8080/v1/admin/llm/api-key `
  -Headers $headers `
  -ContentType application/json `
  -Body '{"api_key":"replace-with-provider-key"}'
```

`GET /v1/admin/llm/api-key` 只返回是否配置及更新时间，永远不会返回密钥值。正式实例要求回答凭据时，删除正在使用的必需 Key 会被拒绝，原值保留；先切到显式无认证配置后才可删除。切换 `api_key_ref` 前应确保新引用已有密钥。

### 6.3 按引用管理凭据

外部审批、MCP 等凭据可以通过 `GET/PUT/DELETE /v1/admin/secrets/{ref}` 管理，三个方法都要求管理端 Bearer 认证。`ref` 是 SecretStore 键名，例如 `review/token`，斜杠层级完整保留；它不是文件路径。CLI `-init-api-key` 仍只做首次写入，已有引用用此 API 替换，无需修改 Header 中的引用。

| 方法 | 请求正文 | 成功结果 |
| --- | --- | --- |
| GET | 无 | HTTP 200，返回 `configured`、`version`、`updated_at` 等状态，不返回明文 |
| PUT | `{"value":"NEW_TOKEN"}` | HTTP 200，创建或替换后返回状态，不返回明文 |
| DELETE | 无 | HTTP 204，无响应正文 |

PUT 正文使用 `value`，与回答/DM 单用途接口的 `api_key` 字段不同；未知字段和多余 JSON 会被拒绝。原有回答/DM 接口仍可用，两种入口共用加密仓储及候选激活生命周期，删除正在使用的必需回答/DM 凭据同样会被拒绝。

```powershell
$headers = @{ Authorization = "Bearer $env:FORGEBOT_ADMIN_TOKEN" }
$body = @{ value = 'replace-with-review-service-token' } | ConvertTo-Json -Compress
Invoke-RestMethod `
  -Method Put `
  -Uri http://127.0.0.1:8080/v1/admin/secrets/review/token `
  -Headers $headers `
  -ContentType application/json `
  -Body $body
```

`external_review.headers.Authorization` 写 `Bearer {review/token}`；替换成功后，后续外部审批请求读取新值，不重新发起已有审批。删除引用后，后续 HTTP 判断缺少凭据时不发送请求，按 unknown 参与 AND/OR 组合；它不会自动关闭审批。

连接器 Provider 可声明实例构建需要的凭据引用，宿主只把该实例声明的键和值交给它，不提供整个 SecretStore。启用的连接器缺少必需凭据时无法构建。使用中的连接器凭据替换会构建候选运行代和连接器集合；运行中先停止旧集合、启动候选集合，成功后激活并保存凭据，再发布新运行代。启动或保存失败时尝试恢复旧集合，旧凭据保持不变；恢复失败会返回错误。此流程会重启整个连接器集合，期间入站等待换代，等待可随调用上下文取消。

删除仍被启用连接器引用的必需凭据会失败；先停用对应连接器或改用已准备好的新引用，再删除旧引用。未被启用连接器、回答模型或已启用 DM 使用的引用，直接沿用凭据仓储的写入/删除流程。外部审批仍在后续请求读取当前值，不触发连接器重启。

## 6.5 外部扩展（MCP 与 Skill）

应用层扩展位于运行时 JSON 的 `extensions` 字段，因此可以通过管理 API
热更新。MCP 支持显式配置的 Streamable HTTP 端点和 stdio 子进程；ForgeBot
启动候选 runtime 时执行 `initialize` 和 `tools/list`，随后仅暴露 `allowed_tools` 中
的工具。工具名统一为 `mcp.<server_id>.<tool_name>`，LLM 无法指定服务地址、
认证方式或未授权工具。MCP 凭据使用 `api_key_ref` 指向 SecretStore，永远不
进入配置脱敏响应和模型上下文。

stdio 配置使用 `command`、`args`、`working_directory` 和 `environment`。
命令通过操作系统进程 API 直接启动，不经过 shell；子进程 stdout 必须只输出
逐行 JSON-RPC，普通日志应写入 stderr。请求超时会终止子进程，候选 runtime
构建失败并保留旧 generation。

`extensions.skills.directories` 是本地 `SKILL.md` 目录列表。加载器按路径
稳定排序并限制单文件、总字节数和文件数量，将正文作为外部 instruction 快照
注入 LLM；Skill 不执行脚本、不拥有网络或文件写权限。目录变化可通过保存一
次运行时配置或 `/v1/admin/runtime/restart` 生效。扩展连接或 Skill 解析失败
时，候选 generation 不会发布，旧运行时继续服务。

### 6.5.1 使用 MCP 工具的完整流程

下面以提供 `list_models` 和 `get_model_pricing` 的 MCP 服务为例。MCP 服务需要
先独立启动并确认可用；ForgeBot 负责连接、发现和调用工具。

1. **确认服务和工具名**：服务应支持 MCP JSON-RPC 的 `initialize`、
   `notifications/initialized`、`tools/list` 和 `tools/call`。记录要开放的原始
   工具名，例如 `list_models`、`get_model_pricing`。
2. **配置 Streamable HTTP**：在 runtime JSON 的 `extensions.mcp` 中加入：

   ```json
   {
     "id": "platform",
     "enabled": true,
     "transport": "streamable_http",
     "endpoint": "https://mcp.example.com/mcp",
     "api_key_ref": "llm/default",
     "allowed_tools": ["list_models", "get_model_pricing"],
     "timeout": "10s",
     "max_response_bytes": 1048576
   }
   ```

   `endpoint` 是 MCP 服务地址，不是 ForgeBot 管理地址。模型可见的工具名会变为
   `mcp.platform.list_models` 和 `mcp.platform.get_model_pricing`；
   `allowed_tools` 始终填写 MCP 返回的原始名称。
3. **配置认证**：`api_key_ref` 是 SecretStore 引用，不是明文 Key。示例复用
   `llm/default`；不需要认证时可省略。当前管理 API 的写入接口固定更新
   `llm.api_key_ref`，不能额外提交任意 `ref` 字段。独立 MCP Key 应在初始化阶段
   预置到加密 SecretStore，禁止写入 runtime JSON 或 Git。
4. **配置 stdio（本地进程）**：

   ```json
   {
     "id": "local-tools",
     "enabled": true,
     "transport": "stdio",
     "command": "node",
     "args": ["./server.js"],
     "working_directory": "D:/forgebot/extensions/local-tools",
     "environment": {"TOOL_MODE": "readonly"},
     "allowed_tools": ["list_models"],
     "timeout": "10s",
     "max_response_bytes": 1048576
   }
   ```

   ForgeBot 直接启动 `command`，不经过 shell。stdout 只能输出逐行 JSON-RPC，
   日志写 stderr；Windows 下确认命令在 `PATH` 中且工作目录存在。
5. **应用并验证**：MCP 属于 runtime。提交完整 runtime 更新，或保存后调用
   `POST /v1/admin/runtime/restart`。启动日志应显示 MCP 初始化和工具发现成功。
   发送“列出当前可用模型及价格”等明确需要工具的问题，debug 日志应出现工具
   调用和执行结果。普通闲聊不会自动调用 MCP。
6. **热更新与回滚**：更新是完整 runtime 替换，不是 JSON Merge Patch，并需要
   正确的 `expected_version`。ForgeBot 会先连接候选 MCP；连接失败时旧 generation
   保持运行。更新成功后旧连接关闭，新连接生效。

### 6.5.2 MCP 安全与排错

- `allowed_tools` 只列出必要工具，避免暴露写入、管理或任意执行能力。
- Streamable HTTP 优先使用 HTTPS；stdio 子进程继承 ForgeBot 的操作系统权限。
- `timeout`、`max_response_bytes` 超限或子进程退出会使调用失败。
- 模型不调用工具时，依次检查工具发现、原始工具名匹配、模型工具调用能力和系统提示词。

## 6.6 原生功能模块与 Hook

`runtime.modules` 按完整功能模块配置，不按单个 Hook 启停。当前内置模块包括
`native.reply_experience` 和 `native.proactive_reply`。前者控制空 @、图片提示和
检索预回复；后者控制未 @ 消息的有界缓存和谨慎主动介入。

```json
{
  "modules": {
    "native.reply_experience": {
      "enabled": true,
      "options": {
        "empty_mention_prompt": "请简单介绍一下你自己。",
        "image_only_prompt": "请读取图片并直接处理图片中提出的问题。",
        "search_progress_text": "正在检索相关文档，请稍候。"
      }
    },
    "native.proactive_reply": {
      "enabled": false,
      "options": {
        "scope": "本项目的接口、部署与故障排查",
        "silence": "4s",
        "max_window": "25s",
        "max_buffered": 50,
        "max_text_len": 500,
        "max_concurrent": 4,
        "groups": {
          "support": {"enabled": true}
        }
      }
    }
  }
}
```

这是完整 runtime 内的一段配置，不是可以单独发送的 PATCH 请求。
options 可省略或仅填写部分字段；缺省字段使用内置文案，已填写值必须为
1–4096 字节的非空白字符串，不接受 `null` 或未知字段。模块级 options
不超过 16 KiB，总 options 不超过 256 KiB，配置模块项不超过 64 个。

关闭模块后：

- 空 @ 没有其他处理器补充正文时返回 `empty_input`，不调用 LLM、不发错误回复。
- `llm.image_understanding=true` 时纯图片仍传给模型，但不自动追加图片分析提示；false 时由核心返回不支持图片。
- 文档检索仍然可用，只是不发送检索预回复。
- 普通文字与获准处理的图文沿用统一问答链；检索始终通过工具调用。

`native.proactive_reply` 默认关闭。启用后仍需在 `options.groups` 中按逻辑群组 ID
显式打开；`silence` 和 `max_window` 控制等待时间，`max_buffered`、
`max_text_len` 和 `max_concurrent` 提供资源边界。`context_messages`（缺省 10，0..50，0 表示不附带）是决策时从核心近期消息容器附带的近期群聊条数，建议不超过 `message_history.max_messages` 的 60%。模块依赖核心近期消息容器，宿主始终提供。

每批待答消息在交给回答模型前先做一次决策（有 DM 用 DM，否则用回答 LLM），判断是否是一个值得主动回答、仍需帮助、尚无管理员接手的明确问题。决策同时带上这批消息之前的近期群聊（取自核心容器，标出管理员和 Bot 的发言，不重复这批消息），所以管理员没有引用、只是直接回答或纠正了同一话题时，同一次决策也会判为不值得回答，不增加模型调用。填写 `scope`（最多 3072 字节）后，还要求问题属于该范围。只有明确“是”才继续；否、无法判断、决策故障或批次超过 3000 字符都保持沉默。通过后，回答模型的指令末尾还会附一句宿主提示：若问题不在服务范围内或不需要回答，就只输出空内容，空回复不会发送。`scope` 只供这次决策使用，不注入回答模型；回答的范围仍以 `bot.system_prompt` 为准，二者应保持一致。

业务群的群主或群管理员引用 ID 命中同群待处理、生成中，或 Bot 在 `yield_duration` 内刚主动回答过的问题作答
时（后一种对应管理员在 Bot 答完后纠正或补充），模块按群暂时让位，缺省 `yield_duration=5m`，可在模块 options 中调整。平台群主/管理员只作为可信接答身份，不自动授予 ForgeBot 配置或凭据权限。无待答问题的普通管理员闲聊不调用接答模型，也不刷新让位时长；有待答/生成中问题时可通过共享 Decision 判断语义接答。

让位只暂停未 @ 的主动插话；同条消息引用问题并 @Bot 时仍走正常问答。旧待答项丢弃，已生成的主动回复在发送前复查群版本，即使到期也不补发。“刚回答过的问题”查的是核心近期消息容器里 Bot 回复所引用的那条消息，热更新后仍然有效，进程重启清空；一批多条消息时只记引用的那一条，其余由上面的近期上下文判断覆盖。引用 Bot 自己发出的回复消息暂不触发（适配器目前不记录 Bot 发出消息的 ID）。状态跨无关热更新保留，进程重启清空。运维群的 `proactive` 命令增加 `/proactive yield <群ID>`（让指定群让位 5 分钟）和 `/proactive resume <群ID>`（提前恢复）；这与全局 pause/resume 相互独立。

检索预回复只在工具通过白名单和预算校验且实际准备执行时产生。
每次请求最多尝试一次；发送失败不阻止检索。它是程序行为，不是开放给 LLM 的发送工具。

模块变化通过管理 API 的完整配置更新、版本检查和回滚生效，沿用 Engine
重建路径，不重启整个进程。旧请求沿旧 Hook 链完成，关闭模块不撤销已在执行的请求。
旧 generation 超过退场宽限仍未释放时，再次重建返回 HTTP 409，错误包含
`runtime_busy`；等待旧请求退出后重试，避免重复点击重建造成资源堆积。

`GET /v1/admin/config` 显示模块有效开关和允许公开的文案；
`GET /v1/admin/runtime` 的 `modules` 显示模块 ID、来源、状态及挂载节点。
存储仍是 `settings.Store` 接口下的 JSONFileStore，本期无 DBStore 或多文件配置。

未知模块 ID 会被拒绝。外部 FPP 插件尚未实现，不能仅往 JSON 添加一个 ID 就动态
加载 Go 代码。结构化检索和反思仍待后续迁移；主动介入已经由模块配置管理。当前
发送 Hook 仅覆盖 Engine 的普通、主动和进度回复，不包含管理命令及运维通知。

## 7. 管理 API 与热更新

所有 `/v1/admin/*` 请求都必须携带：

```http
Authorization: Bearer <FORGEBOT_ADMIN_TOKEN>
```

可用端点：

```text
GET    /v1/admin/config
GET    /v1/admin/config/history
POST   /v1/admin/config/validate
PUT    /v1/admin/config
POST   /v1/admin/config/rollback
GET    /v1/admin/runtime
POST   /v1/admin/runtime/restart
POST   /v1/admin/search/reload
POST   /v1/admin/reflection/scan
GET    /v1/admin/llm/api-key
PUT    /v1/admin/llm/api-key
DELETE /v1/admin/llm/api-key
```

### 7.1 推荐更新流程

运行时更新是**完整替换**，不是 JSON Merge Patch。不要只发送想修改的局部字段。

`GET /v1/admin/config` 返回脱敏视图：它不包含 `llm.api_key_ref`，OneBot 的 `send_token` 和 `receive_secret` 也只显示是否已配置。因此该响应用于查看状态和取得当前 `version`，**不能原样作为全量更新基线**。当前版本应以受操作系统权限保护的运行时 JSON 或部署配置源中的完整 runtime 为基线。

1. 调用 `GET /v1/admin/config` 获取当前 `version` 和脱敏状态。
2. 从受保护的运行时 JSON 或部署配置源取得完整 runtime，在副本中修改目标字段。
3. 将完整 runtime 对象直接发送到 `POST /v1/admin/config/validate` 校验。
4. 校验通过后，把它包进下述更新请求并发送到 `PUT /v1/admin/config`。

更新请求：

```json
{
  "expected_version": 1,
  "runtime": {"...": "完整运行时配置"},
  "source": "admin:model-switch",
  "force": false
}
```

`expected_version` 提供乐观并发控制。版本不一致时拒绝写入，客户端应重新读取并合并；`force: true` 会绕过版本检查，只应在明确接受覆盖并发修改时使用。

配置应用和持久化作为一个事务处理：候选组件启动或文件持久化失败时，ForgeBot 回滚到旧的活动配置。某些变更会重建或重启对应的 Bot、LLM、检索或平台连接器，但不要求整个进程重启。

这项全量更新与脱敏读取之间的不对称是当前管理 API 的已知限制。构建通用 Web 管理面板前，需要提供服务端字段级更新或不可见字段保留语义；在此之前，不应让面板根据脱敏响应拼装全量配置。

### 7.2 回滚

先通过 `GET /v1/admin/config/history` 查询可用版本，再发送：

```json
{
  "target_version": 1,
  "expected_version": 3,
  "source": "admin:rollback"
}
```

回滚会生成一个新版本，不会让版本号倒退。历史默认只保留最近 10 个旧版本。

## 8. 最小本地启动

```powershell
Copy-Item configs/forgebot.example.yaml configs/forgebot.local.yaml
Copy-Item configs/runtime.example.json configs/runtime.json
```

将 `forgebot.local.yaml` 中的 `runtime_config_path` 改为 `runtime.json`。正式启动前先按第 6 节初始化 SecretStore，并配置回答模型；无认证本地服务需显式设置 `llm.authentication=none`。缺少必需配置会在启动时报告错误，不再以回显伪装模型问答。

```powershell
go test ./...
go run ./cmd/forgebot -config configs/forgebot.local.yaml
```

另一个终端检查：

```powershell
Invoke-RestMethod http://127.0.0.1:8080/healthz

Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:8080/v1/messages `
  -ContentType application/json `
  -Body '{"conversation_id":"local","user_id":"u1","message_id":"m1","text":"hello ForgeBot","mentioned":true}'
```

## 9. 生产部署检查

- 使用绝对路径配置 YAML、运行时 JSON、SecretStore 和文档库。
- 使用强随机 `admin_token` 与 `secret_key`。可写在 YAML 中并限制文件权限，也可通过 `FORGEBOT_ADMIN_TOKEN` / `FORGEBOT_SECRET_KEY` 由进程管理器或密钥系统注入。
- 单独备份主密钥；希望密钥与密文分开存放时，把 `secret_store_path` 指到配置目录之外，或改用环境变量注入主密钥。
- 限制 YAML、JSON、SecretStore 及备份文件的操作系统访问权限。
- 管理 API 不直接暴露到公网；使用防火墙、反向代理和 TLS 限制访问。
- 设置明确的 HTTP、LLM、检索超时与资源上限。
- OneBot 启用 `receive_secret`，并保护当前仍在 JSON 中的 OneBot 密钥。
- 在启用自动反思前确认文档库备份策略和管理员名单。
- 监控 `/healthz`、结构化日志及运维群故障通知。

## 入群自动审批

全字段见 [`runtime.example.json`](../configs/runtime.example.json)（模块与群关闭）；正则、HTTP、模型三项 AND 组合的完整 OneBot 配置见 [`runtime.onebot.external-review.example.json`](../configs/runtime.onebot.external-review.example.json)。[群管理说明](group-management.md#外部-http-审批)列出各项启停方式、请求/响应示例和凭据初始化步骤。总示例的 `external_review` 已配置，单独关闭它应删除整个对象；模型通过 `model_enabled` 控制。

入群审批可在 `modules.native.join_review.options.external_review` 配置 HTTP 判断，和正则/模型共用 AND/OR；完整配置、有限表达式语法、JSON 模板与凭据引用见[外部 HTTP 审批](group-management.md#外部-http-审批)。请求限时 5 秒、响应最大 64KiB，字段严格解析，外部配置非空即启用，不另加开关。

入群自动审批由 `modules.native.join_review` 配置，默认关闭。规则在模块 `options` 中统一写一份，所有开启的群共用：`regex`（可选 Go 正则，RE2 语法，最多 4096 字节）、`model_enabled`（是否使用共享模型决策）、`rule`（模型判断标准，最多 4096 字节）、`combine`（`and` 默认或 `or`）与 `reject_reason`（最多 2048 字节的固定拒绝理由）。`options.groups.<群ID>.enabled` 决定该群是否开启。顶层 `group_management` 已删除。

多个判断启用时，固定按成本升序串行执行：**正则 → 外部 HTTP 校验 → 模型判断**；跳过未启用项，不并发执行。AND 遇到不通过即拒绝，OR 遇到通过即同意，短路后不执行后续判断。外部 HTTP 失败或超时为 unknown：AND 立即拒绝，OR 继续；配置外部判断时最终没有通过则拒绝。未配置外部判断时，保留模型失败或无法判断时不操作的行为，由管理员在原平台处理。模型开关缺省时，非空 `rule` 表示启用模型；显式 false 可保留规则但关闭模型。启用自动审批必须至少启用一种判断。完整示例及凭据初始化见 [群管理](group-management.md#外部-http-审批)；凭据轮换见[按引用管理凭据](#63-按引用管理凭据)。

单次工具结果默认 64 KiB，包含 JSON 信封和转义开销，可容纳默认 12000 字符的正常中文检索上下文。字符数不等于字节数；显式更小预算或自定义更大检索输出仍可能得到 `budget_exhausted`。
