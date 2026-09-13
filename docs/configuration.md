# ForgeBot 配置手册

ForgeBot 将启动所必需且不应频繁变化的配置放在 YAML 中，将应用层配置放在可写的 JSON 中。LLM API Key 不进入任一配置文件，而是通过管理 API 写入加密的 SecretStore。

## 1. 配置分层

| 层级 | 介质 | 是否热更新 | 内容 |
| --- | --- | --- | --- |
| 启动配置 | YAML / 少量环境变量 | 否，修改后重启进程 | HTTP 监听、运行时配置路径、密钥库路径、管理令牌 |
| 运行时配置 | JSON / 管理 API | 是 | 日志、Bot、LLM、消息门控、检索、平台、管理员、反思和运维群组 |
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
| `FORGEBOT_SECRET_KEY` | SecretStore 主密钥 | Base64 编码的 32 字节随机值 |
| `FORGEBOT_LLM_API_KEY` | 首次初始化时的一次性 API Key | 仅被 `-init-api-key` 读取，不会写入配置文件 |

## 4. 启动 YAML

参考 [`configs/forgebot.example.yaml`](../configs/forgebot.example.yaml)：

```yaml
server:
  addr: 127.0.0.1:8080
  read_timeout: 10s
  write_timeout: 30s
  shutdown_timeout: 15s

runtime_config_path: runtime.json
secret_store_path: secrets.json

# 本地开发可使用；生产环境改用 FORGEBOT_ADMIN_TOKEN。
admin_token: change-me
```

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `server.addr` | `127.0.0.1:8080` | 管理 API、健康检查和测试消息入口的监听地址 |
| `server.read_timeout` | `10s` | HTTP 请求读取超时，必须大于 0 |
| `server.write_timeout` | `30s` | HTTP 响应写入超时，必须大于 0 |
| `server.shutdown_timeout` | `15s` | 优雅停止期限，必须大于 0 |
| `runtime_config_path` | 空 | JSON 路径；为空时使用内置默认配置，但管理 API 无法持久化变更 |
| `secret_store_path` | 空 | 加密密钥库路径；要配置 LLM API Key 时必须设置 |
| `admin_token` | 空 | 管理 API 令牌；不会出现在配置查询响应中 |

YAML 只接受上述字段，未知字段、无效 duration 或多个 YAML document 会导致启动失败。

## 5. 运行时 JSON

可直接从 [`configs/runtime.example.json`](../configs/runtime.example.json) 复制。首次启动允许使用不带元数据的普通对象：

```json
{
  "log": {"level": "info", "format": "text"},
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
  "version": 2,
  "updated_at": "2026-09-12T10:00:00Z",
  "source": "admin",
  "runtime": {"...": "完整运行时配置"},
  "history": []
}
```

写入采用原子替换，保留最近 10 个历史版本，并将上一个有效文件保存在 `runtime.json.bak`。启动时主文件损坏而备份有效，ForgeBot 会加载备份并把配置来源报告为 `json_backup`。

### 5.1 `log`

```json
{"level": "info", "format": "text"}
```

- `level`：`debug`、`info`、`warn` 或 `error`。
- `format`：`text` 或 `json`。

### 5.2 `bot`

```json
{"name": "ForgeBot", "system_prompt": "你是项目支持助手。"}
```

- `name` 必填。
- `system_prompt` 可为空，用于设定 Bot 的全局行为。

### 5.3 `llm`

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

### 5.4 `gate`

```json
{
  "enabled": true,
  "require_mention": true,
  "silence": "4s",
  "max_count": 8,
  "max_window": "25s",
  "dedup_ttl": "60s",
  "dedup_max": 10000,
  "max_buffered": 50,
  "max_text_len": 500,
  "max_concurrent": 4
}
```

- `enabled`：启用消息门控。
- `require_mention`：群消息是否必须提及 Bot。
- `silence`、`max_count`、`max_window`：消息聚合的静默期、数量和时间窗口。
- `dedup_ttl`、`dedup_max`：消息去重有效期和容量，必须为正数。
- `max_buffered`：单次缓冲消息上限。
- `max_text_len`：单条输入保留的最大字符数。
- `max_concurrent`：并发处理上限，必须为正数。

除 `dedup_ttl`、`dedup_max` 和 `max_concurrent` 外的数值限制项可以为 0，不能为负数。

### 5.5 `search`

```json
{
  "enabled": true,
  "policy": "on_demand",
  "backend": "structured",
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
```

- 当前只支持 `policy: on_demand` 和 `backend: structured`。
- 启用时 `document_root` 必须是绝对路径。
- 扩展名只允许 `.md`、`.mdx`、`.txt`，且不能重复。
- `default_results` 不得大于 `max_results`；`min_match_ratio` 范围为 `(0, 1]`。
- `max_file_bytes` 不得大于 `max_total_bytes`；其余超时和资源上限必须为正数。

普通消息不会自动预检索。模型可按需调用只读的 `search_docs` 工具，`max_queries_per_turn` 限制单轮调用次数；用户也可发送 `/search <query>` 直接检索。搜索配置更新时会先构建候选索引，成功后才切换；构建失败不会替换当前索引。

### 5.6 `platforms`

```json
{
  "connectors": [
    {"id": "echo-local", "type": "echo", "enabled": true},
    {
      "id": "qq-main",
      "type": "onebot",
      "enabled": true,
      "prompt_suffix": "当前回复将发送到不支持 Markdown 的聊天渠道，请使用纯文本。",
      "onebot": {
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
```

`connectors` 支持同时启用多个连接器，当前类型为 `echo` 和 `onebot`。ID 必须唯一。启用的 OneBot 连接器必须至少绑定一个群组。

`groups[].id` 是 Bot 核心使用的协议无关会话 ID；`connector_id` 指向已启用的连接器；`target` 是平台群号。同一连接器与 target 的组合不能重复。

`connectors[].prompt_suffix` 和 `groups[].prompt_suffix` 都是可选的系统提示词追加内容。连接器级内容用于渠道通用约束（例如 QQ 使用纯文本），群组级内容用于该群组的语气、主题或行为约束。两者都存在时按“连接器后群组”的顺序拼接；都为空时不追加。它们属于运行时配置，可通过管理 API 热更新。

OneBot 字段：

- `api_url`：OneBot 11 HTTP API 根地址。
- `send_token`：出站请求的 Bearer Token。
- `receive_secret`：使用 HMAC-SHA1 校验入站 `X-Signature`。
- `webhook_addr`、`webhook_path`：回调监听地址和路径，路径必须以 `/` 开头。
- `request_limit`：回调请求体字节上限。
- `timeout`：出站请求超时。
- `replay_ttl`、`replay_max`：入站事件防重放窗口和容量。

注意：当前 `send_token` 和 `receive_secret` 仍以明文保存在运行时 JSON 中；管理 API 的公开配置响应只显示它们是否已配置。只有 LLM API Key 已接入加密 SecretStore。

### 5.7 `administration`

```json
{
  "administrators": [
    {"conversation_id": "support", "user_id": "12345678"}
  ]
}
```

管理员按“逻辑群组 + 平台用户 ID”授权。`conversation_id` 必须引用 `platforms.groups[].id`。该身份用于通讯侧有限能力和知识反思，不等同于 Web 管理 API 权限。

### 5.8 `operations`

```json
{
  "groups": [{
    "conversation_id": "support",
    "notifications": true,
    "commands": ["status", "restart", "pause", "resume", "gate", "reload_docs"]
  }]
}
```

运维群组必须引用已绑定的逻辑群组。可授权命令只有：

- `/status`
- `/restart`
- `/pause`
- `/resume`
- `/gate on|off`
- `/reload-docs`

`notifications: true` 时，该群组接收 Runtime、Connector 和 LLM 故障通知。通讯侧运维能力刻意小于 Web 管理 API。

### 5.9 `reflection`

```json
{
  "enabled": true,
  "mode": "auto",
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
```

反思模块定时扫描内存中的群消息，识别管理员对用户问题形成的多消息回答，并让 LLM 判断是否可沉淀为通用知识。接受的内容以托管 Markdown 原子写入 `<document_root>/extra/*.md`，每批写入后统一重载索引；不保存完整聊天历史。

启用条件：

- `enabled: true` 且 `mode: auto`；停用时使用 `mode: off`。
- 搜索已启用，`extensions` 包含 `.md`。
- `conversation_ids` 非空、无重复并引用已绑定群组。
- `extra_directory` 固定为 `extra`。
- LLM 模型及其 API Key 可用。
- 所有 duration 和容量为正数，`min_confidence` 范围为 `(0, 1]`。

可通过 `POST /v1/admin/reflection/scan` 立即触发一次扫描。运行状态只暴露计数器，不返回问题或回答正文。

## 6. 配置 LLM API Key

先在 YAML 中配置 `secret_store_path`，再生成主密钥。以下 PowerShell 变量仅在当前终端进程中有效；生产环境应持久配置到进程管理器或密钥系统中。

```powershell
$keyBytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Fill($keyBytes)
$env:FORGEBOT_SECRET_KEY = [Convert]::ToBase64String($keyBytes)
$env:FORGEBOT_ADMIN_TOKEN = "replace-with-a-strong-token"
```

必须妥善保管 `FORGEBOT_SECRET_KEY`。丢失后无法解密原 SecretStore；更换该值前应先规划密钥迁移。

### 6.1 进程启动前初始化

不能提供一个可提交的 `secrets.json` 示例：该文件必须使用你自己的
`FORGEBOT_SECRET_KEY` 加密，手写明文或复制他人的密文都不可用。首次启动
前，使用一次性 CLI 引导命令创建并写入 SecretStore：

```powershell
$env:FORGEBOT_SECRET_KEY = "base64-encoded-32-byte-master-key"
$env:FORGEBOT_LLM_API_KEY = "replace-with-provider-key"

go run ./cmd/forgebot `
  -config configs/forgebot.local.yaml `
  -init-api-key

Remove-Item Env:FORGEBOT_LLM_API_KEY
```

命令会读取 YAML/环境变量中的 `secret_store_path` 和运行时 JSON 中的
`llm.api_key_ref`，加密写入对应引用后立即退出。若该引用已经存在，命令
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

`GET /v1/admin/llm/api-key` 只返回是否配置及更新时间，永远不会返回密钥值。`DELETE` 可删除当前 `llm.api_key_ref` 对应的密钥。切换 `api_key_ref` 后需为新引用单独写入密钥。

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

将 `forgebot.local.yaml` 中的 `runtime_config_path` 改为 `runtime.json`。若暂时不调用 LLM，可保持 API Key 未配置并先验证健康检查和管理接口；消息处理会进入 LLM 流程，要测试完整 echo 链路需先按第 6 节初始化 SecretStore。

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
- 使用强随机 `FORGEBOT_ADMIN_TOKEN`，不要在 YAML 中保存生产令牌。
- 将 `FORGEBOT_SECRET_KEY` 交由部署环境或密钥系统保管并备份。
- 限制 YAML、JSON、SecretStore 及备份文件的操作系统访问权限。
- 管理 API 不直接暴露到公网；使用防火墙、反向代理和 TLS 限制访问。
- 设置明确的 HTTP、LLM、检索超时与资源上限。
- OneBot 启用 `receive_secret`，并保护当前仍在 JSON 中的 OneBot 密钥。
- 在启用自动反思前确认文档库备份策略和管理员名单。
- 监控 `/healthz`、结构化日志及运维群组故障通知。
