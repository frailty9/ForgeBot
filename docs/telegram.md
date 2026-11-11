# Telegram 群聊接入

当前支持 `telegram` 类型和默认关闭的 `native.telegram` 模块。本批已实现长轮询、群文本问答、同群引用、管理员识别及宿主按 ID 撤回；Webhook、图片输入和新人欢迎仍待后续批次，配置中不能提前写 `mode: "webhook"`。

## 配置与首次启动

1. 在 BotFather 创建机器人，取得 Bot Token，将机器人加入目标普通群或非论坛超级群。
2. 从 Telegram 官方 API 的 `message.chat.id` 取得群 ID；绑定使用完整十进制字符串，例如 `"-1001234567890"`，不使用群名或 `@username`。私聊、频道和论坛群不在本期范围内。
3. 从唯一的 [`runtime.example.json`](../configs/runtime.example.json) 复制部署配置，启用 `platforms.connectors` 中的 `telegram-main` 和 `modules.native.telegram`，给 `platforms.groups` 添加 `{"id":"support","connector_id":"telegram-main","target":"你的十进制群ID","prompt_suffix":""}`。Echo 的 `local` 可保留；若删除它，须同步调整 operator 及各模块中引用 local 的配置，关闭的群级功能也会校验绑定。填写回答模型名称及地址，让启动 YAML 的 `runtime_config_path` 指向部署文件，或使用 `-runtime-config` 显式选择。
4. 使用同一启动 YAML/主密钥写入回答模型凭据和 `telegram/main`，再启动服务。

```powershell
$env:FORGEBOT_LLM_API_KEY = 'replace-with-telegram-bot-token'
go run ./cmd/forgebot -config configs/forgebot.local.yaml -init-api-key telegram/main
Remove-Item Env:FORGEBOT_LLM_API_KEY
```

CLI 环境变量名沿用凭据初始化入口，写入目标由 `telegram/main` 参数决定，不覆盖回答模型引用。初始化仓储及模型凭据见 [CLI](cli.md)；运行中的 Token 通过[按引用凭据 API](configuration.md#63-按引用管理凭据)替换，复用连接器候选构建与失败恢复。Bot Token 不写进 runtime JSON。

`init` 直接使用同一份完整示例生成 runtime.local.json，保留全部字段和默认关闭的 Telegram 模块/连接器。启用时同时打开模块和 connector，给它绑定至少一个逻辑群；只打开模块不会自动发现或绑定群。

| 实例 options | 含义 |
| --- | --- |
| `token_ref` | 必填，SecretStore 中 Bot Token 引用，例如 `telegram/main` |
| `mode` | 必填；当前仅接受 `long_polling` |
| `timeout` | 普通 API 超时，默认 `15s`，必须为正；30 秒长轮询额外加此预算 |

模块 options 只接受 `{}`，实例字段严格解析。API 固定使用 Telegram 官方 HTTPS 地址，不增加 `api_base` 配置。公开配置只显示 `token_configured`，含义是已填写引用；是否存在实际凭据由 doctor 单独检查。doctor 不调用 getMe、不接管更新通道，也不验证真实群权限。

## 更新接收与停止

同一个 Bot Token 仅由一个 ForgeBot 部署使用。长轮询启动会调用 `deleteWebhook(drop_pending_updates=false)`，自动清除 Telegram 的回调登记；这可能影响先前使用同一账号的其他服务。它不主动丢弃 Telegram 尚未确认的更新。

Start 验证 getMe 和每个绑定的 getChat，确认都属于普通群或非论坛超级群；首次短 getUpdates 的结果同样进入接收流程，不跳过积压。后续以 30 秒长轮询获取消息，按逐项接纳或明确忽略的进度推进 offset，只有下次请求的 offset 才在平台确认之前的更新。

接收队列固定 128 条，4 个后台工作者；按 update_id 去重 5 分钟、最多 10000 个标记。队列/标记满时等待，不提前确认未接纳的消息，等待可被停止取消。请求失败不推进 offset；读取端暂时网络故障或限流会退避，平台的 retry_after 优先；401/403/409 结束接收并报告生命周期故障。发送与删除操作不会自动重试。

停止取消轮询、排队及在途处理。同一连接器对象恢复时保留已接纳 offset 和短期标记；换新对象或进程不恢复它们。队列在内存中，已接纳但尚未完成的消息可能随停止丢失；不承诺跨重启 exactly-once 或完整历史补处理。

## 消息与权限

- 群内明确 @本 Bot、回复本 Bot 或面向本 Bot 的命令可以触发既有处理；命令 `/status@OtherBot` 会在适配器入口忽略，`/status@本Bot用户名` 先去掉自身后缀。运维命令仍仅按配置中的运维群与白名单生效。
- Telegram 实体偏移按 UTF-16 解析，支持 text/caption 的 mention、text_mention 和 bot_command；清理自身 @ 后保留其他正文。普通消息不会自动成为明确问答，可供已开启的主动插话与 history 使用。
- 同群 `reply_to_message` 的正文独立放入引用上下文，不混入当前正文；不读取 `external_reply` 或按任意 ID 远端找原文。图片、其他附件及包含引用媒体的消息当前返回不支持，不丢图后仅用文字回答。
- 过滤机器人消息和 `sender_chat` 匿名/频道身份；群角色通过 getChatAdministrators 获取，群主映射 owner、管理员映射 admin。成功结果缓存 5 分钟，刷新失败保守视为成员并缓存 30 秒；my_chat_member 会使对应缓存失效。
- BotFather 隐私模式和机器人在群内的权限决定普通消息可见范围。只收到命令/回复/@ 时，history、主动插话和沉淀只能使用实际收到的消息；加长 history 不能补齐平台没有推送的内容。

## 投递与能力边界

纯文本通过 sendMessage 投递，不启用 Markdown/HTML；Segments 使用已有 Text 形式。文本最多 4096 个 Unicode 字符，空白、非法 UTF-8、超长在发请求前返回 invalid，不截断或分条。指定回复 ID 时禁止“原消息不存在也继续发送”的隐式降级；失败交统一结果语义处理，不去引用后重发。

允许无回复 ID 的文本投递，所以已有主动插话和运维通知可以使用此连接器，实际权限以平台返回为准。撤回由宿主 `group.message_recall.v1` 调 deleteMessage，不要求消息在本地 history 中；不增加两分钟或其他本地年龄闸门。平台拒绝或请求结果未知均不触发发送重试，不停止无关连接器。平台返回群迁移 ID 时报告绑定冲突，部署者更新 target，不自动迁移配置或重发。

目录中的 `text_messages=true`、`message_recall=true`、`passive_replies_only=false` 仅说明本地实现；账号权限仍为 unknown。`outbound_segments`、`join_events`、`member_events`、`join_review` 当前为 false。首期不做 Telegram 自动入群审批；欢迎和图片会在后续批次完成后单独声明可用。

## 验证状态

受控测试覆盖轮询确认、停止恢复、UTF-16 实体/命令目标、管理员故障降级、文本限制、撤回、异常 API 响应及凭据脱敏；普通群真实收消息→模型回答→Telegram 收到回复仍待验收。启动生产账号前先确认绑定、独占接收通道和群权限；真实结果不由本地 doctor ready 代替。

官方依据：[Bot API](https://core.telegram.org/bots/api)、[Privacy Mode](https://core.telegram.org/bots/features#privacy-mode)。协议核对日期：2026-11-11。
