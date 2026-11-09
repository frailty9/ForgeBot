# v0.1.0 任务顺序与实施入口

日期：2026-11-03。依据：[需求](requirements.md)、[实施设计](design.md)、[能力清单](../../docs/feature-inventory.md)。

状态：用户已于 2026-11-03 授权连续实施；当前在 feat/v0.1.0。工作包 0–10 的代码已交付，工作包 11 的文档、中文边界注释、许可证与自动验收已收口。2026-11-09 入群审批、欢迎语、init 到启动教程、范围提示词与工具检索、专用 DM、管理员接答让位已通过真实验收；自动沉淀的真实运营效果未验收，用户于 2026-11-09 决定它不作为 v0.1.0 的发版条件，留待其重构后验收。同日已合并 master 并打 v0.1.0 标签；推送与对外发布另行决定。

## 顺序与依赖

```text
0 基线与目录
  → 1 统一结果 → 2 单后端/领域存储
    → 3 输入与默认行为 → 4 有界 Loop → 5 短期会话
      → 6 共享决策/范围/检索 → 7 管理员让位
        → 8 群事件/操作 → 9 欢迎语
          → 10 能力目录/init/doctor → 11 交付验收
```

范围、权限、预算与副作用确定性先于智能群操作；先把一条默认问答路径做完整，再加后续能力。中文注释、配置例子、回归与交付记录随工作包更新，最后只做整体验证与缺口收口。

## 0. 准备代码基线与目录记录

- [x] 核对 master、现有规格改动和适用规则，创建 feat/v0.1.0 分支；不重写历史、不改动个人 .claude 文件。
- [x] 将能力清单、版本需求、设计/任务与入口更新单独形成文档提交，确保需求可追溯。提交：955ea15。
- [x] 更新 gitignore 的本地配置/数据/缓存路径，明确 docs 与 specs 都跟踪；校验共享规则与示例不被误忽略。
- [x] 执行一次普通测试/vet 基线；2026-11-03 go test ./...、go vet ./... 通过。

验收：基线明确、个人数据保留、后续代码在独立分支，规格文件可找到。

关联：R14。建议提交：docs: 确认v0.1.0实施范围与任务顺序；chore: 整理工程目录与本地忽略规则。

## 1. 统一 Core 结果语义

- [x] 定义稳定错误分类、领域原因与副作用确定性，保留 errors.Is/As 与错误链。
- [x] 收口现有消息/工具/配置/LLM/投递边界的映射；正常忽略、重复、空结果不算系统故障。未可靠映射的外部错误保留 unknown。
- [x] HTTP/工具公开文案与诊断信息分开，避免通过错误文本分支；后续新增 CLI 沿用相同分类。
- [x] 验证不支持、无权限、配置冲突、取消、超时且执行未知，不引入自动重试。2026-11-03 go test ./...、go vet ./... 通过。

验收：新旧主要 Core 操作均可被调用方按类型或稳定 code 判断；副作用超时不谎报未执行。

关联：R12、ARCH-P13。建议提交：feat: 统一核心操作结果与错误分类。

## 2. 单后端选择、数据结构与领域仓储

- [x] 梳理 settings/secret/运营数据边界，按实际消费者拆读写/历史/凭据等接口，由唯一 Backend 组合提供。群申请仓储在工作包 8 按真实领域加入。
- [x] 加静态单选配置，仅交付 file，实现稳定 ID、schema/revision、条件更新与领域原子提交。
- [x] 配置加载、更新、历史/回滚与凭据暂存/激活改走同一选中后端，禁止隐藏双写和失败自动切后端。启动 Loader 的文件解析保持独立，App 构建时以后端 Current 为准。
- [x] 实现文件写失败恢复、版本冲突和快照隔离契约测试；配置保留原文档备份，凭据 v1 升级前保留原密文文件。补充中断替换恢复、有效备份保留和过期凭据候选身份测试。

验收：一个实例只有一个持久化后端，领域接口不依赖文件/SQL/NoSQL 语法；配置与凭据仍可更新恢复。

关联：R13、APP-P06、ARCH-P05、ARCH-14/15。建议提交分开为存储契约与文件实现迁移。

## 3. 完善输入与默认行为，移除 /search

- [x] 回答 LLM 成为正常启动必需，删除无模型正式回显；初始化与诊断配置加载保持可用，正式 App 使用 RequireLLM；无认证模型必须显式 authentication=none。
- [x] 增加 image_understanding 开关与不支持图片的确定性回复，覆盖当前/引用、纯图片/混合消息及未 @ 保持沉默。普通定向测试、全量 -race 与 vet 通过。
- [x] OneBot 适配器将可靠表情描述/转义转为文本；未知表情不臆造，保留空值和媒体防护。定向测试与全量 -race、vet 通过。
- [x] 删除 /search 注册、直接检索问答路径与 completer 专用依赖；保留检索 Tool、索引重载和必要命令能力。通用命令注册仍由扩展/Hook测试验证，不因一个业务命令移除而删除。
- [x] 同步修改配置例子与行为测试，确保每个提交可独立编译和运行。

验收：有模型才能正式运行；图片关闭无模型调用；表情语义尽量保留；知识能力只有统一工具调用链。

关联：R1–R5、BOT-02/03/05/07/13。建议各项独立 fix/feat/refactor 提交。

## 4. 实现有界 Agent Loop

- [x] 抽取规范化的多轮回答执行器，串行工具、完整调用 ID 关联、最终文本与中间内容分离。
- [x] 实现轮数、单轮/总工具数、总时限、单结果/累计上下文上限，沿用白名单与每次 Hook。顶层 agent 配置热更新，单次模型时限和父预算同时生效。
- [x] 统一预算耗尽、工具失败、空响应、模型失败、取消终态，检索预回复最多一次；空最终文本沿用回复抑制，超大工具结果以完整错误信封回传。
- [x] 验证先查列表再按结果查详情的依赖调用、模型反复调用、工具超限、取消与旧 generation 在途执行。全量 -race/vet 通过，补充边界测试后 agent/bot/llm/runtime -race 通过。

验收：连续依据收集可完成，任意路径均有界，不丢调用 ID、不输出半成品或重复预回复。

关联：R6、APP-P01、ARCH-P01。建议提交：feat: 实现有界连续工具调用。

## 5. 实现短期问答上下文

- [x] 实现按群+用户隔离的进程内会话服务、TTL/容量/字节上限与有界顺序队列。
- [x] 与 Loop 对接，只提交已确认发送成功的必要问答及实际发送文本；工具结果、决策与群聊旁观内容不写入私人记忆，图片二进制不保留。
- [x] 热更新中保留进程级服务所有权与有效上下文；发布后裁剪新容量，禁用清空并使旧租约不可重新填充，进程重启清空。
- [x] 验证连续追问、跨用户/群不串话、排队/取消、过期、容量淘汰、发送失败与配置更新。全量 -race/vet 通过；原 generation 并发测试改为不同用户，保留跨用户并行验证。

验收：Bot 能在短期内接续问答，记忆不扩大触发范围、不落盘完整聊天记录。

关联：R7、APP-P02、BOT-16。建议提交：feat: 增加有界短期问答上下文。

## 6. 共享 Decision 服务、布尔决策与业务范围

- [x] 定义任务 ID/版本与小 Schema 目录、受限 Decision capability、unknown 及归一化结果。
- [x] 实现独立 LLM 决策适配，锁定共享预算和无 DM 正常路径；确定性输入检查仍在模型前。
- [x] 核对 Jev 官方 System One/Choice 协议并以受控 HTTP 请求验证映射；不承诺任意 Schema 原生支持，未进行真实远程鉴权验收。
- [x] 实现统一 DecideBool(ctx, rule, state) (bool, error)，有 DM 用 DM、未配置/关闭用 LLM，每次最多一次模型请求；失败直接返回 error，记录来源与原因。DM Key 管理只写且热替换。
- [x] ~~完成部署范围分类~~：2026-11-05 按用户决定改为范围写在 bot.system_prompt，@ 不再预分类；主动插话在模块内保留一次布尔决策，故障沉默。
- [x] ~~决策引导检索~~：2026-11-05 删除 `answer_policy.evidence` 与 Loop Preparation，检索交回答模型选择。
- [x] 群审核通过工作包 8 消费同一 Decision 接口；无 DM/DM故障/未知/无效与重复JSON字段/预算/域外拒答/无关检索已有受控测试。真实供应商/群事件证据保留发行验收待办。

验收：共享入口可被多个内置功能使用；DM 是可选加速/专业判断实现，不是基础功能开关。

关联：R4/R9/R16、BOT-20/22、OPS-11/12、ARCH-P14/15。建议拆服务、LLM decision、DM adapter、范围策略四组提交。

## 7. 实现管理员让位与主动插话抑制

- [x] 归一化可信平台角色及配置身份，保留来源，不扩大宿主管理权限。
- [x] 群级临时让位：可信引用接答与可选语义判断、默认 5m、刷新/过期/手动恢复；管理群 proactive 命令支持 yield/resume。
- [x] 清除待插话项，发送前复查让位版本；不在恢复后补发，不取消明确 @ 的正常流程。
- [x] 验证同条引用+@、普通管理员闲聊、让位期间请求、已生成主动回复及跨群/热更新状态隔离；定向 -race 通过，最终全量验证见后续记录。

验收：管理员接答时停止主动插话，明确 @ 仍可获得符合业务范围的回复。

关联：R8、APP-P03、BOT-10/11/12、ARCH-P12。建议提交：feat: 增加群级主动插话让位策略。

## 8. 群事件与可选群管理操作

- [x] 定义入群申请/成员入群事件、稳定引用、绑定与去重；事件不能混入聊天问答文本。
- [x] 定义审核小接口、支持/权限查询与结果；不支持者不实现空方法，权限未知不伪装可用。公告与撤回接口 2026-11-05 删除。
- [x] 入群审批独立实现正则与布尔模型 AND/OR 短路，不持久化审批记录，不依赖任何持久仓储，不提供人工审批 API（欢迎语原先使用的运营仓储已于 2026-11-08 移除）。仅内存事件键防重，执行未知不重放，拒绝理由不支持时忽略。
- [x] 核对 OneBot11 与 SnowLuma 官方源码并实现审核，支持矩阵见 docs/group-management.md。公告（非标准扩展）与撤回实现 2026-11-05 删除。
- [x] 2026-11-09 真实 OneBot 账户权限与事件验收通过；已有受控 HTTP/仓储/API 测试覆盖重复申请/动作、跨群、冲突、故障未知与重启恢复，不能替代真实验收。

验收：有实际可用授权入口与真实连接器支持，不只是接口声明；未支持的连接器仍正常接入聊天。

关联：R10、APP-P09、BOT-17/18/19、ARCH-P11/12/13。建议事件契约、操作路由、OneBot/API 各独立提交。

## 9. 新人欢迎语

- [x] 内置 native.welcome 消费 MemberJoined，按群开关与有限变量模板插值，经模块 GroupEvents 注册返回受限回复意图，由宿主投递。
- [x] 收到入群事件即发送，不去重、不落盘，只过滤 Bot 自己/历史快照（2026-11-08 简化）；OneBot 文本段发送避免 CQ 字面量注入，发送错误归一化且不自动补发。
- [x] 受控测试已覆盖模板、关闭/历史/自身、全局暂停与管理员让位；全量 -race/vet 通过。2026-11-09 真实成员入群事件到发送（含 @ 新成员）验收通过。

验收：新成员实际入群后收到一次正确欢迎，不把审批通过当作入群事件。

关联：R15、APP-P10、BOT-21。建议提交：feat: 增加群成员入群欢迎语。

## 10. 能力目录、init 与 doctor

- [x] 复用各子系统状态建立只读能力投影，区分启用/配置/加载/健康/权限未知，脱敏输出。Skill 目录空内容不伪装已加载。
- [x] 提供 GET /v1/admin/capabilities 与 CLI capabilities，同一数据来源，包含当前选定的 DM 或 LLM 配置状态，不探测网络。
- [x] init 生成最小 YAML/JSON/凭据引用、本地主密钥与忽略规则；模型/图片/匿名声明通过参数填写，重复执行或现有凭据时拒绝覆盖。
- [x] doctor 提供原生模块/协议参数/Store路径/凭据/端口诊断；只读凭据检查不恢复文件，模型及 MCP/stdio 探测须显式选择，单项15s/总30s。
- [x] 自动测试已覆盖空目录初始化→凭据→本地受控模型探测→App构建/关闭及诊断边界，全量 -race/vet 通过。2026-11-09 真实 OneBot/LLM 联合教程验收通过。

验收：部署者无需理解内部 Hook/generation 就能初始化、确认可用能力和定位错误。

关联：R11、APP-P06、OPS-08/10/11。建议目录投影、CLI 向导与诊断分别提交。

## 11. 发布前验收与文档收口

- [x] 完成核心中文边界注释、README_ZH.MD/README_EN.MD、配置/目录/故障/范围与扩展说明；双语启动示例与功能范围一致。
- [x] 用户确认 Apache-2.0 后加入官方 LICENSE 和实际解析依赖的原许可证副本/归属记录；gitignore 验证共享文档保留、本地配置及备份/凭据不进提交。
- [x] 按 R1–R16 交付受控行为证据与 docs/default-behavior.md，真实服务支持和未实现范围单列，不虚构覆盖率或性能收益。
- [x] 自动验证已覆盖无 DM、域外拒答、图片关闭、工具预算、投递未知、存储恢复与初始化流程；最终命令结果见下方收尾记录。
- [ ] 2026-11-09 已通过真实验收：入群审批、欢迎语（含 @）、init 到启动的教程、目标回答模型对范围提示词与工具检索的遵守、专用 DM、管理员接答让位（含事后纠正与未引用直接接手，基于核心近期消息容器）。仍待真实群验收：自动沉淀的运营效果（需真实群消息窗口）；已决定不作为 v0.1.0 发版条件，所以本项保持未勾选，重构知识沉淀后再验收。
- [x] 按工作包维护独立提交历史与交接；实施期间不自动合并 master、创建版本标签或向外发布；2026-11-09 用户明确决定合并 master 并打 v0.1.0 标签，推送与对外发布仍另行决定。

验收：可构建的 Bot、有可复制启动和故障处理流程，所有已选定功能有验收证据。SQL/NoSQL/FPP/WebUI 等仍明确延后。

关联：R14 与全部功能需求。建议提交：docs: 完成v0.1.0交付说明与验证记录。

## 下一步：真实环境验收

从 docs/default-behavior.md 和 docs/group-management.md 执行真实环境验收，先确认专用测试群与入群申请对象，不对普通运营群自动发布测试公告或执行审批。功能代码无待推进工作包；真实验收通过后再由用户决定合并和发行。

## 2026-11-03 本轮收尾交接

- 用户已确认 Apache-2.0；LICENSE 取官方全文。THIRD_PARTY_NOTICES.md 保留实际应用依赖和 Go runtime 的原许可文本。
- 主要提交：4067507 模型/图片；663abb8 工具检索；2b1c552 表情；fea5232 Loop；1d15065 记忆；d12e5da/d375244 决策/范围；d38b492 让位；5d28fa2 群操作；8023473 欢迎；a9af15b CLI/目录。
- 审计修正：4d472de 统一决策/回答历史快照与备份忽略；f8a0263 群事件能力声明；272fa92 HTTP 默认100s覆盖默认90s Agent预算。
- 最终自动验证通过：go test ./... -count=1 -timeout 45s；go test -race ./... -count=1 -timeout 60s；go vet ./...；go build ./...。本分支修改的 99 个 Go 文件格式检查通过，21 份 Markdown 的 227 个本地链接均存在；共享/私人文件忽略检查通过。
- 新增仓库示例加载回归发现并修复默认 support 群绑定禁用 qq-main 的既有问题（a214c79）；默认示例只绑定 Echo，不再指定不存在的 skills 目录。
- 所有模型、MCP 和群操作自动测试使用本地受控服务或测试子进程；未调用用户真实付费模型、真实 QQ 群操作，也未改写部署配置或凭据。
- 未完成：真实服务验收、合并和正式发布。FPP/WebUI/SQL/NoSQL/Telegram/公开SDK仍明确延后，不自动进入下一版本。

## 2026-11-03 恢复：竞态验证环境已修复

编译器已更换为较新的 MinGW-w64 gcc（16.2）。此前五包定向 -race 与 go test -race ./... 均通过，0xc0000139 不再出现。按用户要求恢复持续实施，保留下方历史暂停记录供追溯。

## 2026-11-03 暂停交接：竞态测试无法运行

- 已提交：955ea15 计划；aed46e4 忽略范围和基线；88fb15a 统一结果；2edc132 仓储接口；2d262a6 单后端与数据恢复。另有 CLI 凭据初始化入口收口提交，见 Git 历史。
- 普通验证：go test ./...、go vet ./... 通过；CLI 最后一次独立调整后 go test ./cmd/forgebot、go vet ./cmd/forgebot 通过。
- 失败命令：go test -race ./internal/runtime ./internal/settings ./internal/secret ./internal/persistence，四包均 exit status 0xc0000139，没有 Go 竞态报告。
- 对照：未改动的 internal/admission 执行 go test -race ./internal/admission，同样 exit status 0xc0000139；不能据此认定本次业务代码产生竞态，也不能认定竞态验证通过。
- 环境：go1.25.5 windows/amd64；CGO_ENABLED=1；CC=gcc（当时为较旧的 MinGW gcc 8.1.0）。尚未定位具体加载/工具链原因，未改动系统工具链或 PATH。
- 按用户“遇到错误可暂停”的授权暂停，不继续叠加工作包 3。没有合并 master、运行正式 Bot、访问真实 LLM/OneBot 或改写部署配置及凭据。
- 恢复入口：先核对当前 Git 状态；处理竞态环境后重跑上面五包，或由用户明确接受将竞态验证留给可用环境，再从工作包 3 继续。保留现有正常测试证据，不重演已完成提交。

## 2026-11-05 用户审阅后的范围修正

用户审阅 v0.1.0 后决定（理由见 requirements.md“2026-11-05 用户决定”）：

- @ 消息不再做范围决策；删除整个 `answer_policy`（含 `scope`、`allow_daily`、`refusal_text`、`clarification_text`），回答范围写在 `bot.system_prompt`。
- 删除决策引导预检索：`answer_policy.evidence`、`bot.Policy.Evidence*`、`agent.Loop.Preparation`。
- 删除 `bot.answer` 任务与 Engine 的决策依赖；主动插话决策移入 `native.proactive_reply`，用 `DecideBool` 加模块 `scope` 选项判断；回答模型对主动回复另附“范围外输出空内容”的宿主提示。
- 删除群公告与消息撤回：`group.Announcer/Recaller`、`Support.Announcement/Recall`、`Policy.Announcements/Recall`、`Service.Perform` 与 `ActionRequest`、OneBot `_send_group_notice`/`delete_msg` 实现、`POST /v1/admin/groups/announcements|recall`。运营仓储曾保留给欢迎语，后于 2026-11-08 一并移除。
- 运行配置严格解析，旧配置若仍含上述字段会加载失败，需手动删除。
- 验证（2026-11-05）：go build ./...、go vet ./...、go test ./... -count=1、go test -race ./... -count=1 均通过；修改的 Go 文件格式检查通过，docs/specs 本地链接均存在。未提交。


## 2026-11-05 主密钥可写入 YAML

用户指出主密钥只能用环境变量的要求没有依据：`admin_token` 同为启动期敏感项却允许写在 YAML，`secret_store_path` 也能把密文放到配置目录之外。调整为：

- 静态 YAML 增加 `secret_key`，`FORGEBOT_SECRET_KEY` 优先；与 `admin_token` 一样 `json:"-"`，配置查询与 doctor 不输出。
- 进程入口、`-init-api-key` 与 doctor 统一读取 `cfg.Static.SecretKey`，不再直接读环境变量。
- `init` 把主密钥写进 `forgebot.local.yaml`，不再生成 `master.local.key`，也不再提示 PowerShell 加载步骤。
- 验证：go build ./...、go vet ./...、go test ./... -count=1、go test -race ./... -count=1 通过。

## 2026-11-06 日志配置迁到启动 YAML

用户认为日志属于运维层面的进程配置，不应放在可热更新的运行配置里。调整为：

- `log`（`level`、`format`）从运行时 JSON 移到启动 YAML，`FORGEBOT_LOG_LEVEL` 覆盖等级；运行时 JSON 不再接受 `log`。
- 新增 `log.file`：为空时输出到 stdout，填写后只追加写入该文件，相对路径以 YAML 目录为基准，不内置轮转。
- 删除日志热更新：`logging.Controller`、`Builder.ApplyLog` 与 `ActionUpdateLog`。`app.New` 不再接收日志控制器。
- 旧运行配置若仍含 `log` 会加载失败，需手动移到 YAML。
- 验证：go build ./...、go vet ./...、go test ./... -count=1、go test -race ./... -count=1 通过。

## 2026-11-06 日志文件进程内轮转

外部轮转在 Windows 上不可用（打开中的文件不能被改名或删除），改为进程内轮转：

- 引入 `gopkg.in/natefinch/lumberjack.v2` v2.2.1（MIT，无第三方依赖），先关闭再改名，Windows 与 Linux 都可用。
- `log` 新增 `max_size_mb`（默认 10）、`rotate_after_days`（默认 7，0 为只按大小）、`max_age_days`（默认 60，0 为永久）、`compress`（默认 false）；只能与 `log.file` 一起使用。
- 按周期轮转约每小时检查一次，空文件不轮转；当前文件开始时间取最新备份时间，重启后取第一条日志时间。
- 日志写文件时，启动失败另写一行到 stderr。
- 验证：go build ./...、go vet ./...、go test ./... -count=1、go test -race ./... -count=1 通过；按大小轮转在 Windows 本机实测通过。

## 2026-11-07 日志控制台与文件分区

- `log` 改为 `console` / `file` 两段，各自设置 `enabled`、`level`、`format`；轮转字段移到 `log.file` 下。默认控制台开（info/text）、文件关（info/json），不写 `log` 时行为不变。
- 两路至少开启一路；文件开启时必须填写 `path`；文件关闭时保留路径与轮转设置。等级不区分大小写。`FORGEBOT_LOG_LEVEL` 同时覆盖两路等级。
- 两路都开时使用 Go 1.26 起提供的 `slog.NewMultiHandler`，各路按自己的等级过滤；只开一路时直接使用该 handler。
- 控制台关闭时，启动失败另写一行到 stderr；控制台开启时不再重复。init 生成的 YAML 带两段配置，忽略规则增加 `/logs/`。
- 旧的平铺写法（`log.level`、`log.file: 路径` 等）不再接受，需手动迁移。
- 验证：go build ./...、go vet ./...、go test ./... -count=1、go test -race ./... -count=1 通过。

## 2026-11-08 入群审批开关合并

`review_enabled` 原本控制“记录申请等待人工审批”，Bot 侧人工审批移除后单独开启已不起作用。合并为一个开关：

- 删除 `group.Policy.ReviewEnabled`，只保留 `auto_review`：开启即按正则/模型自动审批，关闭则不处理入群申请。
- 开启 `auto_review` 时仍要求至少一种判断（`review_regex` 或模型）。
- 旧配置残留 `review_enabled` 会因严格解析报未知字段，需手动删除。
- 验证：go build ./...、go vet ./...、go test ./... -count=1、go test -race ./... -count=1 通过。

## 2026-11-08 入群审批迁入模块

入群审批是唯一不在 `modules` 下的群级功能，配置方式与欢迎、主动插话不一致。调整为：

- 新增 `native.join_review` 模块：模块启用且群出现在 `options.groups` 中即开启自动审批，删除 `auto_review` 开关。字段去掉 `review_` 前缀（`regex`、`model_enabled`、`rule`、`combine`、`reject_reason`）。
- 模块只提供各群规则（新注册类型 `join_review`，每代最多一个）；判断、去重、执行前复核与连接器调用仍由宿主 `group.Service` 完成，去重状态跨热更新保留。
- 删除顶层 `group_management` 及 `group.Policy.AutoReview`；残留的 `group_management` 会被严格解析拒绝。
- 统一校验：`native.join_review`、`native.welcome`、`native.proactive_reply` 的 `groups` 与 `native.knowledge_reflection` 的 `conversation_ids` 必须引用已绑定的群，关闭的模块同样检查。
- 验证：go build ./...、go vet ./...、go test ./... -count=1、go test -race ./... -count=1 通过。

## 2026-11-08 明确“一个部署对应一个业务”，入群审批规则改为模块级

用户确认 v0.1.0 定位为一个部署服务一个业务：多个平台的多个群共用业务范围、知识库、回答模型与模块规则，各群只决定开启哪些功能。多业务需求出现较多时再考虑。据此：

- `native.join_review` 的规则字段（`regex`、`model_enabled`、`rule`、`combine`、`reject_reason`）移到模块 `options` 顶层，所有开启的群共用；`groups.<群ID>` 只保留 `enabled`。
- `groups` 中列了群（含关闭的群）时规则必须可用；关闭的群同样检查是否已绑定。
- 双语 README 与 configuration.md 写明部署定位；群管理文档改为模块级规则写法。
- 验证：go build ./...、go vet ./...、go test ./... -count=1、go test -race ./... -count=1 通过。

## 2026-11-08 管理者收窄为唯一运维群

`administration`（可信的人）与 `operations`（运维群）分开放置不易理解，且运维命令只按群授权、不检查执行人。用户决定管理者最多一个，多人管理通过群聊进行：

- 删除 `administration` 与 `operations`，新增可选的顶层 `operator`（单个对象）：`conversation_id`、`notifications`、`commands`。运维群内所有成员都可执行允许的命令，文档要求该群只包含可信管理人员。
- 删除 `internal/administration` 与 `InboundMessage.Administrator`；主动插话让位与知识沉淀的可信答疑人统一以平台群主/群管理员身份为准。
- `/proactive yield|resume [群ID]` 可指定目标业务群，不写时作用于当前群。
- OneBot 适配器目前只处理群消息（协议本身支持私聊），因此管理者暂不支持单个用户；私聊留待后续版本单独规划。
- 验证：go build ./...、go vet ./...、go test ./... -count=1、go test -race ./... -count=1 通过。

## 2026-11-08 入群申请的 DEBUG 排障日志

真实验收中一条入群申请被拒绝，需要看到收到的原始内容才能判断是规则问题还是申请内容格式问题（例如 QQ 的验证问题可能把 comment 拼成“问题：…答案：…”）：

- 适配器收到入群/入群通知事件时，DEBUG 输出 post_type、request_type、sub_type、群号、用户、映射后的逻辑群，以及 comment 原文（最多 512 字符）；审批句柄 flag 始终不输出。被忽略的事件也带上这些字段，便于确认事件是否到达。
- 审批判断时，DEBUG 输出正则、申请原文、是否匹配、组合方式与是否短路；模型判断另输出结果。INFO 级别仍不输出申请内容。
- 验证：go build ./...、go vet ./...、go test ./... -count=1、go test -race ./... -count=1 通过。

真实验收的日志证实：设置了验证问题的群，OneBot 事件的 comment 是“问题：xxx\n答案：yyy”，要求恰好 10 位的规则因此整体不匹配。处理：

- OneBot 适配器取出第一处“\n答案：”之后的答案作为入群事件的申请内容；没有这个结构时原样返回。以第一处为界，申请人无法在答案里伪造“答案：”改变匹配内容。验证问题不参与匹配。
- DEBUG 日志同时输出原始 comment 与提取后的 application。
- 验证：go build ./...、go vet ./...、go test ./... -count=1、go test -race ./... -count=1 通过。

## 2026-11-08 欢迎语简化：移除群操作仓储

真实验收中欢迎语不触发，原因是未配置 `group_store_path`（欢迎语的防重记录依赖它，缺失时静默跳过）。需求本身只是“有人入群就发欢迎语”，仓储属于过度设计。处理：

- 欢迎语收到成员入群事件即发送；只忽略 Bot 自己入群、历史快照、非入群事件和空模板。不去重、不判断事件新旧、不落盘。
- 删除 `group_store_path`、`group.Store/Record/Status`、`group/filestore`、`Backend.GroupOperations`、`GET /v1/admin/groups/operations` 和 doctor 的 `group_store` 检查。旧配置里残留 `group_store_path` 会在严格解析时报未知字段，需要手动删除这一行。
- 入群审批原本就不依赖该仓储，行为不变。
- 验证：go build ./...、go vet ./...、go test ./... -count=1、go test -race ./... -count=1 通过。

## 2026-11-08 欢迎语 {user} 取到用户 ID

真实验收中欢迎语的 `{user}` 显示成了用户 ID。原因：OneBot 的 `group_increase` 通知只带 `user_id`，适配器没有昵称来源，模块只能退回 ID。处理：

- OneBot 适配器收到非 Bot 自身的入群事件后，调用 `get_group_member_info` 查询新成员，优先取群名片，其次昵称，填入事件的 `UserName`。
- 查询带 3 秒超时；失败或超时只记 DEBUG 日志，`{user}` 退回用户 ID，欢迎语照常发送。
- 验证：go build ./...、go vet ./...、go test ./... -count=1 通过；相关包 -race 通过。

## 2026-11-08 欢迎语支持消息段（@ 新成员）

欢迎内容原先只能是固定文本，无法 @ 新成员。OneBot 本身支持消息段组合，因此把配置项从 `text` 改为 `message`，可写成字符串或消息段数组。设计取舍：消息段由宿主定义成平台无关的小集合，连接器负责翻译；不把 OneBot 的段原样透传，避免欢迎模块和 QQ 绑死，也让配置能在加载时校验。

- 新增 `messaging.Segment`（`text`、`at`）与 `OutboundMessage.Segments`；`group.EventReply` 同时带纯文本形式和消息段，宿主投递前复核数量（最多 16 个）、类型和 `at` 目标（只能是纯数字，拒绝 `all`）。
- `native.welcome` 的 `text` 字段移除，改为 `message`；`at` 段的 `data.qq`（沿用 OneBot 写法）可写 `{user_id}`（@ 新成员）或固定数字 ID。旧配置里的 `text` 会因严格解析报未知字段，需要手动改名。
- OneBot 把 `at` 翻译为 `{"type":"at","data":{"qq":…}}`，适配器发送前再次校验；不支持 @ 的连接器（如本地 Echo）发送纯文本形式，`@` 段退化为 `@昵称`。
- 新人昵称只作为文本出现，不会被解析成控制段。
- 验证：go build ./...、go vet ./...、go test ./... -count=1 通过；-race 通过。

## 2026-11-08 欢迎语消息段改为原样透传

OneBot 支持的消息段远多于 `text`/`at`，宿主自己维护一套受限的段集合会不断落后于平台。调整为：`message` 数组里的段不再检查内部语法，原样透传给连接器，形状沿用 OneBot 的 `{"type":…,"data":{…}}`。

- `messaging.Segment` 改为 `Type` + `Data map[string]any`，删除 `SegmentText/SegmentAt` 与 `ValidUserID`；welcome 不再校验段类型和 data，只检查数组非空、最多 64 段、每段有 `type`、顶层键无拼写错误。
- 宿主 `validReply` 只复核结构边界（有内容、数量有界、每段有 `type`），允许只有消息段、没有纯文本（如纯图片）。
- 占位符 `{user}`、`{user_id}`、`{conversation_id}` 在所有字符串值里替换，数字和嵌套对象原样保留；替换发生在 JSON 解码之后，并深拷贝，昵称里的引号、花括号、CQ 字面量不会改变消息段结构，也不污染共享配置。数字用 `json.Number` 往返，QQ 号不丢精度。
- OneBot 把段原样放进 `message`；不再校验 `at` 目标，因此 `qq: "all"` 会被原样发出，能否 @ 全体取决于账号权限和 OneBot 实现（取舍已写入 `group-management.md`）。
- 不支持消息段的连接器发送纯文本形式：`text` 取其文本，`at` 写成 `@昵称`/`@号码`，其余类型省略。
- 验证：go build ./...、go vet ./...、go test ./... -count=1 通过；-race 通过。

## 2026-11-09 专用决策模型缺省关闭，统一开关命名

配置里只有 `decision.dm.disabled` 和 `conversation_memory.disabled` 用 `disabled`，其余开关都用 `enabled`。原因是这两项的 Go 字段是普通 `bool`，缺省启用只能把开关写成反向。梳理后确定规则：**缺省启用的功能写 `disabled`，缺省关闭的功能写 `enabled`**，看键名就知道缺省状态。

- `conversation_memory` 保持缺省启用（连续追问依赖它；进程内、有界、不落盘、无外部依赖），键仍是 `disabled`。
- `decision.dm` 改为缺省关闭：它依赖外部服务和凭据，启用后失败不降级，没有它时直接用回答 LLM 也能工作。键从 `disabled` 改为 `enabled`；写了 `dm` 对象但不写 `enabled` 现在等于未启用，必须显式 `enabled=true`。
- 旧的 `decision.dm.disabled` 已移除，残留会报未知字段；原来写 `disabled: true` 的改为 `enabled: false`（语义不变），原来依赖“有对象就启用”的必须补 `enabled: true`。
- 示例配置与本地配置已同步；`configuration.md` 写明命名规则。
- 验证：go build ./...、go vet ./...、go test ./... -count=1 通过；-race 通过。

## 2026-11-09 让位触发条件扩展：管理员事后纠正

真实验收中让位“没有效果”：日志显示 Bot 在问题提出约 8 秒后就主动回答了，管理员 12 秒后才引用该问题回复“不支持”，此时问题既不在缓冲也不在途，原触发条件（引用待答/生成中的问题，或语义接管）不成立。这是设计如此，但“管理员在 Bot 答完后纠正，随后同话题追问 Bot 不应抢答”同样需要让位。扩展：

- 引用路径（确定、不花模型调用）：可信管理员引用的消息，如果核心近期消息容器里有 Bot 在 `yield_duration` 内回复它的记录，与引用待答问题一样触发让位并丢弃该群缓冲。
- 上下文路径（覆盖未引用的情况）：主动插话的那一次决策附带这批消息之前的近期群聊（取自核心容器，`context_messages` 条，标出管理员和 Bot），规则要求管理员已在回答或纠正同一话题时判为不值得回答。不增加模型调用。
- 中间曾做过“模块自己记一份已回答问题 ID”的版本，未提交，已由上面两条取代。
- 不变的约束：只对群主/管理员生效；引用无关消息、普通成员引用、超过让位时长后的引用都不触发；@ 仍不受让位影响。
- 已知缺口：管理员直接引用 Bot 自己发出的回复消息暂不触发，因为适配器不记录 Bot 发出消息的 ID（需要改动发送接口，另行评估）。
- 验证：go build ./...、go vet ./...、go test ./... -count=1 通过；-race 通过。真实环境验收 2026-11-09 通过：管理员在 Bot 答完后引用或不引用地回答、纠正，之后同群未 @ 的追问 Bot 保持沉默。

## 2026-11-09 核心近期消息容器

主动插话的缓冲池只放“还没处理的问题”，处理完即清空，管理员消息和 Bot 回复都不进去；短期问答只存某个用户与 Bot 的问答。两者都拿不到“这个群最近发生了什么”。新增核心层容器 `internal/history`，作为模块可依赖的公共结构：

- 由宿主持有，**始终启用，没有开关**；`message_history` 只调整上限（每群 `max_messages=50`、`ttl=30m`、`max_text_chars=1000`），写 `enabled`/`disabled` 报未知字段。跨热更新保留，重启清空，只在内存中，不存图片内容，只记录已绑定的群，解绑即丢弃。
- 记录点：`Manager.HandleInbound` 入口记录收到的每条群消息；引擎、运维路由和通知的发送器外包一层，发送成功后记录 Bot 的消息（含它引用的消息 ID），发送失败不记。
- 滑动：逐条滑动，满了挤出最旧的一条；过期记录读不到，下次写入时顺带清理。曾讨论“满了一次移出一批、降到 60%”，因目前没有消费方需要整批而未采用；事件本身按“一批记录”设计，以后改为批量时订阅者不用改。
- 读取：只读能力 `history.reader.v1`，提供最近 N 条、某序号之后的记录（带缺口提示）、按平台消息 ID 查找，返回副本。每条记录有群内递增、不复用的序号。
- 事件：模块用新的注册项 `History` 订阅“新消息进入”和“消息移出”（带原因：capacity/expired/config）。只做发生后的通知，不提供拦截；写入后在容器锁外按顺序同步投递，订阅者回调里再写入不会死锁，panic 被隔离；只投递给当前一代模块。
- 状态归属：模块自己的标记（如知识沉淀的答题会话）放在模块里，用“群 + 序号”或消息 ID 指向记录，容器不提供“钉住”。
- 已知限制：Bot 发出的消息没有平台消息 ID（发送接口不返回），所以“管理员直接引用 Bot 的回复”仍判断不了。
- 后续：知识沉淀重构时改为订阅“新消息进入”，问题内容先查容器、已滑出的用引用原文，去掉现有的 24 小时候选问题缓冲，届时再并掉 `Observers`。本次不动。
- 验证：go build ./...、go vet ./...、go test ./... -count=1 通过；-race 通过。

## 2026-11-09 decision.dm.provider 改名为 protocol

`jev` 指的是 System One 这套接口格式（独立的 `/v1/systemone` 请求和 choice 结果），不是供应商；具体服务由 `endpoint` 决定，本地配置就是把 `jev` 指向了自己的端点。`provider` 还会和“连接器提供者”混淆，也与 `llm.protocol` 的写法不一致。字段改名为 `decision.dm.protocol`，校验仍只接受 `jev`，错误信息改为 `decision.dm.protocol must be jev`。旧的 `provider` 键因严格解析报未知字段，需改名，不做兼容。示例配置、本地配置和 `configuration.md` 同步；文档里的 DM 示例补上了缺省关闭后必须写的 `"enabled": true`。
