# 配置字段索引

按配置文件里的字段反查说明文档。顺序与 [`configs/forgebot.example.yaml`](../configs/forgebot.example.yaml)、[`configs/runtime.example.json`](../configs/runtime.example.json) 一致。运行时配置只维护这一个完整示例，所有平台和功能字段都在其中列出；具体启用和绑定方法见相应文档。

键名约定：缺省启用的功能用 `disabled` 关闭，缺省关闭的功能用 `enabled` 开启，见 [配置手册](configuration.md)。

## 启动 YAML（改动需重启）

| 字段 | 说明 |
| --- | --- |
| `storage.backend` | [配置手册 §4](configuration.md#4-启动-yaml)、[存储与数据恢复](storage.md) |
| `server.addr` `server.read_timeout` `server.write_timeout` `server.shutdown_timeout` | [配置手册 §4](configuration.md#4-启动-yaml) |
| `log.console.enabled` `level` `format`；`log.file.enabled` `level` `format` `path` `max_size_mb` `rotate_after_days` `max_age_days` `compress` | [配置手册 §4](configuration.md#4-启动-yaml)；环境变量 `FORGEBOT_LOG_LEVEL` 见 [§3](configuration.md#3-环境变量) |
| `runtime_config_path` | [配置手册 §2 启动方式与优先级](configuration.md#2-启动方式与优先级) |
| `secret_store_path` `secret_key` | [配置手册 §4](configuration.md#4-启动-yaml)、[§6 配置 LLM API Key](configuration.md#6-配置-llm-api-key)、[按引用管理凭据](configuration.md#63-按引用管理凭据)、[存储与数据恢复](storage.md) |
| `admin_token` | [配置手册 §4](configuration.md#4-启动-yaml)、[§7 管理 API](configuration.md#7-管理-api-与热更新) |

## 运行时 JSON（可热更新）

顶层字段按文件中的顺序排列。

### `bot`、`llm`、`decision`

| 字段 | 说明 |
| --- | --- |
| `bot.name` | [配置手册 §5.2](configuration.md#52-bot) |
| `bot.system_prompt` | [配置手册 §5.2](configuration.md#52-bot)；业务范围的写法见 [业务范围与决策](configuration.md#业务范围与决策botsystem_prompt--decision) 一节 |
| `llm.protocol` `llm.base_url` `llm.model` `llm.anthropic_version` `llm.authentication` `llm.api_key_ref` `llm.image_understanding` `llm.timeout` | [配置手册 §5.3](configuration.md#53-llm)；Key 的写入见 [§6](configuration.md#6-配置-llm-api-key) |
| `decision.dm_timeout` `decision.llm_timeout` `decision.max_concurrent` | [配置手册 业务范围与决策](configuration.md#业务范围与决策botsystem_prompt--decision)、[决策服务契约](decision-service.md) |
| `decision.dm.enabled` `decision.dm.protocol` `decision.dm.endpoint` `decision.dm.model` `decision.dm.api_key_ref` `decision.dm.min_confidence` | [配置手册 业务范围与决策](configuration.md#业务范围与决策botsystem_prompt--decision)（缺省关闭，必须写 `enabled: true`）、[决策服务契约](decision-service.md) |

### `platforms`、`operator`

| 字段 | 说明 |
| --- | --- |
| `platforms.connectors[].id` `type` `enabled` `prompt_suffix` | [配置手册 §5.6](configuration.md#56-platforms) |
| `platforms.connectors[].options`（OneBot：`api_url` `send_token` `receive_secret` `webhook_addr` `webhook_path` `request_limit` `timeout` `replay_ttl` `replay_max`） | [配置手册 §5.6](configuration.md#56-platforms)，旧写法迁移见该节“OneBot 配置迁移” |
| `platforms.connectors[].options`（QQ 官方：`app_id` `secret_ref` `webhook_addr` `webhook_path` `timeout` `request_limit` `replay_ttl` `replay_max`） | [QQ 官方机器人接入](qqbot.md)，`target` 使用 group_openid；[完整示例](../configs/runtime.example.json) |
| `platforms.groups[].id` `connector_id` `target` `prompt_suffix` | [配置手册 §5.6](configuration.md#56-platforms)、[群管理：先把群绑定到 Bot](group-management.md#先把群绑定到-bot) |
| `operator.conversation_id` `operator.notifications` `operator.commands` | [配置手册 §5.7](configuration.md#57-operator) |

### `modules`

模块总开关、`enabled` 与 `options` 的通用规则见 [配置手册 §6.6 原生功能模块与 Hook](configuration.md#66-原生功能模块与-hook)。

| 模块与字段 | 说明 |
| --- | --- |
| `native.echo` | [配置手册 §6.6](configuration.md#66-原生功能模块与-hook) |
| `native.onebot`：`api_url` `send_token` `receive_secret` `webhook_addr` `webhook_path` `request_limit` `timeout` `replay_ttl` `replay_max` | [配置手册 §5.6](configuration.md#56-platforms)（连接器协议由该模块提供） |
| `native.qqbot`：`enabled`、空 `options` | [QQ 官方机器人接入](qqbot.md)，默认关闭，账号参数放在 connector 实例 |
| `native.reply_experience`：`empty_mention_prompt` `image_only_prompt` `search_progress_text` | [配置手册 §6.6](configuration.md#66-原生功能模块与-hook) |
| `native.proactive_reply`：`scope` `yield_duration` `silence` `max_window` `max_buffered` `max_text_len` `max_concurrent` `context_messages` `groups` | [配置手册 §6.6](configuration.md#66-原生功能模块与-hook)；`scope` 与决策的关系见 [业务范围与决策](configuration.md#业务范围与决策botsystem_prompt--decision)；让位规则见 [默认行为表](default-behavior.md) |
| `native.structured_search`：`document_root` `extensions` `default_results` `max_results` `min_match_ratio` `query_timeout` `build_timeout` `max_queries_per_turn` `max_context_chars` `max_section_chars` `max_file_bytes` `max_total_bytes` `max_files` | [配置手册 §5.5](configuration.md#55-modulesnativestructured_search) |
| `native.knowledge_reflection`：`conversation_ids` `scan_interval` `message_ttl` `quiet_window` `max_session_duration` `max_messages_per_conversation` `max_messages_total` `max_message_chars` `max_pairs_per_scan` `min_confidence` `extra_directory` | [配置手册 §5.8](configuration.md#58-modulesnativeknowledge_reflection)（v0.2.0 字段保留，周期/TTL/容量仅作用于已形成会话及模块处理标记） |
| `native.join_review`：`regex` `model_enabled` `rule` `combine` `reject_reason` `groups` | [群管理：入群自动审批](group-management.md#入群自动审批)、[配置手册：入群自动审批](configuration.md#入群自动审批) |
| `native.join_review.external_review`：`url` `method` `headers` `body` `result_expression` | [外部 HTTP 审批](group-management.md#外部-http-审批)（位于模块 options，凭据用 Header 占位符引用）；[三项组合说明](group-management.md#外部-http-审批)，[init](cli.md) 生成文件也列全这些字段（审批关闭） |
| `native.welcome`：`groups.<群ID>.enabled` `message` | [群管理：新人欢迎](group-management.md#新人欢迎) |

### `extensions`

| 字段 | 说明 |
| --- | --- |
| `extensions.mcp[].id` `transport` `endpoint` `enabled` `api_key_ref` `allowed_tools` `timeout` `max_response_bytes` | [配置手册 §6.5 外部扩展](configuration.md#65-外部扩展mcp-与-skill)、[MCP 安全与排错](configuration.md#652-mcp-安全与排错) |
| `extensions.skills.directories` `max_file_bytes` `max_total_bytes` `max_files` | [配置手册 §6.5 外部扩展](configuration.md#65-外部扩展mcp-与-skill) |

### 各项上限

| 字段 | 说明 |
| --- | --- |
| `conversation_memory.disabled` `ttl` `max_pairs` `max_sessions` `max_pending` `max_session_bytes` `max_total_bytes` | [配置手册：业务范围与决策](configuration.md#业务范围与决策botsystem_prompt--decision) 一节中的短期问答说明（缺省启用） |
| `message_history.max_messages` `ttl` `max_text_chars` | 同一节中的核心近期消息容器说明（始终启用，没有开关） |
| `agent.timeout` `max_turns` `max_tool_calls` `max_tools_per_turn` `max_result_bytes` `max_context_bytes` | [配置手册 §5.3](configuration.md#53-llm) |
| `admission.dedup_ttl` `dedup_max` | [配置手册 §5.4](configuration.md#54-admission) |

## 按主题查

| 想了解 | 看这里 |
| --- | --- |
| 配置的分层和哪些改动需要重启 | [配置手册 §1](configuration.md#1-配置分层) |
| 默认行为、哪些功能缺省开启 | [默认行为表](default-behavior.md) |
| 群事件、入群审批、欢迎语、管理 API | [群事件与管理操作](group-management.md) |
| `init`、`doctor`、能力目录 | [初始化、诊断与能力目录](cli.md) |
| 错误码与“是否已执行”的含义 | [Core 操作结果](operation-results.md) |
| 代码结构与模块机制 | [Architecture](architecture.md) |

