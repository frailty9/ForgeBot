# QQ 官方机器人接入

v0.2.0 增加独立的 `qqbot` ConnectorProvider，模块为 `native.qqbot`，默认关闭。它接 QQ 官方群聊 Webhook 和 REST API，与个人 QQ 的 OneBot 连接器独立，可同时绑定不同群。实现已有受控收发测试，真实账号收消息 → 回答验收仍待完成。

连接器统一使用名称 `qqbot`：模块 ID 为 `native.qqbot`，connector 的 `type` 为 `qqbot`。已有配置需同步改名，不提供旧名称别名。示例凭据引用为 `qqbot/main`；引用名可以自行选择，但 `secret_ref` 必须与实际写入的 SecretStore 键一致。

## 准备账号与配置

在 QQ 机器人开放平台取得 AppID/AppSecret，为部署账号开放群聊消息权限并设置测试/可用群。当前仅实现群聊，不接频道或 C2C。账号认证、审核状态和是否开启全量群消息由平台控制；只收到 @ 事件时，history、主动插话和沉淀只能看到实际收到的消息。

从 [官方 QQ 完整示例](../configs/runtime.qqbot.example.json) 复制运行时配置，替换模型地址/名称、`app_id` 和 `target`。`target` 是官方事件里的 `group_openid`，不是普通 QQ 群号；成员身份使用 `member_openid`。启动 YAML 的 `runtime_config_path` 指向这份部署配置，回答模型和 AppSecret 都先写入 SecretStore。

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
| `replay_ttl`、`replay_max` | 已接收消息去重时长/容量，默认 5m、10000；容量范围 1..100000 |

`init` 生成文件和 `runtime.example.json` 均列出默认关闭的 `native.qqbot` 模块与完整 QQ connector options；填好参数并绑定 `group_openid` 后，再同时启用模块和连接器。仅打开模块不会自动绑定群。

模块 options 只接受空对象，账号配置放在每个 connector 实例中；不提供第二套模块默认值。未知字段严格报错。API/Token 地址固定使用官方 HTTPS 地址，不对部署配置开放自定义地址。

## 配置公开回调

由反向代理将公开 HTTPS 路径转发到 `webhook_addr` 和 `webhook_path`。保持原始请求体、`X-Bot-Appid`、`X-Signature-Ed25519`、`X-Signature-Timestamp`，不要让代理改写 JSON 或重编码正文。平台当前接受的公开端口见官方 Webhook 文档，通常使用 HTTPS 443；本地监听地址可单独选择。

开放平台配置该 HTTPS 回调地址，并监听群 @ 消息；需要普通群消息时按账号权限启用全量消息。包括 op=13 在内的所有回调先按原始 `timestamp + body` 验签并核对 AppID，避免挑战接口成为任意签名入口；通过后 op=13 按 AppSecret 派生的 Ed25519 密钥签名 `event_ts + plain_token`。签名时间仅接受过去 5 分钟至未来 1 分钟，部署机器应保持时间同步。

验签通过后以消息 ID 在群内去重，进入固定 128 条队列并返回 `op=12,d=0`，不等待模型完成；队列满、尚未接收或去重容量满时返回 `d=1`，允许平台重投。队列在内存中，关闭/换代取消在途工作，不承诺已 ACK 消息跨重启恢复。后台最多同时处理 4 条消息；事件到达不等于模型回答或平台发送成功。

## 支持与限制

| 能力 | 当前行为 |
| --- | --- |
| 群 @ 消息 | `GROUP_AT_MESSAGE_CREATE` 明确触发问答，平台已去掉 @ 前缀 |
| 全量群消息 | `GROUP_MESSAGE_CREATE` 进入现有 history/主动插话策略，需账号已开启；`<@all>` 不设置 Mentioned |
| 群角色 | 使用官方 `member_role` 的 owner/admin；缺失或未知角色视为普通成员 |
| 入站媒体 | 图片/视频/语音/文件附件映射为 Core 媒体，图片是否交给模型仍由 `llm.image_understanding` 决定 |
| 引用 | 有界记录 `msg_idx/ref_msg_idx` 与真实消息 ID 的同群映射；未知或已经移出的映射不猜 ID，不额外调用远端取原文 |
| 被动文本回复 | 带触发消息 ID，5 分钟内最多 5 次；进度提示也占次数，`msg_seq` 递增，无自动重试 |
| 协议内部消息索引 | 官方发送响应的消息 ID 与 `ext_info.ref_idx` 只保存在适配器内部用于引用/撤回，不返回通用回执，也不补写 Core history 的 Bot 消息 ID |
| 撤回 | 实现宿主可调用能力的协议操作，按消息 ID 直接请求官方 API；不要求适配器缓存有该 ID，不在本地加两分钟闸门，真实账号权限/平台限制由返回结果确认 |
| 主动发送、欢迎语和运维通知 | 首期仅被动回复，无触发依据时返回 unsupported，不自动消耗主动消息额度 |
| 出站图片/富媒体、任意消息段 | 首期不实现上传/消息段；可用纯文本形式时只发送文本 |
| 入群申请审批/新人入群事件 | 未实现，能力目录为不支持，不从普通聊天伪造事件 |

引用映射和被动回复状态最多保留 5 分钟，并受 `replay_max` 容量限制。history 的 30 分钟默认窗口不扩大平台的回复或引用能力窗口。全量消息与 @ 消息重复推送同一消息时只投递一次。权限、消息有效期、正文限制等拒绝不会进入 Adapter.Errors，不停止其他连接器；请求发出后超时保留执行结果未知，管理员应在 QQ 核实。

群能力查询/目录展示 `text_messages`、`outbound_segments`、`passive_replies_only`、`message_recall`，另列审批/成员事件支持和 `permission=unknown`。这些是实现声明，不能替代账号权限和真实调用结果。

Token 由实例缓存，到期前 60 秒内的下次调用单次刷新，并发调用共享结果；没有常驻轮询刷新任务。停用取消消息任务和平台请求。替换使用中的 AppSecret 会构建候选连接器、停止旧连接器、尝试候选启动，成功后才激活凭据；失败恢复旧连接器和旧凭据。该操作沿用整组连接器重启流程，其他已配置实例也会短暂停止后重新启动。启用连接器仍引用该键时不允许直接删除；先停用或移除连接器再删凭据。

轮换入口是受认证的 `PUT /v1/admin/secrets/qqbot/main`，正文 `{"value":"NEW_APP_SECRET"}`；GET 只查状态、DELETE 删除引用，不返回明文。也可先初始化一个新引用，再通过版本化配置更新切换 `secret_ref`；CLI 首次写入命令不能覆盖旧引用。

## 验收步骤

1. doctor 的模块、connector options 和 connector credential 检查通过；确认公开 HTTPS 回调可达，完成 op=13 验证。
2. 测试群 @Bot 提问，确认入站验签通过、模型回答成功发送，并在真实 QQ 客户端看到回复。
3. 通过宿主 capability 传消息 ID 调用撤回，验证真实账号权限和平台返回的消息限制；不得用本地 history TTL/两分钟计时预先拒绝。当前没有聊天引用命令或默认 LLM 工具，真实验收调用入口由部署者选择。
4. 测试仅 @全体不触发明确问答；有全量权限时检查普通消息可见，角色归一正确。
5. 更换凭据并复验收发，尝试无效凭据确认旧连接器恢复；检查整组重启后其他 OneBot/QQ 实例恢复可用。

记录账号权限、回调方式、实际结果和日期即可，勿将真实 Token、签名原文、群聊正文或成员开放 ID 放进仓库。受控测试覆盖验签、排队、Token、发送、协议引用索引、撤回与 Core 问答链路，以上真实验收尚未完成。

官方协议依据：[Webhook](https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/event-emit/webhook.html)、[签名](https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/interface-framework/sign.html)、[群消息发送](https://bot.q.qq.com/wiki/develop/api-v2/autogen/api/v2_groups_group_openid_messages.post.html)、[群消息撤回](https://bot.q.qq.com/wiki/develop/api-v2/autogen/api/v2_groups_group_openid_messages_message_id.delete.html)。核对日期：2026-11-10，协议可用不等于账号已获授权。
