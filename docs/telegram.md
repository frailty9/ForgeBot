# Telegram 群聊接入

当前支持 `telegram` 类型和默认关闭的 `native.telegram` 模块。已实现长轮询与 Webhook 两种接收方式、群文本问答、图片输入、同群引用、管理员识别、新成员欢迎及宿主按 ID 撤回。以上均为受控测试结果，真实群聊验收见文末。

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
| `mode` | 必填，`long_polling` 或 `webhook`；只运行所选的一种接收方式 |
| `timeout` | 普通 API 超时，默认 `15s`，必须为正；30 秒长轮询额外加此预算 |
| `webhook_url` | webhook 必填，公网 HTTPS 地址，端口只能是 443/80/88/8443，不含用户信息或片段 |
| `webhook_addr`、`webhook_path` | webhook 必填，本地监听 `host:port` 与以 `/` 开头的路径；可经反向代理与公网路径不同 |
| `webhook_secret_ref` | webhook 必填，SecretStore 中回调认证文本的引用，不能与 `token_ref` 相同；实际值须为 1..256 个字母、数字、`_` 或 `-` |

长轮询模式下 webhook 字段可以保留（完整示例即如此），但填写了就必须结构合法；此时只要求 `token_ref` 对应的凭据存在，不读取 `webhook_secret_ref`。webhook 模式同时声明两项凭据，doctor 分别检查。公开配置显示 webhook 地址/路径，认证文本只显示 `webhook_secret_configured`。

模块 options 只接受 `{}`，实例字段严格解析。API 固定使用 Telegram 官方 HTTPS 地址，不增加 `api_base` 配置。公开配置只显示 `token_configured`，含义是已填写引用；是否存在实际凭据由 doctor 单独检查。doctor 不调用 getMe、不接管更新通道，也不验证真实群权限。

## 更新接收与停止

同一个 Bot Token 仅由一个 ForgeBot 部署使用。长轮询启动会调用 `deleteWebhook(drop_pending_updates=false)`，自动清除 Telegram 的回调登记；这可能影响先前使用同一账号的其他服务。它不主动丢弃 Telegram 尚未确认的更新。

Start 验证 getMe 和每个绑定的 getChat，确认都属于普通群或非论坛超级群；首次短 getUpdates 的结果同样进入接收流程，不跳过积压。后续以 30 秒长轮询获取消息，按逐项接纳或明确忽略的进度推进 offset，只有下次请求的 offset 才在平台确认之前的更新。

接收队列固定 128 条，4 个后台工作者；按 update_id 去重 5 分钟、最多 10000 个标记。队列/标记满时等待，不提前确认未接纳的消息，等待可被停止取消。请求失败不推进 offset；读取端暂时网络故障或限流会退避，平台的 retry_after 优先。401/403 结束读取并报告生命周期故障，发送与撤回仍可用；409（另一个消费者正在用同一 Token 取更新，常见于滚动部署新旧实例重叠）上报后继续退避重试，不永久停收。发送与删除操作不会自动重试。

### Webhook 模式

Start 先校验 getMe 与绑定群，再占用 `webhook_addr` 本地端口，端口被占用时不调用 setWebhook。随后调用 `setWebhook`，传 `webhook_url`、`secret_token`、与长轮询相同的 `allowed_updates`、`max_connections=1` 和 `drop_pending_updates=false`。登记被平台拒绝时释放端口并启动失败；请求结果未知时再尽力撤销登记。由反向代理把公网 HTTPS 转发到本地地址与路径，保留请求头 `X-Telegram-Bot-Api-Secret-Token`。

每个回调先校验该请求头（恒定时间比较），不符返回 401；请求体上限 1MiB，非法 JSON 返回 400。更新入队或被明确忽略（重复、非消息类型）后才返回 200；队列/去重容量满或连接器已停止时返回 503，由 Telegram 重投，不先确认后丢失。不等模型完成才回应。Telegram 没有 QQ 式的地址挑战。

Stop 先停止本地监听与在途处理，再用独立的有界请求 `deleteWebhook(drop_pending_updates=false)` 撤销登记。远端撤销失败只记诊断，不当作本地停止失败。每次 Start 都按当前模式重新登记：webhook 换成长轮询时自动删除回调登记，长轮询换成 webhook 时自动设置，切换不丢弃平台待处理更新。模式、地址和认证文本的变更都走连接器换代与失败恢复，恢复旧连接器时按旧模式重新登记。

Webhook 模式与长轮询一样只在内存排队：ACK 只表示已进入队列，停机可能丢失已 ACK 但未处理的更新。同一 Token 只能由一个接收者持有，webhook 登记后其他服务的 getUpdates 会收到 409。

停止取消轮询、排队及在途处理。同一连接器对象恢复时保留已接纳 offset 和短期标记；换新对象或进程不恢复它们。队列在内存中，已接纳但尚未完成的消息可能随停止丢失；不承诺跨重启 exactly-once 或完整历史补处理。

## 消息与权限

- 群内明确 @本 Bot、回复本 Bot 或面向本 Bot 的命令可以触发既有处理；命令 `/status@OtherBot` 会在适配器入口忽略，`/status@本Bot用户名` 先去掉自身后缀。运维命令仍仅按配置中的运维群与白名单生效。
- Telegram 实体偏移按 UTF-16 解析，支持 text/caption 的 mention、text_mention 和 bot_command；清理自身 @ 后保留其他正文。普通消息不会自动成为明确问答，可供已开启的主动插话与 history 使用。
- 同群 `reply_to_message` 的正文独立放入引用上下文，不混入当前正文；不读取 `external_reply` 或按任意 ID 远端找原文。同群引用里的图片同样下载并作为引用媒体交给模型。
- 过滤机器人消息和 `sender_chat` 身份：匿名管理员（以群身份发言）和关联频道自动转发的帖子首期不处理。

## 图片输入

照片（`photo`）和 MIME 为图片、或文件名后缀为 `.jpg/.jpeg/.png/.gif/.webp/.bmp` 的文件（`document`）会被下载后交给模型。照片的多个尺寸是同一张图，按像素面积选最大且已知大小不超过 5MiB 的版本，不假定数组有序；所有尺寸都已知超限时整条消息停止。一条消息最多一张照片和一份图片文件，连同同群引用最多 4 张，每张 5MiB。

下载在后台工作者里进行（`getFile` → 官方文件地址），不阻塞接收；已知超限不下载，实际读取按上限 +1 校验。下载后按文件头识别真实类型（JPEG/PNG/GIF/WebP/BMP），不信声明或一律当 JPEG；无法确认类型、下载失败或超限时整条消息停止，不会丢掉图片后只按文字回答。下载地址含 Bot Token，只把内容放进消息，不写入媒体 URL、history、日志或公开状态。

是否把图片交给模型仍由 `llm.image_understanding` 决定；关闭时也会先完成下载，再由宿主回复原有的“当前暂不支持图片”。视频、音频、语音、动图、贴纸和非图片文件只记录类型与名称，不下载。不聚合相册，相册中每张图作为独立消息到达。

## 新成员欢迎

入群服务消息的 `new_chat_members` 中每位真人生成一个成员入群事件，复用 `native.welcome` 的群开关、暂停与模板。身份取成员本身而不是 `from`（可能是邀请人），时间取服务消息的 `date`，缺失时不欢迎、不伪造当前时间；机器人和本 Bot 自己入群不欢迎；服务消息不作为空文本问答。平台重投按 `update_id` 去重；事件 ID 含 `update_id`，同一人短时间内退群再入群仍会被欢迎。不订阅 `chat_member`，平台未推送的成员变化不补齐。
- 过滤机器人消息和 `sender_chat` 匿名/频道身份；群角色通过 getChatAdministrators 获取，群主映射 owner、管理员映射 admin。成功结果缓存 5 分钟，刷新失败保守视为成员并缓存 30 秒；my_chat_member 会使对应缓存失效。
- BotFather 隐私模式和机器人在群内的权限决定普通消息可见范围。只收到命令/回复/@ 时，history、主动插话和沉淀只能使用实际收到的消息；加长 history 不能补齐平台没有推送的内容。

## 投递与能力边界

纯文本通过 sendMessage 投递，不启用 Markdown/HTML；Segments 使用已有 Text 形式。文本最多 4096 个 Unicode 字符，空白、非法 UTF-8、超长在发请求前返回 invalid，不截断或分条。指定回复 ID 时禁止“原消息不存在也继续发送”的隐式降级；失败交统一结果语义处理，不去引用后重发。

允许无回复 ID 的文本投递，所以已有主动插话和运维通知可以使用此连接器，实际权限以平台返回为准。撤回由宿主 `group.message_recall.v1` 调 deleteMessage，不要求消息在本地 history 中；不增加两分钟或其他本地年龄闸门。平台拒绝或请求结果未知均不触发发送重试，不停止无关连接器。平台返回群迁移 ID 时报告绑定冲突，部署者更新 target，不自动迁移配置或重发。

目录中的 `text_messages=true`、`message_recall=true`、`passive_replies_only=false` 仅说明本地实现；账号权限仍为 unknown。`member_events=true`；`outbound_segments`、`join_events`、`join_review` 为 false。首期不做 Telegram 自动入群审批，也不发送图片等出站媒体。

部署在无法直连 `api.telegram.org` 的网络时，可通过进程环境变量 `HTTPS_PROXY` 走代理（Go 标准 HTTP 客户端读取）；不提供自定义 API 地址配置。

## 验证状态

受控测试覆盖轮询确认、停止恢复、UTF-16 实体/命令目标、管理员故障降级、文本限制、撤回、异常 API 响应及凭据脱敏；普通群真实收消息→模型回答→Telegram 收到回复仍待验收。启动生产账号前先确认绑定、独占接收通道和群权限；真实结果不由本地 doctor ready 代替。

官方依据：[Bot API](https://core.telegram.org/bots/api)、[Privacy Mode](https://core.telegram.org/bots/features#privacy-mode)。协议核对日期：2026-11-11。
