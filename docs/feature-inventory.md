# ForgeBot 功能与需求清单

核对日期：2026-11-05。代码基线：`feat/v0.1.0`（初始盘点基于 `master@c982e73`）。

本清单按代码、测试文件、分支差异和会话决策整理。它记录实现范围，不替代发布验收；初次核对未重跑测试，随后分支实施已运行全量测试/vet，证据见 tasks；未刷新远端分支。

**v0.1.0 范围已于 2026-11-03 选定，验收细节见 [版本需求](../specs/v0.1.0/requirements.md)。** 下表的“已实现”仍指当前代码，不能把版本目标当作现成功能。新目标包含必需回答 LLM、图片能力开关、表情文本化、决策模型与回退、移除 /search，以及选中的五项待办和单后端存储调整。

实施设计见 [设计](../specs/v0.1.0/design.md)，实时状态见 [任务顺序](../specs/v0.1.0/tasks.md)。2026-11-03 已在 feat/v0.1.0 完成本版功能代码；2026-11-05 用户审阅后修正业务范围、预检索与公告/撤回范围；Apache-2.0已确认，尚未合并 master。真实群管理/欢迎/DM验收仍独立跟踪。

## 分类与状态

“应用 / 架构”保留为需求性质，但目录进一步按使用者观察面细分：

| 目录 | 回答的问题 | 示例 |
| --- | --- | --- |
| Bot 运行时行为 | Bot 在聊天中能做什么，如何处理边界？ | 群聊收发、问答、图片读取、工具调用、预回复、防重、人工让位、自动沉淀 |
| 配置与运维能力 | 部署者怎样配置、管理和诊断 Bot？ | 更换模型、热更新、凭据管理、运营命令、管理 API |
| 开发者扩展能力 | 开发者怎样给 Bot 增加或替换能力？ | 代码模块、对外 SDK、外部插件、命令和表单贡献 |
| 架构支撑能力 | 上述行为由什么契约与生命周期机制支撑？ | Hook、Provider、capability、generation、Store |

具体协议、算法和文件格式放在单独的“实现支持表”，并作为能力的实现标签，不作为同级产品功能计数。例如群聊收发是一项能力，OneBot 是当前实现；工具调用是一项能力，内置 Tools 与 MCP 是工具来源。协议无关描述表示能力契约通用，不表示已适配所有协议。

运行时能力再用“交互、理解、防护、一致性、工具、知识、体验、协作”等标签表示用途。防护和一致性也是产品可观察行为，不能全部埋入架构清单。“属于 Core 还是 Module”独立记录，不决定能力属于哪一类。

状态含义：

| 状态 | 判断标准 |
| --- | --- |
| 已实现 | 表中限定范围已有主线代码；不表示所有边界均已验收 |
| 部分实现 | 已有可执行代码，但较完整的产品能力还有明确缺口 |
| 代码进行中 | 有实际功能代码正在修改或存在活跃、未完成的实现分支 |
| 仅规划 | 已有需求或设计文档，尚无对应新功能实现 |
| 仅讨论 | 会话或路线图提到过，尚无实施方案和功能代码 |
| 暂缓 | 已明确挂起，不能作为当前待实施任务 |

状态、验收程度、优先级分别记录。质量、运维、发布、文档可用标签表示，不需要为了它们新增主分类；License 和 CI 等发行准备也不能冒充已实现的 Bot 功能。

## 当前分支覆盖记录（优先于下方主线基线盘点）

| 范围 | 2026-11-05 分支状态 | 证据 |
| --- | --- | --- |
| BOT-02/03/05 | 正式运行 LLM 必需；图片开关、可靠表情文字已实现 | 4067507、2b1c552 |
| BOT-07/13、APP-P01、ARCH-P01 | 连续工具循环、总预算/取消已实现；移除 /search 专用路径 | 663abb8、fea5232 |
| BOT-16、APP-P02 | 进程内短期记忆、隔离、顺序与投递提交已实现 | 1d15065 |
| BOT-10/20/22、ARCH-P14/15 | 共享决策、DM/LLM 自动选用已实现；单次决策最多一个模型请求。2026-11-05 起业务范围改由回答模型按提示词处理，删除预检索 | d12e5da、d375244、75b06d5 |
| BOT-12、APP-P03、ARCH-P12 | 群级让位、可信平台角色、@不受让位影响已实现 | d38b492 |
| BOT-17、APP-P09 | 入群自动审批与 OneBot 适配已实现；真实权限待验收。BOT-18/19 公告与撤回 2026-11-05 挂起 | 5d28fa2、05df801 |
| BOT-21、APP-P10 | 欢迎内置模块与受限事件回复、去重已实现；真实成员事件待验收 | 8023473 |
| APP-P06 | 统一目录、init、doctor、capabilities 已实现 | a9af15b |
| ARCH-P05/13 | 单一文件后端、分职责接口与稳定结果语义已实现 | 88fb15a、2edc132、2d262a6 |

上述新增能力已有受控测试，最终命令证据见 tasks。公开 SDK、FPP、WebUI、Telegram 和真实数据库后端仍未实现。下方明细表已按当前分支刷新；原主线盘点时的状态可从 git 历史查看。

## 能力明细

- 本地主线：`master@c982e73`，最后一个主线提交为连接器 Provider 模块化收尾。
- 清单初次核对时没有已跟踪的功能代码修改；本次新增清单、版本需求及规格入口/路线图更新属于文档工作。未跟踪的 `.claude/` 不计入功能开发。
- 新版本的实施结果见上方覆盖记录；下方初始能力表仍以主线基线为比较依据。
- `feat/modular-hooks`、`feat/builtin-feature-modules`、`feat/connector-provider-modules` 为已合并成果，不能当作仍在开发。
- `archive/*` 为历史归档尝试，不算活跃开发或当前可用产品。

## Bot 运行时行为

这一类只描述聊天与任务处理时可以观察到的能力。实现位置、协议和工具来源见证据或后面的支持表。

| ID | 能力 | 用途标签 | 状态 | 当前实现与边界 | 主要证据 |
| --- | --- | --- | --- | --- | --- |
| BOT-01 | 群聊消息接收与回复 | 交互 | 已实现 | 接收绑定群组的消息，并把回复送回对应逻辑会话；支持多个接入实例、多群并行；当前业务限群组 | [消息契约](../internal/messaging/message.go)、[平台路由](../internal/platform/platform.go)、[Engine](../internal/bot/engine.go) |
| BOT-02 | LLM 问答 | 理解与回答 | 问答已实现；必需配置待改 | 当前允许无模型回显；v0.1.0 要求有效回答 LLM，正常运行时缺少模型/必需凭据应报配置错误，移除正式回复的回显降级 | [Engine](../internal/bot/engine.go:270)、[问答测试](../internal/bot/engine_test.go)、[版本 R1](../specs/v0.1.0/requirements.md) |
| BOT-03 | 图片读取与理解 | 多模态 | 图片链路已实现；开关待改 | 当前消息和引用图片可入模；v0.1.0 增加模型图片理解 boolean，false 时对已触发的含图问答直接回复“当前暂不支持图片”，不调用模型或检索 | [多模态测试](../internal/bot/engine_test.go)、[消息模型](../internal/messaging/message.go)、[版本 R2](../specs/v0.1.0/requirements.md) |
| BOT-04 | 消息引用与上下文关联 | 交互与理解 | 已实现 | 引用文字/图片独立于当前输入传给模型，回复可关联原消息；不等同于连续多轮聊天记忆 | [引用模型](../internal/messaging/message.go)、[引用测试](../internal/platform/onebot/onebot_test.go) |
| BOT-05 | 空值和无效内容处理 | 防护 | 防护已实现；表情文本化待改 | 空 @ 自我介绍、无效输入防护和空白回复抑制已有；v0.1.0 由适配器优先把可靠表情描述/转义映射为文本，再保留 Core 防护，避免直接丢失表情语义 | [回复体验](../internal/modules/replyexperience/module.go)、[Engine](../internal/bot/engine.go)、[平台测试](../internal/platform/onebot/onebot_test.go) |
| BOT-06 | 重复消息／问题处理抑制 | 一致性 | 已实现（有限范围） | 同会话消息 ID 在 TTL 内去重；无消息 ID 时可按同会话、同用户、同文本回退去重。不同 ID 的相似问题合并、在途语义防抖尚未实现 | [Admission](../internal/admission/admission.go)、[去重测试](../internal/bot/engine_test.go) |
| BOT-07 | 工具调用（含知识检索） | 执行 | 基础已实现，连续调用列入 v0.1.0 | 统一使用内置 Tools 和 MCP；知识检索是其子能力。多轮、总预算、取消与结束语义尚待实现，决策引导检索也必须走同一工具链 | [Engine](../internal/bot/engine.go:321)、[工具层](../internal/tooling/)、[版本需求](../specs/v0.1.0/requirements.md) |
| BOT-08 | 额外技能赋予 | 行为扩展 | 已实现（Skill） | 从本地 SKILL.md 加载全局 instructions；当前不是按任务选择技能、渐进加载或脚本执行 | [Skill](../internal/skills/)、[指令组装](../internal/runtime/manager.go) |
| BOT-09 | 处理前预回复 | 体验辅助 | 已实现（限文档检索） | search_docs 执行前由程序控制发送进度文案，同次 run 最多尝试一次；没有提供给 LLM 的主动发送工具，也没有覆盖全部 MCP 工具 | [预回复策略](../internal/modules/replyexperience/module.go)、[发送执行](../internal/bot/engine.go) |
| BOT-10 | 明确寻址触发 | 交互策略 | 已实现 | @ 或命令提供处理资格；@ 不受主动插话暂停影响。业务范围由回答模型按 `bot.system_prompt` 处理，宿主不预分类 | [Engine](../internal/bot/engine.go)、[@ 路径测试](../internal/bot/decision_test.go)、[版本 R16](../specs/v0.1.0/requirements.md) |
| BOT-11 | 未 @ 自动介入 | 主动协作 | 已实现（有条件） | 选定群组启用后缓存文本、等待静默或最大窗口后处理，限制缓存和并发；不代表已语义识别每个问题或所有管理员自然语言回答 | [主动介入模块](../internal/modules/proactivereply/)、[缓存池与测试](../internal/proactive/) |
| BOT-12 | 管理员／群主介入后临时暂停主动插话 | 人机协作 | 已实现，真实群验收待补 | 按群临时暂停未 @ 主动介入，默认 5m，引用命中待答或明确语义接答触发；@ 不受影响，同条引用 + @ 不再被吞；平台角色已归一化 | [主动模块](../internal/modules/proactivereply/)、[让位状态](../internal/proactive/yield.go)、[身份解析](../internal/administration/resolver.go) |
| BOT-13 | 工具调用子能力：知识检索 | 知识 | 已实现 | 归并 BOT-07。官方维护内置检索工具，部署者可用 MCP 替代；检索时机由回答模型按工具描述与系统提示词选择，宿主不预检索；/search 专用路径已移除 | [检索模块](../internal/modules/structuredsearch/)、[检索上下文](../internal/search/context.go)、[版本 R4/R5](../specs/v0.1.0/requirements.md) |
| BOT-14 | 管理员回答自动沉淀 | 知识积累 | 已实现，运营验收待补 | 引用锚定后聚合同一管理员多条回答，定时扫描、模型判断与置信度、去重、发布文档、更新索引；没有人工审核/候选编辑闭环 | [回答归组测试](../internal/reflection/buffer/buffer_test.go)、[反思模块](../internal/modules/knowledgereflection/)、[发布器](../internal/knowledge/markdown/) |
| BOT-15 | 按会话追加行为与输出约束 | 行为定制 | 已实现 | 使用可选 Prompt suffix 调整渠道格式、群组语气和主题；这是模型指令，不是强制格式转换器 | [配置](../internal/config/config.go)、[指令组装](../internal/bot/engine.go) |
| BOT-16 | 连续追问与短期会话记忆 | 对话连续性 | 已实现 | 进程内按群+用户隔离，TTL/容量/字节上限与顺序队列；仅提交确认发送成功的问答，重启清空，不持久化完整聊天记录 | [会话记忆](../internal/conversation/memory.go)、[投递提交](../internal/bot/memory_test.go)、[版本 R7](../specs/v0.1.0/requirements.md) |
| BOT-17 | 入群申请自动审批 | 群管理 | 已实现，真实平台验收待补 | 每群正则与统一布尔模型判断，支持 AND/OR 短路；失败不操作，不持久化审批记录，人工处理在原平台完成 | [审批实现](../internal/group/review.go)、[受控测试](../internal/group/review_test.go) |
| BOT-18 | 群公告 | 群管理 | 暂缓 | 2026-11-05 挂起并删除实现：OneBot11 标准无群公告接口，原实现依赖非标准 `_send_group_notice` 扩展 | [挂起说明](group-management.md) |
| BOT-19 | 撤回群成员消息 | 群管理与处置 | 暂缓 | 2026-11-05 挂起并删除实现。原意是处理违规消息，但缺少触发方式（管理员拿不到平台消息 ID）；恢复前需先设计入口，如引用违规消息加命令或违规判定 | [挂起说明](group-management.md) |
| BOT-20 | 共享模型决策与业务范围 | 决策 | 已实现，真实 DM 验收待补 | 有 DM 用 DM、未配置/关闭用 LLM；每次最多一次模型请求，无故障后备；布尔入口区分 false 与 error | [决策契约](decision-service.md)、[测试](../internal/decision/routing_test.go) |
| BOT-21 | 新人入群欢迎语 | 成员事件与群互动 | 已实现，真实成员事件验收待补 | `native.welcome` 按群模板发送，实际入群触发、去重，忽略自身/历史/过旧事件；超时记 unknown 不重发；不依赖 DM/LLM | [欢迎模块](../internal/modules/welcome/)、[欢迎测试](../internal/runtime/groups_test.go)、[版本 R15](../specs/v0.1.0/requirements.md) |
| BOT-22 | 部署业务范围约束 | 问答策略 | 已实现（提示词约束） | 2026-11-05 改为由部署者把范围、拒答与寒暄措辞写在 `bot.system_prompt` 中；属于尽力遵守，需目标模型实测。主动插话的范围另在 `native.proactive_reply.scope` 配置，供其决策使用 | [版本 R16](../specs/v0.1.0/requirements.md)、[配置说明](configuration.md) |

BOT-12 的最新决定取代“管理员介入后全面接管 Bot 回复”的表述：只临时关闭该群的主动插话，明确 @ 始终保留正常触发路径；“始终”针对主动插话暂停而言，不绕过消息去重、身份授权或全局运行状态。介入识别、暂停时长、恢复条件、待处理和在途的未 @ 回复如何处置仍待设计。不得把现有单条待答取消当作该需求已经完成。

BOT-17–19 是 Bot 核心的可选群管理能力契约，不要求每种连接器都实现。它们的产品归属是运行时行为，技术实现可以通过独立的小接口提供。接口存在不等于已对 LLM 暴露为工具；人工命令、规则、工具等调用入口另行确定。

## 配置与运维能力

| ID | 能力 | 状态 | 当前实现范围与边界 | 主要证据 |
| --- | --- | --- | --- | --- |
| OPS-01 | 配置接入与群组范围 | 已实现 | 设置连接器实例、绑定群组、逻辑会话和两级可选 Prompt suffix | [配置](../internal/config/config.go) |
| OPS-02 | 更换 LLM 供应商、模型和参数 | 已实现 | 通过应用配置切换地址、协议、模型等；与 BOT-02 的回答能力分开记录 | [配置](../internal/config/)、[Runtime](../internal/runtime/manager.go) |
| OPS-03 | 配置校验、热更新、版本与回滚 | 已实现 | 应用配置校验、版本冲突、历史、回滚和组件重建；静态配置仍需重启进程 | [管理服务](../internal/admin/service.go)、[Store](../internal/settings/) |
| OPS-04 | 凭据管理 | 已实现（LLM Key） | 初始化、加密存储、只写 API、替换与删除；不代表所有协议凭据均已进 SecretStore | [CLI](../cmd/forgebot/main.go)、[凭据服务](../internal/secret/) |
| OPS-05 | 管理员与管理群配置 | 已实现 | 群内用户身份、运营命令白名单和通知目标；权限小于 Web 管理 API | [身份](../internal/administration/)、[运营路由](../internal/operations/) |
| OPS-06 | 管理群命令与运维通知 | 已实现 | /status、/pause、/resume、/restart、/proactive on/off、/reload_docs；故障通知。暂停不等于 BOT-12 的人工接管 | [运营命令](../internal/operator/service.go)、[通知](../internal/notification/) |
| OPS-07 | 文档索引与反思维护 | 已实现 | 重新构建索引、手动扫描反思、配置沉淀输出目录；文件修改不自动等于索引已刷新 | [管理 API](../internal/server/server.go)、[反思配置](../internal/modules/knowledgereflection/module.go) |
| OPS-08 | 状态查询与本地联调 | 已实现 | HTTP 管理 API、分散模块/连接器状态、日志、健康入口、HTTP 测试消息与 Echo | [HTTP 路由](../internal/server/server.go)、[Echo](../internal/modules/echo/)、[日志](../internal/logging/) |
| OPS-09 | Web 管理控制台 | 本版延后，仅规划 | 重建方案在 design/admin-web-ui，只有需求与设计；不阻塞 v0.1.0 | 分支 design/admin-web-ui@b7a8529 |
| OPS-10 | 统一能力目录与初始化/诊断向导 | 已实现 | `GET /v1/admin/capabilities` 与 CLI capabilities 同源；init 不覆盖现有配置与凭据；doctor 默认本地检查，网络/stdio 探测须显式选择 | [CLI](cli.md)、[版本 R11](../specs/v0.1.0/requirements.md) |
| OPS-11 | 回答模型／决策模型的独立配置 | 已实现 | 回答 LLM 必需，决策模型可选；图片开关、DM 阈值、凭据引用可配置，DM Key 只写管理 | [配置说明](configuration.md)、[版本 R1/R2/R9/R11](../specs/v0.1.0/requirements.md) |
| OPS-12 | 业务范围配置 | 已实现 | 回答范围写在 `bot.system_prompt`；主动插话的范围写在 `native.proactive_reply.scope`，只供其决策使用；不固定某个业务平台 | [配置说明](configuration.md)、[版本 R16](../specs/v0.1.0/requirements.md) |

## 开发者扩展能力

| ID | 能力 | 状态 | 当前实现范围与边界 |
| --- | --- | --- | --- |
| DEV-01 | 编写内置功能模块 | 已实现（仓库内） | 可注册 Hook、Tool、Command、Observer、Participant、Job、状态和能力；目前由组合根装配，尚非公开 SDK |
| DEV-02 | 编写新通讯协议实现 | 已实现（仓库内） | 通过 Connector Provider 与 Adapter 接口，复用通用群组绑定和生命周期；没有无需构建的动态安装入口 |
| DEV-03 | 从外部提供工具或指令 | 已实现 | 分别通过 MCP 与 Skill 接入，对应 BOT-07、BOT-08；它们是 Core Agent 扩展接口 |
| DEV-04 | 外部项目通过公开 Go SDK 扩展 | 仅讨论 | 没有公开 SDK、独立扩展示例或兼容性承诺 |
| DEV-05 | 运行时安装 FPP 插件 | 本版延后 | 没有外部握手、加载或插件生命周期实现；不阻塞 v0.1.0 |
| DEV-06 | 插件注入命令与管理表单 | 内置命令已实现，外部部分仅讨论 | 内置 CommandRegistration 已有；外部协议、表单与标准配置读写接口尚未实现 |
| DEV-07 | 按需实现群管理能力 | 已实现（仅审核） | 连接器按支持情况实现入群申请审核，不承担一揽子强制实现；公告与撤回已挂起；宿主按当前绑定提供能力信息，权限显示 unknown |

## 实现支持表

这里记录上述能力目前由哪些实现提供，不重复算作运行时行为功能。

| 对应能力 | 当前实现 | 明确范围 |
| --- | --- | --- |
| 群聊收发 | OneBot HTTP webhook / send_msg | 一个真实协议接入；Echo 用于本地测试；Telegram 等尚无实现 |
| LLM 问答、图片与工具消息 | Anthropic Messages、OpenAI Responses、OpenAI Chat Completions | 三种客户端实现；模型是否支持图片取决于配置的实际模型 |
| 工具来源 | 内置 Tool 注册、MCP | MCP 当前有 Streamable HTTP 与 stdio；不是实现 MCP 所有能力 |
| 额外技能 | 本地 SKILL.md | 有界加载并全局拼接 instructions；当前没有其他技能来源 |
| 知识检索 | 结构化 BM25F 索引 | 本地 MD、MDX、TXT；向量适配继续挂起 |
| 沉淀输出 | Markdown publisher | 可配置相对文档根目录或绝对输出目录 |
| 配置与凭据存储 | JSONFileStore、加密 FileStore | 已有 Store 抽象，其他后端未实现 |
| 入群审核、群公告、成员消息撤回 | 无当前实现 | 现有连接器没有这些 Core 契约实现；平台是否支持需后续分别核实，不能据 OneBot 名称推断可用 |

## 架构支撑能力：已有代码

| ID | 能力 | 状态 | 当前范围与边界 | 主要证据 |
| --- | --- | --- | --- | --- |
| ARCH-01 | 平台无关消息与会话模型 | 已实现 | Adapter 将外部账号/群号映射为逻辑会话；当前业务绑定仍为群组 | [消息模型](../internal/messaging/message.go)、[平台层](../internal/platform/platform.go) |
| ARCH-02 | 统一 LLM Client 契约 | 已实现 | 三个固定协议实现共用请求、响应、图片和工具消息模型；属于 Core | [协议与客户端](../internal/llm/) |
| ARCH-03 | LLM 客户端流式能力 | 已实现 | 客户端支持 SSE/Stream；Bot 对话主流程仍调用 Complete，不等于聊天平台逐字流式发送 | [SSE](../internal/llm/sse.go)、[Engine](../internal/bot/engine.go) |
| ARCH-04 | 通用 Tool / Executor | 已实现 | 统一定义、调用和结果，多工具来源合并及重名拒绝；不包含完整多轮执行器 | [工具契约与注册](../internal/tooling/) |
| ARCH-05 | MCP Transport 抽象 | 已实现 | MCP 客户端与 HTTP / stdio 传输实现分离；尚不是供 FPP 共用的通用进程通信层 | [Transport](../internal/tooling/mcp/transport.go)、[stdio](../internal/tooling/mcp/stdio.go) |
| ARCH-06 | 模块 Factory / Registry | 已实现 | 按功能整体挂载、默认启停、私有 options、严格校验、公开视图与状态 | [模块系统](../internal/modules/) |
| ARCH-07 | 三类强类型 Hook | 已实现 | 输入准备、工具执行前、回复发送前；执行排序、错误策略、上下文时限、结果校验和 panic 处理；不是进程级隔离 | [Hook](../internal/hooks/) |
| ARCH-08 | 模块扩展注册面 | 已实现 | Tool、Command、Observer、Participant、Background Job、Status、Connector Provider；内部 Go 注册，不等于外部插件协议 | [注册契约](../internal/modules/runtime.go) |
| ARCH-09 | capability 依赖注入 | 已实现 | 受限能力视图、稳定 ID、依赖排序和冲突校验；不向模块交付完整 Runtime / Store | [能力容器](../internal/capability/)、[模块 Registry](../internal/modules/registry.go) |
| ARCH-10 | 内置应用能力模块化 | 已实现 | `reply_experience`、`proactive_reply`、`structured_search`、`knowledge_reflection`；业务开关收口到 modules | [组合根](../internal/app/app.go)、[实施记录](../specs/builtin-feature-modules/tasks.md) |
| ARCH-11 | Connector Provider 模块化 | 已实现 | `native.onebot` / `native.echo` 提供协议，connectors 按 type 声明实例，Provider 解析与脱敏 options；Runtime 仍留测试注入旁路，清理尚未进行 | [Provider](../internal/platform/provider.go)、[构建链](../internal/runtime/manager.go)、[实施记录](../specs/connector-provider-modules/tasks.md) |
| ARCH-12 | generation 生命周期与替换 | 已实现 | 准备候选、发布、旧请求保留旧代、模块退场和关闭；连接器更新有停止/启动与失败恢复路径 | [Runtime](../internal/runtime/manager.go)、[代际测试](../internal/runtime/modules_test.go) |
| ARCH-13 | 不可关闭的核心 Admission | 已实现 | 消息合法性、寻址边界与消息身份去重，与可选主动介入缓存拆分 | [Admission](../internal/admission/)、[Engine](../internal/bot/engine.go) |
| ARCH-14 | 配置模型与存储接口 | 已实现 | 静态 YAML/环境覆盖、运行时 JSON、严格解析、快照复制、`settings.Store` 与 JSONFileStore、版本持久化 | [配置](../internal/config/)、[Store](../internal/settings/store.go)、[文件实现](../internal/settings/json_file_store.go) |
| ARCH-15 | SecretStore 与凭据边界 | 已实现 | Store 接口、AES-GCM 文件实现、暂存/激活、写入与运行时切换；更多存储后端未实现 | [SecretStore](../internal/secret/) |
| ARCH-16 | 检索与沉淀的资源和发布边界 | 已实现 | 索引构建/查询上限与原子替换、非可信检索上下文包装、反思有界缓冲、原子 Markdown 与去重 | [检索](../internal/search/)、[反思](../internal/reflection/)、[发布器](../internal/knowledge/markdown/) |
| ARCH-17 | 日志、状态与回归测试基础 | 已实现 | slog、模块/连接器状态、健康入口，以及客户端/模块/Runtime 等测试；不表示已完成统一健康诊断或 CI | [日志](../internal/logging/)、[服务端](../internal/server/)、[测试目录](../internal/runtime/) |

## 待开工的运行时与管理需求

| ID | 需求 | 状态 | 已有材料或最小明确边界 |
| --- | --- | --- | --- |
| APP-P01 | 有界连续工具调用（BOT-07） | 已实现 | 多轮串行工具调用、调用 ID 关联、轮数/工具数/时限/上下文上限与确定终态；见 [版本 R6](../specs/v0.1.0/requirements.md) |
| APP-P02 | 连续追问的短期会话上下文（BOT-16） | 已实现 | 进程内隔离、TTL、容量与顺序策略；主动介入混合批次不写入私人记忆；不持久化完整聊天记录 |
| APP-P03 | 介入后临时暂停主动插话与恢复（BOT-12） | 已实现，真实群验收待补 | 按群抑制未 @ 主动介入，明确 @ 保留；默认 5m，刷新/过期/手动恢复，在途与待处理项不补发 |
| APP-P04 | Web 管理面板 | 本版延后，仅规划 | design/admin-web-ui 分支只有文档，历史实现已归档；不阻塞 v0.1.0 |
| APP-P05 | Telegram 与更多真实渠道 | 仅讨论 / 路线图 | 属于实现支持扩充，群聊收发能力本身已有；当前有 OneBot 与测试 Echo，Provider 接口可供实现新协议 |
| APP-P06 | 统一能力目录、诊断与 CLI init/doctor | 已实现 | 目录投影、init、doctor、capabilities 与管理 API 同源；真实 OneBot/LLM 联合教程待验收 |
| APP-P07 | 自动沉淀审核与候选运营 | 仅讨论 / 待复核 | 核心扫描发布已有实现；人工审核、候选编辑/撤销等运营流程没有形成当前实现 |
| APP-P08 | 更完整的平台投递体验 | 仅讨论 / 路线图 | 发送回执、分类重试与幂等、长消息处理、合并转发等仍需逐项设计；已有引用关系和发送返回错误不等于完整交付 |
| APP-P09 | 入群申请审核（BOT-17）；公告、撤回（BOT-18/19）暂缓 | 审核已实现，真实平台验收待补 | 正则与模型 AND/OR 自动审批，默认关闭，不留 Bot 侧记录；公告与撤回 2026-11-05 挂起 |
| APP-P10 | 新人入群欢迎语（BOT-21） | 已实现，真实成员事件验收待补 | 独立于入群申请审核；基于真实成员事件，按群模板、去重并报告不支持状态 |

## 架构演进需求

| ID | 需求 | 状态 | 边界与当前决策 |
| --- | --- | --- | --- |
| ARCH-P01 | 通用有界 Agent 执行器 | 已实现 | 与 APP-P01 是同一工作的技术支撑：多轮、总预算、总时限、取消和终止策略 |
| ARCH-P02 | 外部 FPP 插件运行时 | 本版延后 | 没有握手、加载、进程插件生命周期或对外协议实现；不阻塞 v0.1.0 |
| ARCH-P03 | 可供外部项目使用的 Go SDK | 仅讨论 | 当前主要契约在 internal，尚无公开 SDK、独立扩展示例或兼容性承诺；“SDK 先于 FPP”尚不是已确认实施顺序 |
| ARCH-P04 | 插件自定义命令与配置表单 | 部分实现 / 外部部分仅讨论 | 内置模块已有 CommandRegistration；外部命令协议、表单声明及标准配置读写接口尚未实现 |
| ARCH-P05 | Store 数据结构与分职责接口、单后端 | 已实现首期，未合并 master | 配置/历史/凭据/运营记录分职责接口由唯一 file Backend 提供；静态单选、元数据、条件更新和恢复已测试。SQL/NoSQL 延后，禁止双写和故障切后端；见 [存储说明](storage.md) |
| ARCH-P06 | Connector 宿主注入旁路清理 | 仅讨论 | Builder 的 ConnectorFactory / ConnectorProviders 仍存在，生产组合根当前不用；没有开展删除和测试迁移 |
| ARCH-P07 | 请求与投递的重试策略 | 仅讨论 / 路线图 | LLM HTTP 已有超时与错误传播，没有通用可配置重试/退避体系；消息投递还需另定幂等语义 |
| ARCH-P08 | MCP / Skill 独立 Manager | 暂缓 | 保留 extensions.mcp / extensions.skills 作为 Core Agent 扩展入口；拆分建议未实施，暂等实际独立管理需求 |
| ARCH-P09 | FPP 与 MCP 共享通信底座 | 仅讨论 | 目标是复用通信基础组件并分离协议语义；当前 MCP Transport 并未成为通用 FPP 通信层 |
| ARCH-P10 | 进程隔离、沙箱与插件权限系统 | 仅讨论 | stdio 子进程接入已存在，但不等于沙箱、插件权限声明或受限运行环境 |
| ARCH-P11 | 可选群管理接口与能力发现 | 已实现（仅审核） | 入群审核为可选小接口，基础 Adapter 职责不变；Core 按逻辑会话路由并发现能力，接口支持与账号权限分开表示；公告与撤回接口已删除 |
| ARCH-P12 | 群管理／成员事件与角色归一化 | 已实现 | 入群申请与实际成员入群独立于聊天文本建模；平台 owner/admin 作为唯一的可信答疑身份来源，不与宿主管理权限等同；运维权限由唯一的运维群承担 |
| ARCH-P13 | Core 统一操作结果与错误语义 | 已实现，未合并 master | 消息/配置/LLM/工具/投递/群操作/CLI 边界使用稳定分类与副作用确定性；见 [操作结果](operation-results.md) |
| ARCH-P14 | 共享决策服务与任务契约 | 已实现，真实 DM 验收待补 | 内置功能经受限 capability 复用；每次最多一个模型请求，DM 故障不转调 LLM；当前消费者为主动插话、管理员接答与入群审批 |
| ARCH-P15 | 问答触发、业务范围、检索分层 | 已实现（2026-11-05 修订） | @ 决定进入处理；范围由回答模型按系统指令处理；检索由模型选择工具。主动插话由模块用一次布尔决策合并“值得回答”与“在模块 scope 内” |

## Core 统一操作结果原则（2026-11-03 用户确认）

此前对群管理提出的“不支持、无权限、暂时失败、目标失效”等区分，提升为全部 Core 操作的共同约定。范围包括消息处理与投递、LLM、工具、MCP、Skill 加载、知识检索与沉淀、配置更新、存储和群管理等对外或跨子系统操作。内置实现和连接器应把能确认的底层结果映射为统一语义；无法确认的外部错误保留未知原因，不根据错误文本猜测或伪造分类。

统一的是结果含义、可判断性和边界映射，不强迫纯函数、内部辅助函数或所有 Go 接口返回同一个结构。可继续使用领域结果、类型化 error 与错误链；调用方按类型或稳定原因码处理，不能依赖匹配人类可读的错误文案。

| 结果领域 | 必须能表达的区别 | 示例 |
| --- | --- | --- |
| 正常完成 | 有结果、合法空结果、已完成且无需重复执行 | 检索无命中仍是成功查询；文档已存在而跳过发布 |
| 正常未执行或延后 | 未寻址、重复、缓冲、暂停、策略性沉默 | 重复消息不重复回答，不作为系统故障 |
| 能力与配置 | 不支持、未配置、已禁用、依赖未就绪 | 连接器没有撤回能力，与能力已关闭不同 |
| 身份、权限与策略 | 未认证、无权限、策略拒绝 | Token 无效、账号没有群管理权限、宿主禁止工具调用 |
| 输入、目标与状态 | 参数无效、目标不存在、已失效、状态冲突 | 申请失效、消息不可撤回、配置版本冲突 |
| 资源与暂时故障 | 限流、预算耗尽、繁忙、依赖暂时不可用、超时 | LLM 限流、索引重建中、网络请求超时 |
| 取消 | 调用方取消、运行时关闭或替换导致取消 | 用户取消不应笼统归为 LLM 失败 |
| 内部或未知错误 | 内部缺陷、外部原因尚不能归类 | 保留诊断上下文与底层原因，不伪装成已知权限或网络错误 |

各操作只使用适用分类，并保留具体领域原因。所需信息原则上包括：稳定分类和原因码、操作与目标定位、面向使用者的说明、必要的诊断关联；字段与 Go 类型在实施时确定。配置更新成功只说明更新操作完成，不能顺带推断全部外部依赖健康；“能力受支持”“当前可用”“本次执行成功”分开表达。

对发送、审核、公告、撤回等有副作用的操作，还要独立表达执行确定性：确认完成、确认未执行、部分完成（适用时）、结果未知。例如请求已发出但响应超时，应记录“超时 + 执行结果未知”，不能声称未执行，也不能因为暂时失败就自动重试。重试需结合幂等性、剩余预算与具体策略判断。

管理 API、CLI、日志和 LLM 工具结果应沿用同一底层语义，按受众转换文案与信息量；不把内部错误、凭据或堆栈原样当作面向用户的说明。正常忽略和去重无需自动发群消息，错误分类也不等于每次错误都要通知聊天用户。

主线基线已有 Verdict、tooling.Result.Code、领域 error 和部分 HTTP 映射；feat/v0.1.0 已补统一结果基础及主要边界。新增群管理、CLI 等尚未实现，其验收随对应工作包推进，不能将基础契约完成等同于全版本验收。

## 新增方向的接口边界（待细化）

1. **主动插话与明确触发分路。** 介入暂停只影响按群主动插话策略；明确 @ 正常回复仍通过通用准入路径。恢复条件不是修改全局 pause/resume 的替代语义。
2. **按能力提供小接口。** 申请审核、公告、撤回分别提供可选接口，不向每个 Adapter 塞入必须实现的群管理大接口。具体 Go 名称、参数与状态结构后续设计。
3. **宿主按逻辑会话路由。** 上层给出 conversation_id、申请引用/消息引用及操作内容，由绑定找到连接器；平台群号、请求标识和账号权限由适配层处理。
4. **事件与操作同时考虑。** 审核需要可归一化、可标识的入群申请事件和对应决策操作；不能只新增一个 Approve 方法，也不能将入群事件伪装成普通问答文本。
5. **区分支持与可用。** 连接器实现某接口，仅表示有该操作实现；当前账号在某群可能无权限，也可能无法预先确认。能力查询与调用错误需表达这些区别。
6. **统一结果原则。** 遵循上述全部 Core 操作的共同约定，区分不支持、无权限、暂时不可用、申请失效/已处理、消息不可撤回及执行结果未知等；公告或撤回不可实现时不伪装成功，也不自动用普通发言替代。
7. **入口与策略另定。** Core 操作契约不等于 LLM 工具授权。入群是否自动同意/拒绝、谁可发布公告或撤回消息、何时允许模型发起操作，均不因登记接口而默认生效。

## 暂缓、规划文档与交付事项

- **向量检索 / 向量 RAG 适配：暂缓。** 当前有按需结构化检索；此前决定等文档体量与效果证明需要后再评估。它不是正在开发的功能。
- **Prayer 差异评估：文档进行中，代码未开工。** `plan/prayer-parity-assessment@571b385` 有需求和设计草案，未产出 matrix/priorities；它是规划工作，不是 Bot 应用功能。
- **默认行为规范 / 承诺深度清单：仅讨论。** 已有散落的行为测试与文档，但没有完成统一产品基线；本清单只记录功能状态，不冒充默认行为规范。
- **开源与发行准备：另用交付标签。** 源码预览、SDK 和二进制预览是不同承诺；License、CI、仓库身份整理、发布流水线与分发验收应单独检查，不能因已有可构建 Go 程序而标为发行完成。
- **现有文档过时：已知记录缺口。** specs/README、总路线图及部分需求/设计页状态早于后续提交。发生冲突时，本清单以当前代码、阶段完成记录和用户最新决定为依据，不机械读取旧勾选。
- **本次补充的行为缺口：** 管理员同条消息引用待答问题且明确 @Bot 时，主动介入池先执行引用取消并返回 ignored。用户已明确介入只暂停主动插话，显式 @ 应保留正常触发；此目标及群主角色处理尚待实现与验收，本次未改代码。去重和预回复也分别限定为当前身份/文本回退去重与文档检索预回复。

## 验证说明

当前分支新增功能已执行全量普通/竞态测试及 vet，2026-11-03 更新编译链后不再出现 0xc0000139。初始盘点与旧主线的历史证据保留在下面；它们不能代替当前分支最终验收。真实平台与模型测试仍须区分受控服务测试。

2026-09-20 阶段记录中，`go test ./...`、`go vet ./...` 通过；race 测试受 Windows `0xc0000139` 加载失败影响。用户随后确认真实 OneBot 测试通过。以上属于历史验证，本次清单核对没有重新执行这些测试。

自动沉淀已有流水线和归组回归测试，但会话中没有确认完成真实群聊运营验收。Provider、Hook、管理 API 等“已实现”不表示本次已完成全量代码审计，也不建立新的稳定 API 或发行承诺。
