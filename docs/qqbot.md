# QQ 官方机器人接入

v0.2.0 增加独立的 `qqbot` ConnectorProvider，模块为 `native.qqbot`，默认关闭。它接 QQ 官方群聊 Webhook 和 REST API，与个人 QQ 的 OneBot 连接器独立，可同时绑定不同群。实现已有受控收发测试，真实账号收消息 → 回答验收仍待完成。

连接器统一使用名称 `qqbot`：模块 ID 为 `native.qqbot`，connector 的 `type` 为 `qqbot`。已有配置需同步改名，不提供旧名称别名。示例凭据引用为 `qqbot/main`；引用名可以自行选择，但 `secret_ref` 必须与实际写入的 SecretStore 键一致。

## 准备账号与配置

在 QQ 机器人开放平台取得 AppID/AppSecret，为部署账号开放群聊消息权限并设置测试/可用群。当前仅实现群聊，不接频道或 C2C。账号认证、审核状态和是否开启全量群消息由平台控制；只收到 @ 事件时，history、主动插话和沉淀只能看到实际收到的消息。

从唯一的 [runtime.example.json](../configs/runtime.example.json) 复制部署配置，填写模型地址/名称及 qqbot-main 的 `app_id`，同时启用 `platforms.connectors` 中的 qqbot-main 与 `modules.native.qqbot`。在 `platforms.groups` 添加 `{"id":"support","connector_id":"qqbot-main","target":"YOUR_GROUP_OPENID","prompt_suffix":""}`。Echo 的 local 可保留；若删除它，须同步调整 operator 和所有群级模块对 local 的引用。`target` 是官方事件里的 `group_openid`，不是普通 QQ 群号；成员身份使用 `member_openid`。启动 YAML 的 `runtime_config_path` 指向这份部署配置，回答模型和 AppSecret 都先写入 SecretStore。

```powershell
go run ./cmd/forgebot -config configs/forgebot.local.yaml -init-api-key qqbot/main 'YOUR_APP_SECRET'
go run ./cmd/forgebot doctor -config configs/forgebot.local.yaml
go run ./cmd/forgebot -config configs/forgebot.local.yaml
```

`secret_ref` 必须与写入的引用一致。命令使用方式及回答模型凭据初始化见 [CLI](cli.md)。真实 AppSecret 不写入 runtime JSON 或仓库；公开配置只返回 `secret_configured`，不返回引用值或 Token。doctor 只验证引用及凭据是否存在，不向 QQ 请求 Token，不验证真实平台权限。

| 实例 options | 含义 |
| --- | --- |
| `app_id`、`secret_ref` | 必填，账号 AppID 与加密仓储里的 AppSecret 引用 |
| `webhook_addr`、`webhook_path` | 必填，本地 HTTP 监听地址及以 `/` 开头的路径，建议 `127.0.0.1:18091`、`/qqbot` |
| `timeout` | 平台请求超时，默认 15s，必须为正 |
| `request_limit` | 入站请求字节上限，默认 1MiB，范围 1..8MiB |
| `replay_ttl`、`replay_max` | 已接收消息去重时长/容量，默认 5m、10000；容量范围 1..100000。满额时淘汰最早到期的标记，不拒收新消息 |
| `sandbox` | 默认 `false`；`true` 时 OpenAPI 改用开放平台沙箱地址，供尚未上线的机器人在沙箱群测试。Token 地址不变 |

`init` 直接复用唯一的 `runtime.example.json` 生成部署文件，列全字段并保持 `native.qqbot` 模块和 QQ connector 默认关闭；填好参数并绑定 `group_openid` 后，再同时启用模块和连接器。仅打开模块不会自动绑定群。

模块 options 只接受空对象，账号配置放在每个 connector 实例中；不提供第二套模块默认值。未知字段严格报错。Token 固定从 `https://bots.qq.com/app/getAppAccessToken` 获取，OpenAPI 使用 `https://api.sgroup.qq.com`（`sandbox: true` 时为 `https://sandbox.api.sgroup.qq.com`），不对部署配置开放任意地址。Token 响应缺少 `expires_in` 时按官方默认 7200 秒处理。

## 配置公开回调

由反向代理将公开 HTTPS 路径转发到 `webhook_addr` 和 `webhook_path`。保持原始请求体、`X-Bot-Appid`、`X-Signature-Ed25519`、`X-Signature-Timestamp`，不要让代理改写 JSON 或重编码正文。平台当前接受的公开端口见官方 Webhook 文档，通常使用 HTTPS 443；本地监听地址可单独选择。

开放平台配置该 HTTPS 回调地址，并监听群 @ 消息；需要普通群消息时按账号权限启用全量消息。所有请求先核对请求体大小和 JSON 格式；带 `X-Bot-Appid` 头时必须与 `app_id` 一致，不带该头的请求（部分代理会去掉）照常处理，来源由签名证明。`op=13` 是回调地址验证，允许不带签名头，按 AppSecret 派生的 Ed25519 密钥签名 `event_ts + plain_token` 后返回，不分发消息、不改变去重或引用缓存。其他回调仍必须按原始 `timestamp + body` 验签。

挑战的 `event_ts` 和业务签名时间均只接受过去 5 分钟至未来 1 分钟，部署机器应保持时间同步。挑战令牌限定为 1..4096 字节的 ASCII 字母、数字及 `-_+/=`，不接受 JSON、空白和控制字符，防止验证接口被用于签署业务事件。这是本地约束，实际开放平台回调仍需验收；不为兼容挑战而放宽业务验签。

验签通过后以消息 ID 在群内去重，进入固定 128 条队列并返回 `op=12,d=0`，不等待模型完成；队列满、尚未接收或去重容量满时返回 `d=1`，允许平台重投。队列在内存中，关闭/换代取消在途工作，不承诺已 ACK 消息跨重启恢复。后台最多同时处理 4 条消息；事件到达不等于模型回答或平台发送成功。

## 支持与限制

| 能力 | 当前行为 |
| --- | --- |
| 群 @ 消息 | `GROUP_AT_MESSAGE_CREATE` 明确触发问答，平台已去掉 @ 前缀 |
| 全量群消息 | `GROUP_MESSAGE_CREATE` 按 `mentions[].is_you=true` 识别明确 @Bot，其余进入现有 history/主动插话策略；需账号已开启，@全体或其他成员不因此触发明确问答 |
| 群角色 | 使用官方 `member_role` 的 owner/admin；缺失或未知角色视为普通成员 |
| 入站媒体 | 图片/视频/语音/文件附件映射为 Core 媒体；兼容无协议头地址及缺失 MIME 的图片，是否交给模型仍由 `llm.image_understanding` 决定 |
| 引用 | 有界记录 `msg_idx/ref_msg_idx` 与真实消息 ID 的同群映射；未知或已经移出的映射不猜 ID，不额外调用远端取原文 |
| 被动文本回复 | 带触发消息 ID，5 分钟内最多 5 次；进度提示也占次数，`msg_seq` 递增。已知引用索引时附带 `message_reference`；平台因引用失效以 4xx 拒绝时，去掉引用用同一 `msg_seq` 再发一次，其余失败不重试 |
| 消息时间 | 使用事件 `timestamp`；格式异常时取接收时间，不因此拒收或要求平台重投 |
| 协议内部消息索引 | 官方发送响应的消息 ID 与 `ext_info.ref_idx` 只保存在适配器内部用于引用/撤回，不返回通用回执，也不补写 Core history 的 Bot 消息 ID |
| 撤回 | 实现宿主可调用能力的协议操作，按消息 ID 直接请求官方 API；不要求适配器缓存有该 ID，不在本地加两分钟闸门，真实账号权限/平台限制由返回结果确认 |
| 主动发送、欢迎语和运维通知 | 首期仅被动回复，无触发依据时返回 unsupported，不自动消耗主动消息额度 |
| 出站图片/富媒体、任意消息段 | 首期不实现上传/消息段；可用纯文本形式时只发送文本 |
| 入群申请审批/新人入群事件 | 未实现，能力目录为不支持，不从普通聊天伪造事件 |

附件地址保留合法 HTTP/HTTPS；`//域名/路径` 和裸域名地址补为 HTTPS，保留原路径与查询参数，空地址、缺主机、非法格式、带用户信息或其他协议的地址会跳过。附件不在 Webhook 中下载。媒体类型优先使用非空 `content_type`；仅缺失时按文件名后缀 `.jpg/.jpeg/.png/.gif/.webp/.bmp`（大小写不敏感）识别图片并补 MIME，不根据 URL 后缀覆盖明确的非图片类型。每条消息仍最多保留 16 个有效附件。

引用映射和被动回复状态最多保留 5 分钟，并受 `replay_max` 容量限制。history 的 30 分钟默认窗口不扩大平台的回复或引用能力窗口。全量消息先根据平台 `mentions[].is_you` 归一明确 @，再按同群消息 ID 去重；它与专用 @ 事件先后或并发到达时只投递一次。仅删除已确认属于本机器人的 `id/member_openid` 对应 `<@ID>` 或 `<@!ID>` 标签，保留其他成员和 @全体标签，再识别命令。全量事件未提供 `is_you` 时，不根据正文标签、AppID、昵称或其他 Bot 身份猜测明确 @；此时若它先到，同 ID 的后续事件仍按现有规则去重。权限、消息有效期、正文限制等拒绝不会进入 Adapter.Errors，不停止其他连接器；请求发出后超时保留执行结果未知，管理员应在 QQ 核实。

群能力查询/目录展示 `text_messages`、`outbound_segments`、`passive_replies_only`、`message_recall`，另列审批/成员事件支持和 `permission=unknown`。这些是实现声明，不能替代账号权限和真实调用结果。

Token 由实例缓存，到期前 60 秒内的下次调用单次刷新，并发调用共享结果；没有常驻轮询刷新任务。停用取消消息任务和平台请求。替换使用中的 AppSecret 会构建候选连接器、停止旧连接器、尝试候选启动，成功后才激活凭据；失败恢复旧连接器和旧凭据。该操作沿用整组连接器重启流程，其他已配置实例也会短暂停止后重新启动。启用连接器仍引用该键时不允许直接删除；先停用或移除连接器再删凭据。

轮换入口是受认证的 `PUT /v1/admin/secrets/qqbot/main`，正文 `{"value":"NEW_APP_SECRET"}`；GET 只查状态、DELETE 删除引用，不返回明文。也可先初始化一个新引用，再通过版本化配置更新切换 `secret_ref`；CLI 首次写入命令不能覆盖旧引用。

## 验收步骤

1. doctor 的模块、connector options 和 connector credential 检查通过；确认公开 HTTPS 回调可达，完成 op=13 验证。
2. 测试群 @Bot 提问，确认入站验签通过、模型回答成功发送，并在真实 QQ 客户端看到回复。
3. 通过宿主 capability 传消息 ID 调用撤回，验证真实账号权限和平台返回的消息限制；不得用本地 history TTL/两分钟计时预先拒绝。当前没有聊天引用命令或默认 LLM 工具，真实验收调用入口由部署者选择。
4. 测试仅 @全体或 @其他成员不触发明确问答；有全量权限时检查普通消息可见、角色归一，以及明确 @Bot 的全量事件与专用 @ 事件先后到达都只回答一次。
5. @Bot 发送图片，验证无协议头 URL 与缺失 MIME 的附件可识别；分别启停 `llm.image_understanding`，确认进入模型或返回原有不支持提示。
6. 更换凭据并复验收发，尝试无效凭据确认旧连接器恢复；检查整组重启后其他 OneBot/QQ 实例恢复可用。

记录账号权限、回调方式、实际结果和日期即可，勿将真实 Token、签名原文、群聊正文或成员开放 ID 放进仓库。受控测试覆盖验签、排队、Token、发送、协议引用索引、撤回与 Core 问答链路，以上真实验收尚未完成。

官方协议依据：[Webhook](https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/event-emit/webhook.html)、[签名](https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/interface-framework/sign.html)、[群消息发送](https://bot.q.qq.com/wiki/develop/api-v2/autogen/api/v2_groups_group_openid_messages.post.html)、[群消息撤回](https://bot.q.qq.com/wiki/develop/api-v2/autogen/api/v2_groups_group_openid_messages_message_id.delete.html)。核对日期：2026-11-10，协议可用不等于账号已获授权。
