# ForgeBot 计划入口

更新日期：2026-11-11。

本目录持久化需求、决策、阶段进度与交接信息，不代表所有建议都已实现或获准执行。

## 从哪里继续

1. 阅读 [v0.2.0 需求](v0.2.0/requirements.md)，原范围于 2026-11-10 确认：沉淀重构、Connector 旁路清理、消息撤回、外部 HTTP 入群审批、QQ 官方机器人连接器。2026-11-11 用户追加确认 Telegram 群聊连接器，直接并入本版，尚未实现。
2. 阅读 [v0.2.0 设计](v0.2.0/design.md) 与 [任务顺序](v0.2.0/tasks.md)。用户已于 2026-11-10 确认实施，先提交规划文档，再从沉淀重构开始；不能把计划当成已实现。v0.1.0 已合并并有本地标签，主要真实验收通过，沉淀运营效果随 v0.2.0 重构再验；历史证据见 [v0.1.0 tasks](v0.1.0/tasks.md)。
3. 阅读 [功能与需求清单](../docs/feature-inventory.md)，区分当前代码、待实现目标与仅讨论事项。
4. 检查当前 Git 分支、工作区和代码，不凭旧勾选或规划分支状态推断实现；本次核对 `feat/v0.2.0@1103bc7`、本地 `master@fc5a7ec`，未刷新远端。
5. Hook、内置功能模块、Connector Provider 模块化已合入主线，不重复实施。FPP 与 WebUI 本版延后，MCP/Skill 保持 Core Agent 扩展入口。

## 当前计划

| 计划 | 用途 | 状态 |
| --- | --- | --- |
| [v0.2.0/requirements.md](v0.2.0/requirements.md) | 本版六项目标与非目标 | Telegram 范围已追加确认，见 §1.5 |
| [v0.2.0/design.md](v0.2.0/design.md) | 沉淀、宿主撤回、外部审批、QQ 与 Telegram；通用回执已取消 | 原实现进度见 tasks；Telegram 设计方案见 §8，未实现 |
| [v0.2.0/tasks.md](v0.2.0/tasks.md) | 工作包 0–8、依赖和真实验收 | QQ 及协议修复已提交；Telegram 为工作包 7，按四批推进，工作包 8 保留真实验收 |
| [v0.1.0/requirements.md](v0.1.0/requirements.md) | 首版目标、行为变更、五项应用待办、存储与开源交付 | 已实施代码；Apache-2.0已确认，验收状态见tasks |
| [v0.1.0/design.md](v0.1.0/design.md) | 结果、存储、问答、决策、群操作与生命周期边界 | 已授权实施，状态与证据见 tasks |
| [v0.1.0/tasks.md](v0.1.0/tasks.md) | 工作包 0–11、依赖、验收和提交拆分 | 代码/文档/自动验证已收口，主要真实验收已通过，自动沉淀待补 |
| [功能清单](../docs/feature-inventory.md) | 运行时、运维、扩展与架构的当前状态及版本关联 | 代码核对记录，不等于发行验收 |
| [roadmap.md](roadmap.md) | 全局优先级、阶段范围、交接说明 | 后续阶段需在前阶段完成后复核 |
| [modular-hooks/requirements.md](modular-hooks/requirements.md) | 模块级挂载、开关、替代自由与配置存储 | 首期已落实，长期能力仍受设计范围约束 |
| [modular-hooks/design.md](modular-hooks/design.md) | 首个迁移模块、强类型节点、配置、生命周期和验证矩阵 | 已确认并实施，含实现补充 |
| [modular-hooks/tasks.md](modular-hooks/tasks.md) | 实施清单、验证结果与下一阶段建议 | 七项完成；竞态验证环境限制已记录 |
| [agent-loop/requirements.md](agent-loop/requirements.md) | 多轮工具调用、预算、超时、取消与降级需求 | 历史草案；本版以 v0.1.0 范围收口，/search 兼容要求不再沿用 |
| [builtin-feature-modules/tasks.md](builtin-feature-modules/tasks.md) | 主动介入、检索与反思的模块化 | 已完成并合入主线 |
| [connector-provider-modules/tasks.md](connector-provider-modules/tasks.md) | Echo/OneBot Provider 与配置模型 | 已完成并合入主线 |

旧阶段授权不自动扩展到后续版本；v0.2.0 范围已确认，按本版确认后的设计和任务执行。允许破坏式变更不等于允许丢弃本地数据或重写历史。

## 历史计划

- [运行时配置管理](runtime-config-management/requirements.md)
- [结构化检索](structured-search/requirements.md)
- [知识反思](knowledge-reflection/requirements.md)

历史文件保留用于追溯，不是当前能力的唯一依据。例如反思任务已勾选完成，但标题仍有“待确认”；部分路径描述早于可配置 extra_directory 的变更。遇到冲突先核对代码与最新用户决策，不重新执行旧任务或擅自覆盖历史。

## 维护约定

- 明确区分已确认决策、建议和待确认事项，不能把助手建议视为用户批准。
- 分别记录需求、设计、任务和实现状态；文档完成不等于功能完成。
- 每阶段结束更新路线图交接：完成项、验证命令与结果、提交、遗留问题、下一步。
- 长上下文恢复后先读入口与交接，阶段结束后重新评估下一阶段，不机械执行旧清单。
- 最新用户决策优先于文件；变化应记录取代了什么。
