# v0.2.0 任务顺序与交接

日期：2026-11-10。范围见 [requirements.md](requirements.md)，方案见 [design.md](design.md)。

状态：用户已于 2026-11-10 授权提交规划文档并开始实施，先推进工作包 1。工作分支 `feat/v0.2.0`，核对基线 `1103bc7`。只在用户明确要求时提交，不自动推送、合并或打标签。

2026-11-11 追加：用户确认 Telegram 群聊连接器纳入 v0.2.0，需求 §1.5、设计 §8；规划已提交 `10c5b2f`。同日接手审查并完成 7.2–7.4，全部未提交，待用户审阅。原版本验收移为工作包 8，工作包 7 按以下批次推进，每次只暂存一批供用户审阅。

## 0. 上下文与基线

- [x] 阅读 AGENTS.md、计划入口、配置索引和 v0.2.0 需求，核对当前代码。
- [x] 核对：独立沉淀 buffer/scanner、Connector 宿主旁路仍存在；发送不返回消息 ID；未实现撤回、外部审批或 QQ 官方 Provider。
- [x] 编写设计和任务草案，更新当前计划入口与路线图，保留历史。
- [x] 在 Windows/amd64、Go 1.27.3、`1103bc7` 上执行基线：`go build ./...`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./internal/... -count=1` 全部通过。
- [x] 用户于 2026-11-10 要求“提交文档后开始实现”，确认修正后的设计与任务顺序。

## 1. 自动沉淀使用核心 history（BOT-14，需求 §1.1）

- [x] 将 AnswerSession 迁到独立纯数据包，调整 pipeline/reflector 类型引用；保留现有提炼、去重、发布和索引重载测试。
- [x] history 增加有界引用原文及副本读取；移除沉淀 Observer，仅订阅 Appended，模块依赖 Reader，不订阅 Evicted。
- [x] 可信管理员引用时先查同群 history，查不到用适配器已带回的引用原文；两处都无正文才跳过。保存已形成会话的有界问题/回答材料和关联标记，保留管理员连续答复归组；不缓存全部成员问题。
- [x] 模块自己的定时任务批量处理静默/最长时长到期会话，手动与后台互斥；失败后续批次重试，按模块自己的容量/期限释放；Retire/Close 取消并收尾。
- [x] 删除 reflection/buffer、reflection/scanner 包；沿用所有现有配置键及默认值，周期/TTL/容量的约束对象缩到已形成会话和标记；状态改为 pending/worker/pipeline。
- [x] 同步 configuration、config-index、默认行为、architecture、功能清单及双语 README。示例字段和值无需迁移；配置手册已说明 init/doctor 行为和语义变化。

- [x] 补充现有保存策略：每条成功沉淀的知识单独生成哈希命名 Markdown；同源三项/完整哈希重复跳过，不追加、不覆盖；不同来源可能重复，不做语义合并或自动修订。仅补充 docs/specs，未修改发布算法。

出口：管理员回答后没有新消息仍能定时发布；多消息回答不提前拆散；同一候选不重复处理；普通成员不能伪装管理员；原问题已滑出但引用有正文时仍能开会话，两处都没有才跳过；已捕获的问题/回答随后滑出不影响处理；会话状态有界；热更新不重放旧回答。若最终删除配置键，旧键残留被明确拒绝。真实运营效果在工作包 8 验收。

实施记录（2026-11-10）：会话、容量/期限、静默/最长时长、批次上限、失败重试、取消、history 引用副本测试通过；Runtime 已验证换代不重放旧回答、新回答仍能引用保留的问题。build、vet、全量普通测试及 internal 竞态测试通过；真实沉淀质量仍待工作包 8 验收。

### 1.1 普通知识文档组织（2026-11-12 用户追加）

- [x] 同一次反思调用产出主题标题、简短摘要、自然 Markdown 正文和标签；不再固定生成“问题 / 答案”两节，不额外调用模型整理。按用户补充，聊天只作事实材料，提示词明确允许改写措辞、重排与合并答复，以客观正式的文档语气表达，保留事实与准确的技术字面值。
- [x] 调整 Result → Entry → publisher 的字段传递与正文排版；摘要进入标准 frontmatter，复用现有索引的标题、摘要、标签与标题层级。
- [x] 保留置信度、敏感内容约束、每条一文件、来源去重、原子发布和批次重载；验证同源旧问答文档不会被覆盖或重复发布。
- [x] 补齐结果字段边界、自然正文保存、真实 publisher → structured search 的集成测试，以及用户文档与规格说明。
- [x] 独立暂存快照通过 `go build ./...`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./internal/... -count=1`；本批单独暂存供审阅，未创建提交。

出口：产物可直接作为普通知识文档阅读；标题、摘要、主题小标题、标签和代码键均能经现有结构化索引命中；真实供应商生成质量仍留工作包 8 验收，受控测试不替代运营效果。

## 2. 删除 Connector 宿主旁路（ARCH-P06，需求 §1.2）

- [x] 删除 ConnectorFactory 类型及 Builder.ConnectorFactory/ConnectorProviders 字段和构建分支。
- [x] 将 manager 测试改为测试模块注册 Provider，保留原有生命周期计数和失败恢复断言；实际 ConnectorSet 参与测试。
- [x] 核对生产组合根、doctor 和普通测试都走模块 Provider；同步架构与功能清单。

出口：宿主旁路已不存在；Echo/OneBot 注册、禁用、热替换、启动失败/保存失败恢复、关闭均通过；不删无关 Sender/LLM 注入。

## 3. 通用发送回执：已取消并撤回（2026-11-10）

用户明确要求“完全撤回回执改动”。此前为本功能暂存的 27 个文件片段已取消暂存，DeliveryReceipt、发送接口迁移、OneBot 发送 ID 读取、宿主按发送响应补全 history ID 及专用测试已从工作区撤回；保留其他功能的未提交片段。

后续代码恢复使用原 error-only 发送接口。QQ 发送结果中的协议 ID/引用索引只留在适配器内部，不构成通用回执或填入 Core history；移除能力目录 message_receipts 字段和相关承诺。已提交的宿主无 ID 出站正文记录和个人连续问答记忆保持原样，本次不实现自消息回推识别。

后续用户进一步纠正 BOT-19 为无 history 依赖的基础撤回能力，旧引用命令/让位扩展不再保留；取消的工作包 3 保留编号供追溯。

回退验证（2026-11-10）：`go build ./...`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./internal/... -count=1` 全部通过。已确认原 27 个回执暂存片段清空，其他功能改动保留，未创建提交。

## 4. 宿主单条消息撤回能力（BOT-19，需求 §2.1）

用户于 2026-11-10 指出管理员引用命令偏离目标，已改为只提供可调用能力，业务消费方后续确定。

- [x] 删除聊天 `/recall` 入口、角色/引用授权和结果回复；删除直接引用 Bot 的让位增强，原主动插话不变。
- [x] 取消本批 history Remove/Clear、removed 事件和改绑清理，不耦合缓存的存在性/TTL。
- [x] 注册 `group.message_recall.v1`，按逻辑群取得只接受消息 ID 的 MessageRecall；模块依赖声明和调用通过测试。
- [x] Runtime 复核当前绑定、全局暂停与取消；改绑/解绑拒绝旧能力，无关热更新可继续使用。连接器可选 Recaller 与 Support 分离。
- [x] OneBot 调 delete_msg，不在宿主加两分钟或 history 存在性限制；不支持/权限/平台失效/超时返回原分类与副作用确定性，不自动重试、不中止连接器。
- [x] 同步配置手册、群管理、架构、功能清单和 specs；明确不新增定时扫描、HTTP 撤回入口、LLM 工具或违规判断。

出口：无 history 记录的消息 ID 仍交连接器执行；只有平台确认的限制才返回相应错误，旧能力不能随配置改绑误操作其他群。真实副作用验收保留工作包 8，不以模拟服务代替账号权限验收。

## 5. 入群审批外部 HTTP 判断（需求 §1.3）

- [x] options/Policy 增加 external_review；严格校验 URL、method、Header 引用、JSON 模板和布尔表达式子集。
- [x] Core 审批接入完整 Event，宿主解析所需凭据并执行固定 5s/64KiB 请求；安全替换占位符，禁止凭据回显及自动重定向。
- [x] 实现按成本升序串行的正则 → 外部 HTTP → 模型短路组合，跳过未启用项、不并发执行，区分 false 与 HTTP unknown；Policy.Equal 比较外部配置内容，保留执行前绑定/策略复核和模型一次预算。
- [x] 覆盖 AND/OR 的调用顺序与短路、引号/换行注入、大整数、缺失凭据及运行时轮换、非法响应/表达式、超限、取消、重定向和策略变化。
- [x] 同步 group-management（含服务契约示例）、configuration、config-index、默认行为、架构与功能清单；唯一 runtime.example.json 列全 external_review，文档解释 OneBot 三项 AND 组合、启停方式、凭据初始化及请求/响应；init 生成文件也列全默认关闭的审批配置及 HTTP 字段，覆盖加载、关闭状态和 JSON 模板验证。

出口：外部判断可单独启用，也可与正则/模型组合；没有配置外部服务时旧行为不变；外部失败不无限等待、不泄密、不多调用模型；只有当前策略仍有效才执行审批。

### 5.1 按引用凭据管理补充

- [x] 管理 API 新增按引用 GET/PUT/DELETE；复用现有 Secret Manager 与运行时生命周期，不返回明文。
- [x] 覆盖认证、含斜杠引用、未知字段/多余 JSON 拒绝、版本递增、密文落盘及删除后状态。
- [x] 同步配置手册、字段索引及外部审批凭据轮换说明；QQ 和连接器换代另批审阅。

## 6. QQ 官方群聊连接器（需求 §1.4）

依赖工作包 2 和 4；工作包 3 已取消。固定采用设计确认后的范围，不同时引入频道、C2C 和两种事件通道。

- [x] 建立独立 native.qqbot 模块和 qqbot Provider，严格 options、绑定、脱敏视图与默认关闭。2026-11-10 按用户要求全局统一名称为 qqbot，包路径、模块/类型、日志、init、示例与文档同步，无旧名别名。
- [x] 为 Provider 增加显式凭据引用声明和仅本实例可见的构建凭据；接入候选凭据替换/删除与连接器生命周期。整组重启，启动或保存失败恢复旧集合/凭据，删除保护；入站等待换代可取消。连接器凭据基础能力已于 `689c1e3` 提交，本批接入 QQ Provider 与 doctor。
- [x] 实现 Token 获取、并发共享刷新、临近过期时下次调用刷新和 Stop 取消；Build 不启动网络或协程。
- [x] 实现业务 Webhook 原始体验签、独立 op=13 校验回调、有界去重/排队、及时 ACK、满队列拒绝和关闭；2026-11-11 纠正挑战强制验签，兼容无签名挑战并约束令牌及时间。
- [x] 映射群消息、可信角色、@Bot/@全体、文本/附件/引用；有界维护引用索引和消息 ID 的对应关系。
- [x] 实现被动文本回复、有效期内的 msg_seq、适配器内部引用索引和群消息撤回；统一失败及副作用语义，能力未实现时明确 unsupported。
- [x] 补充能力目录、doctor、配置索引、唯一 runtime.example.json 中的完整 QQ 字段和接入教程；完整通用示例及 init 显式声明关闭的 QQ 模块与完整 connector options。
- [x] 完成签名、队列、Token、权限、引用、重复发送保护、候选凭据失败/保存失败恢复、删除保护测试；受控 Webhook → Runtime → 模型 → QQ 发送链路通过，不向宿主返回平台 ID。

必要实施补充：旧管理 API 只能管理回答/DM 的固定引用，新凭据缺少运行中轮换入口。新增 Bearer 认证的 `GET/PUT/DELETE /v1/admin/secrets/{ref}`，只查配置状态、写入或删除，无明文读取；未知字段拒绝、落盘加密测试通过。外部审批后续请求读取新值；QQ 凭据换代走上述候选路径。等待连接器换代屏障的入站请求支持上下文取消，避免 Stop 与屏障相互等待。

连接器生命周期已在前批 `689c1e3` 提交并验证；本批 QQ 使用该能力，不重复引入另一套凭据事务。

出口：受控链路通过，权限未知不伪装为可用；OneBot 与 QQ 可以同时绑定不同群；失败不使无关连接器退出。真实账号收消息 → 回答为版本验收必需项。

### 6.5 QQ 协议兼容修复（2026-11-11）

- [x] 第一批：op=13 与业务验签分流，保留 AppID/限体积/时间窗口；有界令牌字符集防止挑战签业务 JSON。测试覆盖无签名/有签名挑战、非法令牌/时间/AppID/体积、业务事件无签名和借用挑战签名的拒绝，以及挑战不改变消息状态。
- [x] 第二批：全量消息在去重前识别 mentions.is_you，仅清除已确认属于本机器人的 @ 标签并重新识别命令；保留其他正文，不从 AppID/正文猜身份。覆盖全量先到、专用 @ 先到和并发重复投递，以及全量明确 @ → Runtime → 回复链路。
- [x] 第三批：兼容无协议头附件 URL 与缺失 MIME 的图片类型；保留 HTTP/HTTPS、查询参数和附件上限，非法地址跳过，明确 MIME 优先。覆盖 URL/类型映射、16 条上限和图片附件 → Runtime → 模型/关闭图片理解提示的受控链路。

第一批现为 `b66a5ab`，第二批已提交 `527a923`。第二批仅涉及 platform/qqbot 内的明确 @ 归一、去重回归与文档；无宿主接口/配置字段变更，缺少 is_you 的事件不添加身份推断或重投后备。第二批独立暂存快照已通过 go build ./...、go vet ./...、go test ./... -count=1 和 go test -race ./internal/... -count=1；真实群聊验收待补。

第三批仅修改 platform/qqbot 的附件归一与测试及对应说明，保留 ForgeBot 原有类型和接口；不下载媒体，不改宿主或配置，不扩展主动发送/出站媒体。真实 QQ 图片与供应商读取能力待验。 第三批独立暂存快照已通过 go build ./...、go vet ./...、go test ./... -count=1 和 go test -race ./internal/... -count=1；未创建提交。

第一批只修改 platform/qqbot 的回调验证及配套测试、设计和用户说明；未改宿主接口、配置字段或部署凭据。真实开放平台回调验证待补。第一批独立暂存快照已通过 go build ./...、go vet ./...、go test ./... -count=1 和 go test -race ./internal/... -count=1。

## 7. Telegram 群聊连接器（需求 §1.5；设计 §8）

依赖既有 Provider、SecretStore、group.message_recall.v1 和 native.welcome；命名、配置和接口遵循 ForgeBot 现有约定，不引入 Telegram 自动入群审批。首批长轮询文本/引用/角色/撤回已实现待审阅，下面按批次记录完成状态；Webhook、图片及欢迎未开始。

### 7.1 Provider、长轮询与文本问答（TG-R1/R2/R3/R4/R6/R8/R9/R10）

- [x] 新增默认关闭的 native.telegram 和 telegram Provider，空模块 options、严格实例配置、凭据声明与脱敏；固定官方 API 主机，Build 不联网，不增加 SDK。补齐 API 响应实际读取限额、读取错误/HTTP/ok 联合判定及 parameters.retry_after/migrate_to_chat_id 分类，禁止截断成功或发送自动重定向。
- [x] 实现 API 分类与 Token URL 脱敏，Start 的 getMe/getChat 检查；chat_id 绑定只接普通群和非论坛超级群，忽略私聊/频道/话题、匿名身份与机器人消息。
- [x] 实现 long_polling、deleteWebhook(false)、显式 allowed_updates、有界队列/去重/offset 确认、可取消退避与 Stop；首个非空探测批次不能丢弃，同实例恢复保留 offset/短期标记，retry_after 可大于 30s，不要求 update_id 连续。此批不接受尚未实现的 webhook mode。
- [x] 映射 UTF-16 实体、明确 @/回复本 Bot、当前文本与同群引用、管理员角色缓存；在原文坐标解析 bot_command 目标、校验代理对边界并逆序清理自身 mention，在进入宿主前过滤指向其他 Bot 的命令。
- [x] 实现纯文本 sendMessage 和 deleteMessage；超长预先拒绝，无发送重试或通用回执；暂不声明图片和欢迎已接通。
- [x] 注册组合根、能力目录、doctor、init 完整关闭配置和唯一 runtime.example.json 中的 Telegram 字段；覆盖首个非空探测批次/停止恢复/非连续 update_id、异常响应不确认、HTTP 非 2xx 但 ok=true、HTTP 200 但 ok=false、retry_after>30s、迁移 ID、OtherBot 命令，以及普通问答/角色故障/409/取消/撤回，回归 OneBot/QQ。

第一批验证（2026-11-11）：独立暂存快照通过 go build ./...、go vet ./...、go test ./... -count=1、go test -race ./internal/... -count=1。配置示例加载、doctor 凭据检查、原首批暂存范围及新增文档链接/空白检查通过；未调用真实 Telegram 或模型服务，未创建实现提交。

### 7.1.1 唯一完整运行时示例（2026-11-11 用户补充）

- [x] 仅保留 configs/runtime.example.json，补全空字段、OneBot 模块默认项与 MCP transport 字段，删除独立平台/组合示例，修正文档和测试引用。
- [x] init 直接复用程序内嵌的唯一示例，保留完整配置和字段顺序，只覆盖 CLI 模型参数；不改变 DefaultRuntime。
- [x] 验证字段覆盖、每个连接器启用后的 options/绑定、init 与唯一示例一致，使用文档中的步骤在内存构造业务配置进行测试，不另建运行时示例文件。

唯一示例验证（2026-11-11）：独立暂存快照已通过 go build ./...、go vet ./...、go test ./... -count=1、go test -race ./internal/... -count=1；字段覆盖、init 完整性/顺序、各连接器启用及无遗留示例引用检查通过。本批仍未创建实现提交。

### 7.2 Webhook 与模式/凭据切换（TG-R2/R9/R10）

- [x] 加入 webhook 配置（`webhook_url/addr/path/secret_ref`）与当前模式专属凭据声明；先占本地端口再 setWebhook，校验 secret header（恒定时间比较）、1MiB 请求体上限和入队后 ACK（满/已停止 503）；不实现 QQ 式挑战。
- [x] 每次 Start 按当前模式登记（长轮询 deleteWebhook、webhook setWebhook，均保留待处理更新）；登记被拒释放端口，结果未知时尽力撤销；Stop 先停本地再有界撤销，远端失败只记诊断。
- [x] 覆盖登记顺序与参数、错/缺认证、重复投递、满队列/非法/超限、端口占用、登记被拒/未知、远端撤销失败仍本地成功并可重启重新登记；模块层覆盖两种模式的凭据声明、URL 端口/HTTPS、认证引用不得与 Token 相同。
- [x] 同步 telegram.md、config-index、唯一示例（webhook 字段完整列出，默认仍为 long_polling）。模式切换走既有连接器换代与恢复，未新增 Runtime 级测试。

### 7.3 图片输入（TG-R4/R5/R10）

- [x] 照片按像素面积选最大且已知不超 5MiB 的尺寸，不假定有序；全部已知超限报错；图片 document 按 MIME 或后缀识别。工作者中 getFile 后按 limit+1 读取，file_path 只允许相对路径，下载后按文件头确认真实 MIME，不写 Media.URL。
- [x] 当前与同群引用图片一并下载；任一失败/超限/类型不明整条停止，不丢图后问答；非图片附件只映射类型和名称。
- [x] 覆盖尺寸乱序、全部超限、文档后缀/非图片、当前+引用、类型不明、实际超限、声明超限不下载、下载失败、路径校验与 Token 不进消息/日志。
- 说明：一条消息最多一张照片与一份文档，连同引用天然不超过 4 张、20MiB，未另设计数；图片理解关闭时仍会下载，由 Core 返回原有提示。

### 7.4 欢迎、换代屏障与收口（TG-R7/R9/R10）

- [x] new_chat_members 中每位真人生成 MemberJoined：身份取成员元素、时间取服务消息 date（缺失不欢迎），事件 ID 含 connector/chat/update_id/user，跳过机器人与自身；服务消息先于发送者过滤处理，不作为空文本问答；能力声明 member_events=true、join_events=false，不实现 Reviewer。
- [x] Runtime 群事件与入站共用可取消的换代屏障（审查修正时完成，见下方交接）。
- [x] OneBot 处理上下文绑定每次 Start 的生命周期，Stop 取消等待者，保留客户端断开后继续处理（审查修正时完成）。
- [x] 覆盖邀请人与新成员身份不同、多成员、机器人/自身、非法时间、未绑定群、重投去重与短期重新入群。
- [x] 同步 telegram.md（图片、欢迎、代理说明）与能力目录描述。

每批出口：gofmt、build、vet、相关测试通过；供提交的独立暂存快照再跑全量普通测试和 internal 竞态测试。代码、配置、用户文档与 specs 同步后才暂存，用户审阅前不推进下一批。Telegram 模块实现了哪些能力就声明哪些，不接受未来 mode 或把未完成的能力标为可用。

## 8. 版本验收与收尾

- [ ] 真实沉淀：管理员引用及连续答复，静默后经定时批次生成可检索 Markdown；验证原问题已滑出时使用引用原文，已形成会话不随 history 滑动失效；记录输出质量与失败结果，敏感正文不入仓库。
- [ ] 真实 OneBot：通过 capability 按 ID 调 delete_msg，验证管理员账号的实际撤回权限/时限；无权限和平台拒绝不停止运行，不增加聊天命令作为验收入口。
- [ ] 外部审批：使用可控服务验证同意、拒绝、unknown、AND/OR 和真实审批副作用；验证凭据替换。
- [ ] 真实 QQ：有效 Webhook、@Bot、模型回答成功发送、引用映射；具备权限时验证 capability 按 ID 撤回，其他能力按账号结果记录。
- [ ] 真实 Telegram：普通群/非论坛超级群，两种接收分别验证 @/回复/命令、照片和图片文件、欢迎/通知、撤回权限、超长拒绝，以及切换模式/替换凭据后的恢复；核对隐私模式影响，未启用的私聊/频道/话题不进入群会话。不执行自动入群审批。
- [x] Telegram 实施后重新执行所有代码 gofmt、`go build ./...`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./internal/... -count=1`；2026-11-11 审查修正与 7.2–7.4 完成后全部通过（Go 1.27.3/windows-amd64）。
- [ ] Telegram 实施后刷新 specs、配置索引、功能清单及双语 README 的最终能力边界，逐项区分实现/自动测试/真实验收；新文档链接和空白检查通过。

此前实现的自动验证（2026-11-10，Go 1.27.3/windows-amd64）：上述四项检查通过。本批 QQ 独立暂存快照已通过 go build ./...、go vet ./...、go test ./... -count=1 和 go test -race ./internal/... -count=1，未调用真实平台或模型服务。一次并发全量验证中，未改动的 `notification.TestDispatcherSendsToEveryTargetAndContinuesAfterFailure` 在 1 秒等待处超时，无竞态报告；该包随后 `go test -race ./internal/notification -count=10` 通过，单独重跑完整 `go test -race ./internal/... -count=1` 全通过，未为消除超时修改通知代码或测试。

真实条件不足就保留未勾选及阻碍原因，不以单元测试代替真实验收，不自动修改部署配置。提交或发行需用户另行明确要求。

## 当前交接

### 2026-11-11 审查与修正

接手审查已实施工作包，修正如下，均未提交：

- 外部入群审批：HTTP 故障、非 2xx、超限、缺凭据、响应不符表达式及组合中的模型故障，原实现落成 Approve:false 并真实拒绝申请人。改为未决（undecided，不操作）：AND 遇未决即停，OR 无明确 true 且有未决时不操作；明确 false 仍拒绝。替换后请求体另限 256KiB。§5 原“区分 false 与 unknown”勾选此前仅日志层面成立。
- QQ：Token 地址改为 `bots.qq.com/app/getAppAccessToken`，OpenAPI 改为 `api.sgroup.qq.com`（原 `api.bot.qq.com` 不可用，连接器无法启动）；新增 `sandbox` 选项。Stop 超时也释放句柄；引用失效 4xx 去引用重发一次；时间戳异常取接收时间；`X-Bot-Appid` 缺失时不拒绝；去重满额淘汰最旧标记；`expires_in` 缺失按 7200s。
- Telegram：非论坛超级群的回复带 message_thread_id，原实现误判为话题而丢弃回复/引用，改为只看 is_topic_message；Stop 超时也释放句柄；401/403 只结束读取，不再取消发送；409 上报后退避重试；角色查询不再持锁访问网络；保留平台错误描述并细分 not found/权限；非 JSON 错误正文按 HTTP 状态分类。
- 沉淀：history 增加 QuotedFromBot/TextTruncated/QuotedTruncated；引用 Bot 自身消息（连接器按平台发送者标记）、被截断的问题/引用/回答均不采纳；`message_ttl` 须 ≥ `quiet_window + scan_interval`；失败会话排在新会话之后。删除遗留空目录。
- Runtime：保存失败后旧连接器恢复失败不再被吞，合并错误并置 degraded；换代屏障改为释放时唤醒，群事件同样经过屏障；OneBot 处理上下文绑定每次启动周期，Stop 时取消（工作包 7.4 的两项前置随之完成）。
- 未改：Telegram 回复仍为 allow_sending_without_reply=false（设计决定，参考项目用 true），留待用户确认。

验证（2026-11-11）：go build ./...、go vet ./...、go test ./... -count=1、go test -race ./internal/... -count=1 全部通过。

### 此前交接

2026-11-11：Telegram 规划已提交 `10c5b2f`。本批仅实现工作包 7.1：Provider、Bot Token 凭据声明、绑定检查、长轮询及逐项确认、文本/UTF-16/引用/角色、纯文本投递和宿主撤回；组合根、能力目录、doctor、init 与示例/用户文档同步。图片及引用媒体明确 unsupported，欢迎服务消息忽略，Webhook 不接受。未修改现有宿主接口，不新增通用回执，不调用真实账号。本批实现只暂存待用户审阅，不创建提交；后三批及真实验收未开始。

用户补充原始讨论后，沉淀方案已修正：仅订阅新消息、模块保留已形成会话及处理标记、定时批处理；先查 history，再用适配器引用原文；不依赖 Evicted，不要求处理时原记录仍在容器。一并撤回“一次删除五个配置键”的建议。实现保留已有调度和上限字段，把约束对象缩到已形成会话。

其余实施选择：外部表达式采用有限子集及 JSON 结构内安全替换；QQ 首期采用 Webhook、群聊和被动文本回复，支持当前公开的群撤回接口。原始讨论澄清后，用户已另行授权实施。

代码入口：`internal/modules/knowledgereflection/{sessions,worker}.go`、`internal/runtime/recall.go`、`internal/group/recall.go`、`internal/group/external_review.go`、`internal/modules/qqbot`、`internal/platform/qqbot`。QQ 教程与验收步骤见 `docs/qqbot.md`；受控测试不替代真实平台验收。
