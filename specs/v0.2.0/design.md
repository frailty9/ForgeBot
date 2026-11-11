# v0.2.0 实施设计

日期：2026-11-10。依据：[requirements.md](requirements.md)。核对基线：`feat/v0.2.0@1103bc7`，本地 `master@fc5a7ec`。

状态：用户已于 2026-11-10 授权先提交规划文档，再开始实施。沉淀职责按用户补充的原始讨论修正；配置迁移、表达式子集和 QQ 首期接入按本设计执行，进度与验证见 tasks。

2026-11-11 范围追加：Telegram 已由用户确认并入 v0.2.0，需求见 requirements §1.5；以下 §8 为本次设计方案，尚未实施。原表和基线描述保留用于追溯，不表示当前代码仍停在原基线。

## 1. 当前代码与实施边界

| 目标 | 当前代码事实 | 本版改动 |
| --- | --- | --- |
| BOT-14 沉淀 | `knowledgereflection` 用 Observer、独立 buffer 和周期 scanner；宿主已有 history.Reader 和 History 订阅 | 只替换候选来源、归组与调度，不改变模型提炼、置信度、去重、发布和索引重载行为 |
| ARCH-P06 旁路 | `Builder.ConnectorFactory/ConnectorProviders` 仍存在；生产组合根只注册模块，测试仍走旁路 | 删除宿主旁路，测试迁到模块 Provider 注册 |
| BOT-19 撤回 | Sender 只返回 error；OneBot 丢弃发送响应里的消息 ID；无撤回可选接口 | 保持 error-only 发送接口；宿主共享 capability 按群绑定后传消息 ID 撤回，业务触发后续确定 |
| 外部入群审批 | 模块只提供 Policy，Core 的 group.Service 判断并执行 | 继续由 Core 发外部请求，接入三种判断及凭据解析 |
| QQ 官方连接器 | 生产只注册 Echo/OneBot Provider | 独立 `native.qqbot` 模块及 `qqbot` 适配器 |
| Telegram 连接器 | 截至 `6a386b5` 无 Telegram Provider，现有凭据和群操作契约可复用 | 新增 `native.telegram`；普通群/非论坛超级群，长轮询或 Webhook；不做自动入群审批 |

代码核对纠正：pipeline 与 reflector 都依赖 `buffer.AnswerSession`；reflection.Status 还依赖 buffer/scanner 的 Stats。删除包前必须迁移这些类型。`quiet_window/max_session_duration` 当前属于归组和调度，不属于 pipeline。

实施顺序：基线 → 沉淀重构 → Provider 旁路清理 → 消息撤回能力 → 外部审批 → QQ 官方连接器 → Telegram 连接器 → 验收。提交、推送和发行仍需用户明确要求。

## 2. BOT-14：核心消息容器驱动沉淀

### 2.1 数据与归组

- 模块显式依赖 `history.ReaderKey`，只用 `History.Appended` 取代自身的 Observer，不注册或依赖 Evicted。保留通用 Observer 和消息移出事件机制，其他消费者不随本次重构删除。
- 普通成员消息仅进入 Core history，不再复制到沉淀的问题池。可信管理员引用问题时，从同群 `Reader.Find` 查原文；Bot、命令、空文本不作为问题或回答候选。
- history 当前丢弃引用原文。补充有界的引用 ID、引用文本字段，沿用 `message_history.max_text_chars`，不保存图片字节或媒体 URL；Reader 和事件返回的引用数据同样保持副本语义。原问题已移出时，仅使用适配器已解析且 ID 匹配的引用原文；没有原文就跳过，不额外远程拉消息。
- 管理员引用锚定问题后，模块立即为该答题会话保存有界的材料快照：问题文本和消息 ID、回答文本与消息 ID、管理员 ID、起止时间；关联标记用“群 + Seq”或消息 ID。后续答复到来时保存到同一会话，不依赖将来再从 Reader 读到正文。只保存已经开始的答题会话，不重建普通消息候选池。
- 保留“同群、同一管理员在静默窗口内的连续回答归为一组”。显式引用可确定新问题；不引用且无法归入已有会话的管理员发言不自动猜问题。
- 定时处理时从模块已保存的会话组装 `AnswerSession`，不要求问题或回答仍在 history 内。将该纯数据类型迁到 `internal/reflection/session`，pipeline/reflector 只改类型引用与对应测试，不修改提炼或发布算法；避免移到会造成循环依赖的 reflection 控制包。

### 2.2 静默、模块会话与定时批处理

History 回调在收发链路同步执行，只识别锚点、保存会话必要材料并更新有界状态；不在回调里调用模型、写文件或重载索引。

模块自己的定时任务按周期批量检查已形成的会话，安静窗口结束或达到最长会话时间后交给 pipeline。没有下一条群消息也会处理。删除独立 scanner 包，将单批互斥、计数与定时任务归入模块；不遍历全部 history 来重新发现问题，也不为删除包强制更换调度策略。

- 可处理条件为 `当前时间 >= min(最后回答时间 + quiet_window, 开始时间 + max_session_duration)`，在随后一次定时批处理中执行；每批最多 `max_pairs_per_scan`，后台与手动入口共用单批互斥。
- 问题或回答因容量、TTL 或配置变化滑出 history，均不清理已经保存的答题会话。模块无需订阅 Evicted，也不在处理前以 Reader 可见性作为候选有效条件。
- 会话的材料、处理标记和重试保留都受模块自己的条数、文本容量与生命周期约束。满额时释放最旧会话并记录计数，不截断某个会话后继续将其作为完整材料发布，不改变 Core history 上限。
- 模型或发布失败由模块保留该会话，在后续定时批次再处理，按模块自己的保留期限释放，不因 history TTL 到期而释放；取消不触发立即忙重试。批次内成功、拒绝、低置信度和重复等结果仍沿用 pipeline 语义。
- 已完成会话释放正文，处理标记在模块内有界保留用于去重，按自身期限/容量清理，不依赖消息移出事件。
- 热更新后新实例不重放历史管理员回答；新到达的回答可以引用保留的旧问题。旧实例 Retire 取消待处理任务，Close 等待在途工作退出。重启和模块换代不承诺保存待沉淀会话。

### 2.3 配置和控制入口

撤回上一稿“一次删除五个配置键”的建议。移除所有成员消息的 24 小时候选问题池，不意味着答题会话可以不受资源和生命周期约束，也不意味着取消模块定时任务。

最小配置迁移建议：保留 `scan_interval` 作为模块定时批处理周期，`message_ttl` 只约束已形成会话及处理标记的保留，不再保存所有成员问题；`max_messages_per_conversation/max_messages_total/max_message_chars` 仅约束已形成会话的材料。其余 `conversation_ids/quiet_window/max_session_duration/max_pairs_per_scan/min_confidence/extra_directory` 继续沿用。实现沿用这些字段，把约束对象缩到已形成会话，并同步文档。

Core history 默认 50 条、30m、1000 字不自动扩大；它影响首次查找问题的可用范围，不能作为已经形成会话的隐式失效条件。字段若最终删除或改名，旧键仍按严格解析报错，配置手册、索引、示例及 init/doctor 同步更新；保留字段发生语义变化也必须明确记录。

保留 `POST /v1/admin/reflection/scan`，含义是立即处理已经到期的候选，不强行截断还在回答的会话。状态改为 pending/worker/pipeline 计数，不再暴露 buffer/scanner 结构，不返回正文。

### 2.4 沿用的知识文件发布策略

- 一个已完成答题会话经模型提炼、接受并达到置信度后，产生一条知识；每条知识单独写入 `extra_directory/<哈希前16位>.md`，不按原始消息逐条建文件，也不向汇总文件追加。文件包含提炼后的问答和托管来源元数据，时间取会话开始时间。
- 发布器按完整哈希或“群 + 问题消息 ID + 首条回答消息 ID”判重复。来源相同，即使模型输出变化也跳过，不覆盖或修订旧文件。哈希本身包含来源身份与提炼内容，不提供跨来源的语义去重。
- 相同问题在不同消息/群、不同首回答来源下可能形成多份知识；不自动合并补充答案、创建修订版本或用新答案替换旧答案。保持临时文件完整写入后重命名、发现同名占用时报冲突、批次发布后统一重载索引。
- 这是对当前 publisher/pipeline 行为的说明，不扩大 BOT-14 的代码重构范围。用户说明见 `docs/configuration.md` 的“沉淀文件的保存、去重与更新”。

## 3. ARCH-P06：单一 Connector Provider 构建路径

删除 `runtime.ConnectorFactory` 类型、Builder 两个旁路字段及对应分支。连接器类型只来自启用模块的注册项；复用 `connectors_test.go` 现有测试模块形式迁移 manager 测试。

保留 Sender、ClientFactory 等与本项无关的边界测试注入。新 Provider 仍遵守：Build 只构建，Start 激活，Stop 释放，Errors 只报告生命周期故障。覆盖普通热更新复用、连接器更换、启动失败恢复、持久化失败恢复、禁用和关闭，不能为删注入删掉测试断言。

## 4. BOT-19：宿主单条消息撤回能力

### 4.1 目标纠正与发送边界

2026-11-10 用户先取消通用发送回执，后明确撤回只做宿主开放能力。Sender/ConnectorSender 保持 error-only，出站正文记录与个人会话记忆不变，不新增自身消息回推。删除此前聊天 `/recall` 实现、管理员聊天授权、直接引用 Bot 的让位增强和 history 清理扩展，原主动插话行为保持已提交基线。

### 4.2 共享 capability 与路由

新增 `group.RecallKey`（`group.message_recall.v1`），值为窄接口 RecallProvider。调用方先 `ForConversation(conversationID)` 得到该群绑定的 MessageRecall，运行时 `Recall(ctx, messageID)` 只接收消息 ID。逻辑群作用域用于避免多个连接器的消息 ID 冲突，不是内容/违规策略。

Runtime 在模块构建前提供此 capability；模块声明 Requires 后可解析，宿主服务也可调用 Manager.ForConversation。不向消费者暴露 Runtime 或凭据仓储。能力在创建时保存连接器 ID 和目标，执行时复核当前绑定，无关热更新不影响使用，改绑/解绑使旧能力返回 conflict。全局暂停、ctx 取消、20 秒操作截止时间和正常关闭继续适用。

连接器提供可选 `group.Recaller`，Registry/ConnectorSet 路由具体平台目标，Support 的 message_recall 与账号权限分开。OneBot 直接调用 delete_msg；Core 和适配器不根据 history 可见性或本地消息年龄拒绝请求，QQ 等协议的实际时限/权限由平台响应表达，不施加通用两分钟限制。

### 4.3 操作结果与明确留白

不支持、权限、平台过期或未知原因沿用 operation 分类；请求后超时保留 effect=unknown，不自动重试、不写群提示。操作本身不修改 history/短期问答/沉淀内容，取消本批新增的 Remove/Clear/removed 事件和改绑清理。消费方如需标记或清理自身状态，后续按其业务设计，不作为基础撤回前置条件。

暂不添加聊天命令、HTTP 操作 API、LLM Tool、违规判断模型或扫描任务。调用者负责选择业务目标与授权，不以“管理员发来一条消息”作为能力调用前提。未来可在既有功能/模型调用中合并违规判断并使用此能力，避免为动作本身增加模型请求；不把示例当成已确认实现范围。

## 5. 外部入群审批

### 5.1 宿主执行与配置边界

`native.join_review` 仍只提供数据策略；外部 HTTP 客户端放在 Core 审批执行边界。请求输入需要完整 Event 的 application、user_id、conversation_id，不能继续只把 application 传给 evaluateReview。

外部配置非空即启用，仍由模块和 `groups.<id>.enabled` 控制整体审批。启动/热更新校验 URL、HTTP method、模板、表达式及 Header 格式。HTTPS 必需，HTTP 仅允许明确的 localhost/回环地址供本地测试；禁止 URL userinfo 和自动跨站重定向。请求固定 5s，响应最多读取 64KiB+1，超限整体失败，不能将截断后碰巧合法的 JSON 当成功。

`headers` 值中的 `{<SecretStore键名>}` 表示凭据引用，例如 `Bearer {join_review_token}`。只解析配置明确列出的键；模块和外部服务不拿到 SecretStore 对象。缺失凭据不能以空字符串发送，凭据替换对后续请求生效，已经开始的请求使用自己的快照。公共配置与日志只返回是否配置，不输出解析后的 Header、响应正文或 Token。

`body` 保持字符串配置形式，但必须是 JSON 模板：先解析结构，在字符串值中替换占位符，再由 json.Marshal 编码。这样 application 中的引号、换行、反斜杠不会改变 JSON 结构。空 body 不发送正文；不支持任意非 JSON 模板或键名替换。这是对需求“序列化后替换且无注入”的实现澄清。

### 5.2 表达式与组合语义

建议首期只支持 jq 风格的对象路径与布尔比较：`.approved`、`.data.approved`、`.data.approved == true/false`。不引入完整 jq、脚本执行、管道、函数和动态索引。结果必须为一个布尔值；缺失路径、非布尔值或无效 JSON 记一次诊断，外部判断为 false。非法表达式配置加载时直接拒绝。

启用项固定按成本升序串行求值：正则 → 外部 HTTP 校验 → 模型判断；跳过未启用项，不并发执行，短路后不再执行后续判断。AND 遇 false 即拒绝，OR 遇 true 即同意；HTTP 失败、非 2xx、超时、超限或缺失凭据记为 unknown，AND 无法同意，OR 继续尝试其余启用项，最终没有 true 则拒绝。启用外部判断时按该规则组合；未配置外部判断时保留当前正则/模型行为，包括模型故障的 undecided。

外部 unknown 不消耗模型预算；模型仍最多一次，DM/LLM 行为不变。调用方上下文取消时立即退出，不继续执行拒绝操作。执行审批前继续复核当前策略和绑定；Policy.Equal 必须比较外部配置内容，不能按 map/指针地址判断，也不能把已解析密钥放进可公开的 Policy。

## 6. QQ 官方机器人 Connector

### 6.1 首期建议与官方依据

建议首期只接 QQ 群聊，采用 Webhook，复用现有部署的 HTTP 接入模式；频道、C2C、双接入模式暂不实现。官方现行文档同时描述 Webhook 和 WebSocket，不能据此声称 WebSocket 已停用。此范围随本次实施授权确认。

2026-11-10 核对的官方资料：

- [Webhook 接收](https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/event-emit/webhook.html)：验签、校验回调和公开 HTTPS 接入要求。
- [WebSocket 接收](https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/event-emit/websocket.html)：另一种接入方式，本次不同时实现。
- [API 鉴权](https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/interface-framework/api-use.html)：AppID/Secret、AccessToken 和请求认证。
- [群 @ 消息](https://bot.q.qq.com/wiki/develop/api-v2/autogen/event/group_at_message_create.html) / [全量群消息](https://bot.q.qq.com/wiki/develop/api-v2/autogen/event/group_message_create.html)：事件、ID、引用和平台角色字段。
- [群消息发送](https://bot.q.qq.com/wiki/develop/api-v2/autogen/api/v2_groups_group_openid_messages.post.html)：被动回复、发送回执及发送限制。
- [群消息撤回](https://bot.q.qq.com/wiki/develop/api-v2/autogen/api/v2_groups_group_openid_messages_message_id.delete.html)：当前公开撤回接口、权限和时限。
- [官方 Go SDK](https://github.com/tencent-connect/botgo)：仅作协议实现参考，不引入旧 WebSocket 架构或未经需要的 SDK 依赖。

以上采用实际页面标题与正文核对；部分旧地址虽然返回 HTTP 200，正文却是“启动接入”，不能视为接口证据。这些资料证明接口契约，不证明部署账号拥有权限或已通过验收。实施时重新核对请求字段和错误码，真实沙盒权限以用户账号为准。

### 6.2 模块与凭据生命周期

新增 `internal/modules/qqbot` 与 `internal/platform/qqbot`，在 NativeModules 注册 `native.qqbot`（默认关闭），Provider 类型为 `qqbot`。独立于 OneBot 的协议实现。

建议实例 options 为 `app_id/secret_ref/webhook_addr/webhook_path/timeout/request_limit/replay_ttl/replay_max`；群 target 为 group_openid。API 和 Token 地址使用官方固定 HTTPS 地址，不增加面向生产的任意地址配置；测试用注入 HTTP client/transport。

当前 ConnectorBuildRequest 没有凭据能力。增加 Provider 可选的“声明所需凭据引用”接口；宿主只解析所声明的凭据，并将本实例需要的值传入构建请求，不把整个 SecretStore 交给 Provider。引用收集用于 QQ 凭据替换/删除的候选运行代构建，候选凭据只供候选代使用；不能沿用当前只识别 LLM/DM 引用的快捷返回。

Build 校验并准备资源，不监听或请求网络。Start 加载 Token 并启动回调服务，Token 由本实例缓存，临近过期时单次刷新，并发请求共享刷新结果；Stop 取消刷新与消息任务。凭据变化通过连接器替换更新缓存，不在旧代原地换密钥。外部审批使用每请求解析，不能因此顺手重构整个 MCP 凭据生命周期。

### 6.3 入站、引用与投递

- Webhook 限制请求大小、核对 AppID 并解析 JSON。2026-11-11 对照官方回调示例，纠正“op=13 也强制验签”的旧设计：地址验证允许无签名头，校验 event_ts 的现有时间窗口及 plain_token（1..4096 字节，ASCII 字母/数字及 `-_+/=`），仅返回 event_ts + plain_token 的 Ed25519 签名，不进入业务队列或缓存。令牌拒绝 JSON、空白/控制字符，避免作为业务事件签名入口。业务事件继续对原始 timestamp + body 验签及校验签名时间，不能解码再编码后验签。通过后做有界去重、入队并及时 ACK，后台调用宿主，不能等待 LLM 完成再 ACK；队列满返回可重试失败，不先确认再丢失。
- 映射群/用户开放 ID、消息 ID、文本、附件、引用及角色。`GROUP_AT_MESSAGE_CREATE` 取得明确触发资格；全量事件读取 `mentions[].is_you=true`，在去重前设置 Mentioned，仅清理该项 id/member_openid 对应的 `<@ID>`/`<@!ID>` 标签再识别命令，保留其他正文。两类事件按同群消息 ID 去重，不因到达顺序或并发重复触发。缺少 is_you 时不由正文、AppID、昵称或 Bot 字段推断身份，仍沿用消息 ID 去重。普通群消息进入 history 和已有主动插话策略，`@全体` 和其他成员不扩大明确触发资格。
- 附件在 platform/qqbot 内归一：保留合法 HTTP/HTTPS，无协议头的裸域名或 `//` 地址补 HTTPS；用 URL 解析拒绝缺主机、非法格式、用户信息及其他协议，不发下载请求。非空 content_type 优先，仅缺失时按文件名 `.jpg/.jpeg/.png/.gif/.webp/.bmp`（大小写不敏感）识别并补图片 MIME，其余沿用文件/视频/语音映射；最多保留 16 个有效附件。图片交给模型仍由既有 llm.image_understanding 控制，不改 Core 媒体接口或配置字段。
- 官方当前群角色字段可映射 owner/admin；缺字段时视为普通成员，不从昵称、用户文本或运维群配置提权。连接器没有收到全量消息权限时，文档说明 history 和沉淀只能看到实际投递的事件。
- 官方消息引用索引与消息 ID 不是同一字段。适配器维护有界映射，将平台引用归一为 Core 消息 ID；无法解析时不猜。被动回复使用触发消息 ID，引用展示仅在平台引用索引已知时附加。
- 官方发送响应的消息 ID 和引用索引仅在适配器内部保存用于协议操作，不返回通用回执或补写宿主 history。进度提示与最终回答可能回复同一消息，`msg_seq` 由适配器在有限有效期内按触发消息管理，不能始终固定为同一个值；不增加自动发送重试。
- 首期以被动文本回复完成问答链路，遵守平台有效期和条数限制。没有平台允许的触发依据时，对欢迎语、通知或主动推送返回明确 unsupported/expired，不默认消耗主动推送额度。出站富媒体不随本版扩展；纯文本可用时沿用文本形式。
- 实现已公开的群消息撤回接口，并通过 BOT-19 基础能力路由。适配器不按内部缓存可见性或本地两分钟计时拒绝请求，实际限制与账号权限以官方响应为准，映射已知权限/过期码。群入群审批/新人事件未实施时明确为不支持，不伪造为聊天消息。

### 6.4 能力目录与验收

目录继续分开表示：协议实现支持、配置启用、已加载、账号权限未知和实际调用结果。至少列明群文本收发、撤回，以及未实现的审批、成员事件、出站媒体和主动推送边界；按需给现有目录补最小投递能力字段，不构建新的通用能力框架。

受控测试覆盖签名/校验/重复/队列满/关闭、Token 并发刷新、群绑定、角色、@全体、引用、msg_seq、权限和过期。真实账号必须完成“有效回调 → @Bot → 模型回答 → 平台收到消息”；如账号具备撤回权限，再验 capability 按 ID 撤回。没有账号或公网回调条件时，单独记录真实验收待办，不标版本完成。

## 7. 文档、验证与交付

2026-11-10 实施补充：为外部审批等按引用配置的凭据提供运行中管理入口，新增 Bearer 认证的 `GET/PUT/DELETE /v1/admin/secrets/{ref}`。GET 只返回配置状态/版本/更新时间，PUT 使用 `value`，不提供读取明文；复用 Secret Manager 候选激活和 Runtime.ReplaceSecret/DeleteSecret，保留回答/DM 必需凭据保护。外部审批在后续请求读取当前凭据。QQ Provider 与凭据换代随后分批实施，完成情况见 tasks。

连接器凭据生命周期补充：Provider 可选实现 CredentialProvider，宿主仅向当前实例传入其声明的键值。凭据替换将候选值用于构建新运行代和整个连接器集合，沿用停止旧集合、启动新集合、激活保存凭据、发布的顺序；失败尝试恢复旧集合并保留旧凭据，恢复失败返回错误。必需凭据删除在候选构建时拒绝。入站等待换代时可取消，避免 Stop 与路由屏障互相等待。QQ Provider 和 doctor 在本批接入，配置示例与 init 同步列出默认关闭的 QQ 参数。

每个工作包同步更新自己的 specs 进度、用户 docs 和示例；变更配置同时更新 `docs/config-index.md`。计划文档不能让用户文档提前声称新功能可用，不改本地部署配置或密钥。

每次业务代码修改后执行 gofmt、`go build ./...`、`go vet ./...`，并运行覆盖关键行为的测试。提交前还需完整 `go test ./... -count=1` 与 `go test -race ./internal/... -count=1`。规划提交前的基线验证结果见 tasks，实施后继续更新。

人工审核闭环、自动分段/重试、FPP、WebUI、数据库后端和公开 SDK 继续延后。真实沉淀、OneBot 撤回与 QQ 官方链路分别保留验收证据；不以模拟服务代替真实平台验收。

## 8. Telegram 群聊连接器（需求 §1.5）

### 8.1 官方依据与总体边界

协议核对日期：2026-11-11。Telegram 接入以以下官方资料为依据：

- [Bot API：更新接收、getUpdates、setWebhook、deleteWebhook](https://core.telegram.org/bots/api#getting-updates)。
- [Message 与实体](https://core.telegram.org/bots/api#message)、[MessageEntity](https://core.telegram.org/bots/api#messageentity)：实体偏移使用 UTF-16，引用随 update 提供。
- [getMe](https://core.telegram.org/bots/api#getme)、[getChat](https://core.telegram.org/bots/api#getchat)、[getChatAdministrators](https://core.telegram.org/bots/api#getchatadministrators)。
- [PhotoSize](https://core.telegram.org/bots/api#photosize)、[getFile](https://core.telegram.org/bots/api#getfile)、[sendMessage](https://core.telegram.org/bots/api#sendmessage)、[deleteMessage](https://core.telegram.org/bots/api#deletemessage)。
- [隐私模式](https://core.telegram.org/bots/features#privacy-mode)、[Webhook 部署](https://core.telegram.org/bots/webhooks)。
- 官方 [TDLib 文本长度检查](https://github.com/tdlib/td/blob/master/td/telegram/MessageContent.cpp) 使用 `utf8_length`，与 [UTF-16 长度函数](https://github.com/tdlib/td/blob/master/td/utils/utf8.h) 区分；纯文本上限按 Unicode 字符计数，不能照搬实体偏移单位。

新增 `internal/modules/telegram` 和 `internal/platform/telegram`。前者只提供严格 options、凭据声明、脱敏和 Provider，后者以标准库 HTTP 对接官方 Bot API，不引入 SDK。发送和媒体沿用现有 messaging 类型，Send/SendTo 继续只返回 error，不为 Telegram 恢复回执或扩展 thread 字段。

### 8.2 配置与账号

模块 ID 为 `native.telegram`，默认关闭，模块 options 只接受空对象。connector 类型为 `telegram`，实例 options 如下；新增字段均限于该 Provider，不进入 Core 配置结构。

| 字段 | 约定 |
| --- | --- |
| `token_ref` | 必填，SecretStore 中 Bot Token 的引用 |
| `mode` | 必填，`long_polling` 或 `webhook`；只能选择已实现的方式，不运行两套接收器 |
| `timeout` | 可选，普通 API/下载超时默认 15s，必须为正；长轮询请求预算为 30s 加该超时 |
| `webhook_url` | webhook 必填，公网 HTTPS 地址，无 userinfo/fragment |
| `webhook_addr`、`webhook_path` | webhook 必填，本地监听 host:port 与绝对路径；不强制公网路径等于本地路径，允许反向代理映射 |
| `webhook_secret_ref` | webhook 必填，SecretStore 中回调认证文本的引用；实际值遵守官方 1..256 字符、字母/数字/下划线/短横线约束 |

长轮询可省略 webhook 字段，也允许保留通过结构校验的 webhook 示例值；仅在 webhook 模式解析其凭据。Provider 仅声明当前模式所需的凭据，公开配置返回是否设置，不回显 Token、认证文本或其解析结果。init 与总示例默认关闭模块和连接器，显式填写 mode，独立示例分别展示两种启用方式；使用现有按引用凭据 API 轮换。

群绑定 target 使用稳定的十进制 chat_id，按有符号 int64 校验并以字符串传递，不接受用户名代替。Start 调 getMe 获取自身 ID/username，并逐个 getChat 验证绑定只为 group 或非论坛 supergroup。私聊、频道、论坛群、不可访问群使候选启动失败；运行中也忽略私聊、频道、论坛/话题消息，群转为论坛后不把话题合并到既有会话。普通群升级导致 chat_id 变化时返回可诊断的绑定冲突，部署者更新配置，不自动改写绑定。

### 8.3 两种接收方式、确认与恢复

两种方式显式选择相同 allowed_updates：`message` 和 `my_chat_member`；旧订阅遗留的其他 update 仍按不支持忽略。不订阅 chat_join_request、chat_member、channel_post 或 edited_message。

- **长轮询**：Start 成功执行 deleteWebhook(drop_pending_updates=false)，用 timeout=0、limit=20、同一 allowed_updates 的首个 getUpdates 验证可用，再启动唯一轮询器。首个响应若含更新，必须走正常接纳流程，不能丢弃返回值后跳 offset；接纳后把当前 offset 交给轮询器。每次 getUpdates 使用 timeout=30s、limit=20；按收到的顺序逐项入队或明确忽略后才推进 offset，不能直接确认整批最大 ID。队列满时等待空间，等待可取消。暂时网络故障/429 在读取循环中可取消退避（普通指数退避 1s 起、最高 30s；存在 parameters.retry_after 时完整遵守平台等待值，即使超过 30s），不把读取重连扩展为发送重试；401/403 或重复消费者的 409 结束本接收器并报告生命周期故障。
- **Webhook**：先准备监听和队列，再调用 setWebhook，传当前公网地址、secret_token、allowed_updates、max_connections=1 和 drop_pending_updates=false。Telegram 没有 QQ 的 op=13 挑战；只接收 Update。每次请求校验 `X-Telegram-Bot-Api-Secret-Token`，请求体上限 1MiB；非法认证/报文不入队。成功入队或明确忽略才回 200，队列满/尚未就绪回 503，留给平台重投，不能等模型完成才 ACK。
- **共同接纳边界**：固定 128 个更新的内存队列、4 个后台工作者，队列只存 DTO/file_id，不预下载图片。以 update_id 做 5 分钟内、最多 10000 个的有界去重；满容量不确认新 update。去重检查、入队与记账原子完成，未接纳的更新不留去重标记。offset 和短期去重标记属于 adapter 实例：同对象 Stop/恢复 Start 不清零，新的 adapter 或进程从平台未确认更新重新开始；队列和在途任务在 Stop 时取消。首个探测也使用该实例保留的 offset，只有下次 getUpdates 携更大的 offset 才在平台确认之前的更新；不使用负 offset 丢弃积压，不要求 update_id 连续无缺口。offset、队列和标记都不持久化，ACK 只表示进入内存；停机可能丢失已 ACK 的排队/在途工作，不承诺跨重启 exactly-once 或补处理历史。
- **启停/换代**：Build 不联网。Start 失败自行清理自己已创建的监听/协程，远端登记结果未知时尽力撤销；不能依赖 Set 为启动失败的那个 adapter 调 Stop。Stop 先停止接纳、取消轮询/下载/处理并关闭本地资源；webhook 再用独立、有界上下文尽力 deleteWebhook(false)。远端清理失败记脱敏诊断，不在本地已停止后伪装本地 Stop 失败；本地资源未退出仍返回错误。每次重新 Start 都按当前账号/模式执行登记，不使用“曾经登记过”的缓存。复用 Runtime 的整组连接器换代和旧集合恢复，不增加另一套凭据事务。

为避免切换期间新欢迎事件访问旧代，给 HandleGroupEvent 补上与 HandleInbound 相同的可取消 routeMu 读取屏障，复用私有等待逻辑；不改公共 Handler 或模块接口。测试必须覆盖候选 Start 后、publish 前的群事件，以及 Stop 取消等待，不能用延迟启动工作者猜测代已发布。 兼容既有 OneBot：其处理当前使用 WithoutCancel 脱离 HTTP 请求取消，加入屏障后不能等到 Stop 超时才退出。OneBot Start 为每次启动创建生命周期上下文并由回调捕获，处理上下文仍不随客户端断开取消，但必须绑定该生命周期；Stop 先取消等待中的处理再关闭服务。仅补必要的生命周期关联，不改 OneBot 协议行为或配置；已有进入处理的旧代事件由读取屏障保证先完成，尚未进入的等待者可取消。

### 8.4 消息、引用、角色与欢迎

- `message_id`、`from.id` 转十进制字符串；ConversationID 来自显式群绑定。过滤自身和其他 Bot 消息；含 sender_chat 的匿名管理员/频道身份首期忽略，不伪造普通用户或可信管理员。
- Text 使用 text 或 caption；entities/caption_entities 的 offset/length 以原文 UTF-16 码元解析，先校验范围和代理对边界，再解析 bot_command 的目标，不能先删文本破坏原始偏移。依据 getMe 的 username/ID 识别 mention/text_mention，按 offset 逆序清理而不依赖数组顺序，移除只属于自身的 @ 文本后再确定命令；其他正文保留。回复同群 Bot 消息也置 Mentioned。对显式 `/command@OtherBot`，适配器直接忽略，不能只设 Command=false，因为现运维路由仍会解析 Text；本 Bot 后缀去掉后交给现有路由。
- 同群 reply_to_message 的 ID、正文和图片分别映射到 ReplyToMessageID/Quoted，不并入当前 Text；只处理一层，不把 external_reply、跨群引用或单独 quote 片段伪装成完整同群原文。没有通用 getMessage，不新增按 ID 远端取原文。
- 绑定群按需 getChatAdministrators，creator→owner、administrator→admin；成功缓存 5 分钟，失败丢弃过期权限并保守 member，负缓存 30 秒。缓存只包含绑定群，my_chat_member 使相关缓存失效；能力目录仍分开表示支持与权限，不能由配置或用户名赋权。
- 仅将 message.new_chat_members 作为首期新人来源，每位真人分别生成 MemberJoined；UserID/UserName 取该成员元素而不是 message.from（可能是邀请人），事件 ID 由 connector/chat/update_id/user_id 生成，At 取服务消息的有效正数 date，时间缺失或非法时不伪造当前时间。机器人/自身加入不触发欢迎，服务消息不再作为空文本问答。不订阅第二套成员事件，不承诺补齐平台未投递的成员变化。复用 native.welcome 的开关、暂停和纯文本模板，不增加模板语法；不提供 Telegram Reviewer，join_events=false、member_events=true、join_review=false。

### 8.5 图片与投递

photo 的多个尺寸是一张图，不假定数组有序；按像素面积选择最大且已知大小不超本地限制的版本，未知大小仍在下载时限流。没有可选尺寸时返回超限失败，不能当作无图消息继续问答；下载后超限不再自动换小尺寸重试。支持 MIME 为 image/* 的图片 document；缺 MIME 时按文件名的常见图片后缀补类型。当前与同群引用图合计最多 4 张，每张 5MiB、单条总计 20MiB，已知超限不下载，实际读取使用 limit+1 验证。超过数量/体积或下载失败时停止该条处理并返回分类失败，不静默丢图后让模型猜测；不聚合相册。非图片附件只映射现有类型/元数据，不额外下载。

在后台工作者中调用 getFile 并有界读取文件，只访问固定的官方 HTTPS API/文件主机、禁止自动重定向。getFile 的 file_path 只用于构造固定官方主机下的文件路径，不把它解释成外部地址或本地文件。PhotoSize 不含 MIME，getFile 不保证原名/类型；下载后用文件头识别实际图片 MIME（JPEG/PNG/GIF/WebP/BMP），图片 document 的声明/文件名只作下载前候选识别，类型无法确认则返回 unsupported，不凭 HTTP 通用类型或一律 JPEG 构造错误的模型输入。首期不新增缩放、转码或图片持久化。图片只设置 Media.Data/MimeType/Name，不把含 Token 的下载地址填入 Media.URL；保留现有 history 不存图片、短期记忆不保留内联 Data 的行为。适配器不读取宿主模型配置：即使图片理解关闭也可能完成下载，随后仍由 Core 返回既有不支持提示，不为省下载新增跨层接口。

SendTo 调 sendMessage，不设 parse_mode，不渲染 Markdown/HTML，Segments 使用已有 Text 形式降级；首期不发 Media。UTF-8 无效、空文本或超过 4096 个 Unicode 字符，在请求前返回 invalid；不按实体的 UTF-16 单位计算正文上限，不拆分/截断。ReplyToMessageID 用 reply_parameters 指向同群原消息，显式 allow_sending_without_reply=false；引用不可用交平台错误处理，不自动去引用重发。主动插话/通知/欢迎可无 ReplyTo 投递，passive_replies_only=false，outbound_segments=false。

RecallMessage 使用绑定 chat_id 和给定 message_id 调 deleteMessage；平台决定权限/时限，不按本地缓存存在性或年龄拒绝，不触发聊天提示。发送结果中的平台 ID 仅留协议内部，不补写 Core history。API 响应以固定 8MiB+1 限量读取，读取失败、超限、无效 JSON 不使用截断结果，不推进轮询 offset；HTTP 非 2xx 或 ok=false 都按失败处理，沿用 operation 分类，并解析 parameters.retry_after/migrate_to_chat_id。收到 migrate_to_chat_id 时返回绑定 conflict，诊断给出新 chat_id（int64），不自动修改绑定、重定向发送或重试；getChat 启动检查也处理该结果。请求已发出后超时保留 effect=unknown，发送/删除不自动重试。限流、权限和单条操作失败不进入 Adapter.Errors；底层错误含 Token URL 时必须先去除地址，再包装可公开分类，不记录原始请求地址、文件路径或响应正文。

### 8.6 验证与交付

逐批实现见 tasks §7。每批同步 specs、docs、config-index、init 与相应示例，只暂存当前功能范围供用户审阅，不创建提交。受控测试要独立验证两种接收、自动切换、凭据替换失败恢复、取消、去重确认、UTF-16 实体、普通/跨群引用、角色失败降级、欢迎和撤回、图片体积及 Token 不泄露。真实群聊两种模式分别验证后才能宣称接入完成；v0.2.0 的其他真实验收仍独立保留。
