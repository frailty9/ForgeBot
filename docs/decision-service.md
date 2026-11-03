# 决策服务契约

`internal/decision` 是 Core 共享判断入口，通过 `decision.service.v1` capability 提供给受信任的内置消费者。服务只给建议，不提供发送、审核、撤回、配置写入或凭据访问权限。运行时策略接入状态见版本任务清单。

宿主在构建期注册带 ID/版本的任务；首版采用有描述的有限枚举题，每题必须有 `unknown`。由这些受限题型生成 JSON Schema，所有适配器的输出再做本地字段/枚举检查，不开放任意供应商请求透传。一个任务最多 8 题，任务定义最多 32768 字节，单次 state 最多 16384 字节。

未配置 DM 时调用独立的 LLM 决策适配；它复用模型客户端，但没有普通聊天提示词、Skill、工具或用户聊天记忆。没有原生结构化输出保证时使用受限提示词与严格本地校验，拒绝 Markdown 包装、额外字段、多份 JSON 和工具请求。

配置 DM 时优先 DM；技术失败或结构无效至多交给 LLM 后备一次。合法 unknown 或低置信选择不反复重试以追求肯定答案。全部不可用时返回 unknown 和可观察原因，由各决策点执行规则、沉默、澄清或待人工；不能自动放行。

聊天或群事件入口通过 `WithBudget` 分配共享语义调用预算，缺省最多两次（含后备）。父取消和总时限优先，默认单次 DM 3 秒、LLM 决策 8 秒，并限制并发。同 run 的相同任务/输入可复用结果，不将该缓存扩展为跨用户长期记忆。

Jev 适配依据 2026-11-03 核对的 [TypeSafe System One API](https://docs.typesafe.ai/api/system-one.md) 与 [Choice 题型](https://docs.typesafe.ai/primitives/choice.md)，使用独立的 `/v1/systemone` 请求、命名问题和 choice 结果，不套用聊天补全协议。概率分布与置信度经本地验证，低于配置阈值或明确无置信度时转换为 unknown；LLM 自报 confidence 不与它共用阈值。此适配已有受控 HTTP 测试，尚未声称真实服务验收。
