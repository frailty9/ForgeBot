# ForgeBot 计划入口

更新日期：2026-09-17。

本目录持久化需求、决策、阶段进度与交接信息，不代表所有建议都已实现或获准执行。

## 从哪里继续

1. 阅读 [阶段路线图](roadmap.md)，确认当前阶段、范围与下一步。
2. 阅读 [模块化 Hook 需求](modular-hooks/requirements.md)，以其中记录的最新用户决策为准。
3. 检查当前 Git 分支、工作区和代码，不凭旧计划勾选或其他分支状态推断当前实现。
4. 模块级 Hook 首版已在 feat/modular-hooks 实施，先读任务表的最终验证与交接；普通测试通过，race 受本地工具链阻塞。下一阶段先复核 Agent Loop 需求，不重复执行本阶段。

## 当前计划

| 计划 | 用途 | 状态 |
| --- | --- | --- |
| [roadmap.md](roadmap.md) | 全局优先级、阶段范围、交接说明 | 后续阶段需在前阶段完成后复核 |
| [modular-hooks/requirements.md](modular-hooks/requirements.md) | 模块级挂载、开关、替代自由与配置存储 | 首期已落实，长期能力仍受设计范围约束 |
| [modular-hooks/design.md](modular-hooks/design.md) | 首个迁移模块、强类型节点、配置、生命周期和验证矩阵 | 已确认并实施，含实现补充 |
| [modular-hooks/tasks.md](modular-hooks/tasks.md) | 实施清单、验证结果与下一阶段建议 | 七项完成；竞态验证环境限制已记录 |
| [agent-loop/requirements.md](agent-loop/requirements.md) | 多轮工具调用、预算、超时、取消与降级需求 | 需求规划中，待确认 |

用户已授权本阶段连续实施；未授权提交、合并或自动执行后续功能阶段。

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
