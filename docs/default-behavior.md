# 默认行为与验收边界

2026-11-10 更新 v0.2.0 沉淀行为，其余沿用 v0.1.0 记录。下面列出实现中的默认行为与测试证据；测试通过不等于真实平台或供应商已经验收。

| 场景 | 默认行为 | 配置或约束 | 主要证据 |
| --- | --- | --- | --- |
| 正式运行缺模型／回答凭据 | 启动或候选构建失败，不回显代答 | 匿名需 authentication=none | [模型检查](../internal/runtime/model_required_test.go) |
| 空 @ | 回复体验模块补自我介绍提示，关闭模块后正常忽略 | native.reply_experience | [问答测试](../internal/bot/engine_test.go) |
| 图片能力关闭 | 已触发的纯图片/图文/引用图片直接提示不支持，不调用模型或工具 | llm.image_understanding=false | [图片策略](../internal/bot/image_policy_test.go) |
| 未 @ 图片 | 不因图片开关额外回复 | 独立准入策略 | [图片策略](../internal/bot/image_policy_test.go) |
| 引用失效 | 不虚构引用内容，保留当前有效输入；空输入按防护处理 | OneBot get_msg 与输入准备 | [OneBot](../internal/platform/onebot/onebot_test.go) |
| 平台表情 | 可靠描述转文字，未知ID忽略；字面转义不重解析 | 适配层负责 | [表情](../internal/platform/onebot/emoji_test.go) |
| 消息重复 | TTL 内按消息身份去重 | admission | [去重](../internal/admission/admission_test.go) |
| 业务范围（@） | 不做预分类，由回答模型按部署者的系统提示词决定是否实质解答 | bot.system_prompt；属于尽力遵守 | [@ 路径](../internal/bot/decision_test.go) |
| 主动插话决策 | 模块先做一次决策，判断是否值得回答并属于模块 scope；仅明确 yes 才交回答模型，回答模型仍可输出空内容放弃 | native.proactive_reply.scope；一次只选 DM 或 LLM | [模块决策](../internal/modules/proactivereply/module_test.go)、[空回复抑制](../internal/bot/decision_test.go) |
| 无专用 DM | 独立使用回答 LLM 作有界决策 | 无工具/Skill/聊天记忆污染 | [无DM](../internal/runtime/decision_test.go) |
| 检索时机 | 由模型按工具描述与系统提示词选择是否检索，宿主不预检索 | 建议在 system_prompt 写明何时必须检索 | [按需检索](../internal/bot/engine_test.go) |
| 检索无结果 | 成功空结果，不捏造资料 | Tool 结果语义 | [知识工具](../internal/tooling/searchdocs/tool_test.go) |
| 工具多轮依赖 | 上轮结果可作为下轮参数依据 | 串行、有界、调用ID关联 | [循环](../internal/agent/loop_test.go) |
| 超时／预算耗尽 | 明确终止，不后台无限重试 | agent与单次模型时限共同生效 | [循环](../internal/agent/loop_test.go) |
| 预回复失败 | 同一 run 只尝试一次，可继续最终处理 | 仍受Hook与总时限限制 | [Hook](../internal/bot/hooks_test.go) |
| 短期追问 | 群+用户隔离，发送确认成功后提交 | TTL/容量/顺序队列；重启进程清空 | [记忆](../internal/conversation/memory_test.go) |
| 发送失败／执行未知 | 不把回答写入记忆，不盲目重发 | effect独立于错误分类 | [投递提交](../internal/bot/memory_test.go) |
| 管理员接答 | 群级暂停主动插话，@仍走正常范围判断 | 默认5m，仅引用命中待答或明确语义接答 | [主动模块](../internal/modules/proactivereply/module_test.go) |
| 让位后到期 | 新问题可处理，旧批次不补发 | revision抑制旧在途回复 | [让位状态](../internal/proactive/yield_test.go) |
| 管理员事后纠正 | 管理员引用 Bot 在让位时长内刚回答过的问题，同样让位；未引用时由主动插话决策结合近期群聊判断管理员是否已接手 | 仅群主/管理员；引用无关消息、普通成员或超时均不触发；不增加模型调用 | [回归测试](../internal/modules/proactivereply/regression_test.go) |
| 核心近期消息容器 | 始终启用：按群保存最近收到的消息和 Bot 发送成功的消息，逐条滑动 | 每群 50 条、30 分钟、单条 1000 字；只记已绑定的群，只在内存中，不存图片，重启清空 | [容器](../internal/history/window_test.go)、[宿主接入](../internal/runtime/history_test.go) |
| 配置更新失败 | 保持原运行代，持久化恢复错误可诊断 | expected_version与候选构建 | [运行时](../internal/runtime/manager_test.go) |
| 不支持群操作 | unsupported；权限未知不能伪装成功。公告仍挂起，撤回按可选能力执行 | 可选接口 | [群操作路由](../internal/platform/groups.go) |
| Telegram 群聊 | 长轮询，明确 @/回复 Bot/命令走现有处理；纯文本收发和宿主撤回 | 默认关闭；仅绑定普通群/非论坛超级群；超长拒绝，无媒体/欢迎/Webhook；真实验收待补 | [受控链路](../internal/platform/telegram/integration_test.go)、[接入说明](telegram.md) |
| 单条消息撤回能力 | 按群取得 capability 后传消息 ID 执行；不依赖 history/引用/发言角色，不做违规判断或群内提示 | 当前绑定和全局暂停有效；不设统一两分钟限制，无自动重试或业务入口 | [能力路由](../internal/runtime/recall_test.go)、[OneBot](../internal/platform/onebot/recall_test.go) |
| 自动入群审批 | 正则与模型支持 AND/OR 短路，不持久化审批记录 | 默认关闭；仅内存事件键防重，跨热更新保留、重启清空 | [审批测试](../internal/group/review_test.go) |
| 自动审核不确定 | 不同意也不拒绝，管理员在原平台处理 | 模型错误不映射为 false；无后备请求 | [审批测试](../internal/group/review_test.go) |
| 外部 HTTP 入群审批 | 正则 → HTTP → 模型短路；AND 遇 false/unknown 拒绝，OR 继续直到有 true，否则拒绝；取消不操作 | 仅配置 external_review 后适用；5s/64KiB，无重定向；不配置外部时保留原模型故障语义 | [外部审批](../internal/group/external_review_test.go) |
| 新人欢迎 | 收到入群事件即发送；忽略自身/历史事件；不去重、不落盘 | 默认关闭，群模板；全局暂停有效 | [欢迎测试](../internal/runtime/groups_test.go) |
| 沉淀答题会话 | 仅管理员引用锚定后保存材料，同一管理员继续答复；静默后由定时任务分批处理 | 模块自己的周期、TTL 和容量；不保留全部普通消息候选池 | [归组与边界](../internal/modules/knowledgereflection/sessions_test.go) |
| 沉淀原问题已滑出 | 先查同群 history，再使用适配器已带回的引用原文；两处没有正文才放弃；已捕获会话不随 history 滑动失效 | 只订阅新消息，不钉住记录或依赖移出事件 | [会话快照](../internal/modules/knowledgereflection/sessions_test.go)、[引用文本](../internal/history/quote_test.go) |
| 自动沉淀生成失败 | 保留未过期会话，后续批次再处理；低置信度、拒绝和重复结束会话 | native.knowledge_reflection；换代取消，不重放旧回答 | [批处理与取消](../internal/modules/knowledgereflection/sessions_test.go)、[流水线](../internal/reflection/pipeline/pipeline_test.go) |
| 能力目录 | 展示投影，not_probed不冒充健康 | 认证API与CLI同源 | [目录](../internal/runtime/catalog_test.go) |
| 初次使用 | init不覆盖；doctor不要求先启动Bot | 远程与stdio探测须显式 | [CLI](../cmd/forgebot/commands_test.go) |

2026-11-09 已通过真实验收：入群审批、实际成员入群欢迎（含 @）、init 到启动的教程、目标回答模型对范围提示词与工具检索的遵守、专用 DM、管理员接答让位。尚待真实验收：自动沉淀的实际运营效果（需真实群消息窗口）。暂未实现分类自动重试、群消息合并转发和长回复分段，不将一次 `Send` 成功推论为平台最终可见的 exactly-once 保证。
