# 默认行为与验收边界

2026-11-04，feat/v0.1.0。下面列出实现中的默认行为与测试证据；测试通过不等于真实平台或供应商已经验收。

| 场景 | 默认行为 | 配置或约束 | 主要证据 |
| --- | --- | --- | --- |
| 正式运行缺模型／回答凭据 | 启动或候选构建失败，不回显代答 | 匿名需 authentication=none | [模型检查](../internal/runtime/model_required_test.go) |
| 空 @ | 回复体验模块补自我介绍提示，关闭模块后正常忽略 | native.reply_experience | [问答测试](../internal/bot/engine_test.go) |
| 图片能力关闭 | 已触发的纯图片/图文/引用图片直接提示不支持，不调用模型或工具 | llm.image_understanding=false | [图片策略](../internal/bot/image_policy_test.go) |
| 未 @ 图片 | 不因图片开关额外回复 | 独立准入策略 | [图片策略](../internal/bot/image_policy_test.go) |
| 引用失效 | 不虚构引用内容，保留当前有效输入；空输入按防护处理 | OneBot get_msg 与输入准备 | [OneBot](../internal/platform/onebot/onebot_test.go) |
| 平台表情 | 可靠描述转文字，未知ID忽略；字面转义不重解析 | 适配层负责 | [表情](../internal/platform/onebot/emoji_test.go) |
| 消息重复 | TTL 内按消息身份去重 | admission | [去重](../internal/admission/admission_test.go) |
| 域外专业问题 | 配置范围后拒答，即使明确 @ 或问题简单 | answer_policy.scope | [范围判断](../internal/bot/decision_test.go) |
| 范围未知／决策失败 | 直接问题澄清，主动介入沉默 | 一次只选 DM 或 LLM，故障不转调模型 | [单次决策](../internal/decision/routing_test.go) |
| 无专用 DM | 独立使用回答 LLM 作有界决策 | 无工具/Skill/聊天记忆污染 | [无DM](../internal/runtime/decision_test.go) |
| 简单问题 | 默认由模型选择是否用工具，不每条消息预检索 | 未绑定 evidence 时无强制预检索 | [按需检索](../internal/bot/engine_test.go) |
| 检索无结果 | 成功空结果，不捏造资料 | Tool 结果语义 | [知识工具](../internal/tooling/searchdocs/tool_test.go) |
| 工具多轮依赖 | 上轮结果可作为下轮参数依据 | 串行、有界、调用ID关联 | [循环](../internal/agent/loop_test.go) |
| 超时／预算耗尽 | 明确终止，不后台无限重试 | agent与单次模型时限共同生效 | [循环](../internal/agent/loop_test.go) |
| 预回复失败 | 同一 run 只尝试一次，可继续最终处理 | 仍受Hook与总时限限制 | [Hook](../internal/bot/hooks_test.go) |
| 短期追问 | 群+用户隔离，发送确认成功后提交 | TTL/容量/顺序队列；重启进程清空 | [记忆](../internal/conversation/memory_test.go) |
| 发送失败／执行未知 | 不把回答写入记忆，不盲目重发 | effect独立于错误分类 | [投递提交](../internal/bot/memory_test.go) |
| 管理员接答 | 群级暂停主动插话，@仍走正常范围判断 | 默认5m，仅引用命中待答或明确语义接答 | [主动模块](../internal/modules/proactivereply/module_test.go) |
| 让位后到期 | 新问题可处理，旧批次不补发 | revision抑制旧在途回复 | [让位状态](../internal/proactive/yield_test.go) |
| 配置更新失败 | 保持原运行代，持久化恢复错误可诊断 | expected_version与候选构建 | [运行时](../internal/runtime/manager_test.go) |
| 不支持群操作 | unsupported；权限未知不能伪装成功 | 可选接口 | [群操作路由](../internal/platform/groups.go) |
| 重复公告／撤回 | CAS/幂等ID防重复；未知结果需核实 | 单一运营Store | [运营测试](../internal/group/filestore/service_test.go) |
| 自动入群审批 | 正则与模型支持 AND/OR 短路，不持久化审批记录 | 默认关闭；仅内存事件键防重，跨热更新保留、重启清空 | [审批测试](../internal/group/review_test.go) |
| 自动审核不确定 | 不同意也不拒绝，管理员在原平台处理 | 模型错误不映射为 false；无后备请求 | [审批测试](../internal/group/review_test.go) |
| 新人欢迎 | 实际入群后去重发送；忽略自身/历史/过旧事件 | 默认关闭，群模板；全局暂停有效 | [欢迎测试](../internal/runtime/groups_test.go) |
| 自动沉淀生成失败 | 不发布文档，已有归组/扫描/发布链路保留 | native.knowledge_reflection | [沉淀](../internal/reflection/pipeline/pipeline_test.go) |
| 能力目录 | 展示投影，not_probed不冒充健康 | 认证API与CLI同源 | [目录](../internal/runtime/catalog_test.go) |
| 初次使用 | init不覆盖；doctor不要求先启动Bot | 远程与stdio探测须显式 | [CLI](../cmd/forgebot/commands_test.go) |

尚待真实验收：当前 QQ 服务版本与账号的申请审核、公告扩展、撤回权限、实际成员入群事件，以及专用 DM 实际账户和模型版本。自动沉淀的实际运营效果也需真实群消息窗口。暂未实现分类自动重试、群消息合并转发和长回复分段，不将一次 `Send` 成功推论为平台最终可见的 exactly-once 保证。
