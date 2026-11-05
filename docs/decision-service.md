# 决策服务契约

`internal/decision` 是 Core 共享判断入口，通过 `decision.service.v1` capability 提供给受信任的内置消费者。服务只给建议，不提供发送、审核、配置写入或凭据访问权限。运行时策略接入状态见版本任务清单。

当前消费者都通过统一布尔入口接入：主动插话（是否值得主动回答，可附 `native.proactive_reply.scope` 范围）、管理员接答与入群审批。明确 @ 的问答不经过决策服务，范围由回答模型按 `bot.system_prompt` 处理。主动插话遇到未知或故障时保持沉默。宿主目前不注册多选任务。配置与 Key 管理见 [配置说明](configuration.md)。

共享服务提供 `DecideBool(ctx, rule, state) (bool, error)`：`true, nil` 表示明确符合，`false, nil` 表示明确不符合；无法判断返回 `ErrUndecided`，其他失败返回相应错误。消费者通过 `decision.DecideBool(ctx, decider, rule, state)` 注入同一服务。规则与待判断数据分别封装为 `rule` / `data`，布尔任务为 `decision.boolean/v1`，不向业务暴露供应商选择。规则最多 4096 字节，封装后的 state 同样受 16384 字节上限约束。错误不能当作 false 消费。

宿主在构建期注册带 ID/版本的任务；首版采用有描述的有限枚举题，每题必须有 `unknown`。由这些受限题型生成 JSON Schema，所有适配器的输出再做本地字段/枚举检查，不开放任意供应商请求透传。一个任务最多 8 题，任务定义最多 32768 字节，单次 state 最多 16384 字节。

未配置 DM 时调用独立的 LLM 决策适配；它复用模型客户端，但没有普通聊天提示词、Skill、工具或用户聊天记忆。没有原生结构化输出保证时使用受限提示词与严格本地校验，拒绝 Markdown 包装、额外字段、多份 JSON 和工具请求。

每次决策最多请求一个模型：DM 已配置且启用时选择 DM，未配置或显式关闭时选择回答 LLM。选择 DM 后，凭据缺失、输入不支持、请求故障、超时或结构无效均直接返回错误，不重试或改用 LLM。多选任务可返回合法 unknown；布尔入口将 unknown 表达为错误，由业务执行沉默或不操作。

聊天或群事件入口通过 `WithBudget` 分配共享语义调用预算；聊天上下文与主动插话批次缺省各允许两次独立判断，入群审批最多一次。单次决策始终最多一个模型请求。父取消和总时限优先，默认单次 DM 3 秒、LLM 决策 8 秒，并限制并发。同 run 的相同任务/输入可复用结果，不将该缓存扩展为跨用户长期记忆。

Jev 适配依据 2026-11-03 核对的 [TypeSafe System One API](https://docs.typesafe.ai/api/system-one.md) 与 [Choice 题型](https://docs.typesafe.ai/primitives/choice.md)，使用独立的 `/v1/systemone` 请求、命名问题和 choice 结果，不套用聊天补全协议。概率分布与置信度经本地验证，低于配置阈值或明确无置信度时转换为 unknown；LLM 自报 confidence 不与它共用阈值。此适配已有受控 HTTP 测试，尚未声称真实服务验收。
