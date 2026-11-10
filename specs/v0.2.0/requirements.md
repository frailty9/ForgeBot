# ForgeBot v0.2.0 需求草稿

日期：2026-11-10。代码基线：master@d6c95c4（v0.1.0 已合并打标，AGENTS.md 已加入）。

状态：范围已确认（2026-11-10）。待开始实施前需完成设计拆分和任务排序。

---

## 1. 从 v0.1.0 遗留的明确待做项

### 1.1 自动沉淀重构（BOT-14）

**来源**：v0.1.0 明确留到 v0.2.0 的一项。

v0.1.0 的 `native.knowledge_reflection` 有功能实现，但运营效果未验收，且已决定重构后再验。当前实现的结构性问题：

- 知识沉淀用自己的 24 小时候选问题缓冲（`reflection/buffer`），与新建的核心近期消息容器（`internal/history`）重复。用户在上一版本有意设计了核心容器作为"宿主层的通用结构"，知识沉淀后期重构时也能用上。
- 重构方向：改用 `history.Reader` 和 `History` 注册项（`"new message"` / `"message removed"` 事件）替换专用的 24 小时缓冲；`ObserverRegistration` 仍可保留对收消息的直接观察，但触发沉淀的"收到足够问答"判断应基于容器状态，不再独立维护。
- 重构是破坏式的：`internal/reflection/buffer` 可能整体删除或大幅缩减；模块配置字段（如 `scan_interval`、`quiet_window`、`max_messages_per_conversation`、`max_messages_total` 等）可能变化，需同步更新文档和 `config-index.md`。

**决定：只改缓冲层，不动 pipeline 和 publisher。**

- `reflection/pipeline` 和 `internal/knowledge/markdown` 与缓冲无耦合，不在本次重构范围内。
- 删除 `internal/reflection/buffer` 和 `internal/reflection/scanner`，模块改订阅 `history.History` 的 `"new message"` 事件。
- `scan_interval` 字段随 scanner 一起消失，需同步更新文档和 `config-index.md`；其余配置字段（`quiet_window`、`max_session_duration` 等 pipeline 仍需的部分）保留或迁移到新的触发逻辑。
- 重构后运营验收仍需真实群消息窗口，时机与 BOT-14 真实效果验收合并。

---

### 1.2 知识沉淀 Observers 合并（ARCH-P06 旁路清理）

**来源**：上下文中多次提及但未处理。

`internal/runtime/manager.go` 里的 `Builder.ConnectorFactory` / `ConnectorProviders` 旁路和测试注入路径仍保留。用户在上一版本确认"ConnectorProvider 测试注入旁路清理尚未进行"，属于技术债，可与沉淀重构一起处理。

### 1.3 入群审批外部 HTTP 服务接口（新增）

**来源**：本版新提出。

在现有正则 + 模型判断之外，新增第三种判断方式：调用部署者配置的外部 HTTP 服务，由外部服务返回审批结论。

**配置形式（`native.join_review` 模块 options 顶层）**：

```json
{
  "external_review": {
    "url": "https://example.com/review",
    "method": "POST",
    "headers": { "Authorization": "Bearer {secret_ref}" },
    "body": "{ \"application\": \"{application}\", \"user_id\": \"{user_id}\" }",
    "result_expression": ".approved == true"
  }
}
```

- `url`、`method`：必填
- `headers`：可选，键值对，值支持 `{secret_ref}` 形式引用凭据（不直接写密钥）
- `body`：可选，字符串模板，支持 `{application}`、`{user_id}`、`{conversation_id}` 占位符
- `result_expression`：必填，jq 风格路径表达式，对响应 JSON 求值，结果为布尔值即为审批结论；表达式不合法或响应不可解析时视为拒绝并记日志

**与现有判断的组合**：`combine` 字段（`and`/`or`）对三种方式同样生效；外部服务超时或失败时视为 `unknown`，按 `combine` 语义处理（`and` 时拒绝，`or` 时继续其他判断）。

**安全约束**：
- `url` 必须是 HTTPS（本地测试除外），配置加载时校验
- 请求带固定超时（建议 5s），不得无限等待
- 响应体大小有上限（建议 64KB），超出截断后解析
- `body` 模板的占位符替换在序列化后进行，不产生 JSON 注入

---

### 1.4 QQ 官方机器人 Connector（新增）

**来源**：本版新提出。

新增 QQ 官方机器人平台适配器，作为独立 ConnectorProvider。QQ 官方机器人（`qq_official`）与 OneBot（`onebot`）是独立的连接器类型，各自注册，不共享实现。

**背景**：QQ 官方机器人 API（原 QQ频道 Bot API，现已扩展到群/私聊）使用 Webhook 或 WebSocket 接收事件，使用 REST API 发送消息，鉴权方式、消息格式和能力边界与 OneBot 有本质区别。

**待设计的核心问题**：
- 接入方式选型：Webhook（需公网或内网穿透）或 WebSocket 长连接
- 消息类型映射：官方 API 的 `MessageCreate`、`MessageMentionEveryone` 等事件到 Core 规范消息的映射
- 能力边界：官方沙盒审核机制、消息被动回复 vs. 主动推送的差异、群与频道的区别
- 鉴权：`appid` + `secret` 获取 Access Token，热更新时 Token 刷新策略

**验收门槛**：与 OneBot 适配器同等要求——能力目录明确标注支持/不支持的操作，不支持的操作隐式失败或返回 unsupported，不抛停止级别错误；至少在真实账号下完成收消息 → 回答的完整链路验收。

**优先级**：中。可与 BOT-19（消息撤回）并行，但不阻塞沉淀重构（BOT-14/ARCH-P06）。

---

## 2. 功能清单中明确列为"下一版本"的项

### 2.1 消息撤回（BOT-19）

**来源**：`docs/feature-inventory.md` BOT-19。

v0.1.0 挂起原因已消除：OneBot v11 `send_msg` 响应带回 `message_id`（`{"status":"ok","retcode":0,"data":{"message_id":123456}}`），发送层可以把它写入 `internal/history` 对应记录。`internal/history` 已存每条群消息的平台消息 ID，因此管理员触发时可从容器里查到目标 ID。

**方案：Option A — 管理员引用 + `/recall` 命令**

- 可信管理员引用违规消息并发送 `/recall`，Bot 从引用消息 ID 查 `internal/history`，取出平台消息 ID，调 OneBot `delete_msg`。
- Bot 自己发出的消息：发送成功后需将 `send_msg` 返回的 `message_id` 写回 history 条目，管理员引用 Bot 回复也可触发撤回。这同时修复 `tasks.md` 2026-11-09 记录的让位已知缺口（"管理员直接引用 Bot 自己发出的回复消息暂不触发"）。

**前置工作（BOT-19 子任务）**：在 OneBot 适配器发送成功回调里，将 `data.message_id` 写回 `internal/history` 中对应 Bot 消息条目。这是实现层缺口，协议层已具备能力。

**适配器失败语义**：不支持 `delete_msg` 的连接器隐式失败，记一条 DEBUG/WARN 日志或向上返回 runtime error；不得向上抛停止级别的错误，调用方决定是否告知管理员。

---

### 2.2 更完整的平台投递体验（APP-P08）

**来源**：`docs/feature-inventory.md` APP-P08，当前状态"仅讨论 / 路线图"。

**决定：不纳入 v0.2.0，继续留路线图。**

如后期需要多条回复或重试能力，倾向于直接给 LLM 提供发送消息的工具，而不是在宿主层做自动分段和重试策略。

---

## 3. 路线图 E 阶段：自动沉淀完善

路线图把"自动沉淀完善"列为阶段 E，要求"在已有扫描、去重、发布、索引更新基础上补缺口"。v0.1.0 末段提出的具体缺口：

- 无人工审核 / 候选编辑闭环（APP-P07）：当前发布是自动的，没有"候选 → 人工确认 → 发布"流程
- 沉淀质量标志：知道哪些内容是由 Bot 自动生成的，哪些经过人工确认
- 知识库版本与撤销：已发布内容的修订和撤销入口

**决定：APP-P07 不纳入 v0.2.0，继续留路线图。** 人工审核闭环依赖稳定的沉淀输出（BOT-14 重构完成后），也依赖某种运营通知机制，先把沉淀重构做完再设计更合适。

---

## 4. 架构演进项（供版本规划参考）

| ID | 内容 | 来源 | 建议优先级 |
| --- | --- | --- | --- |
| ARCH-P02 | 外部 FPP 插件运行时（握手、加载、生命周期） | 路线图 G | 低；v0.1.0 已明确延后 |
| ARCH-P03 | 公开 Go SDK | 路线图 H | 低；无当前实现，需接口稳定后 |
| ARCH-P06 | Connector 宿主注入旁路清理 | v0.1.0 遗留 | 中；可随沉淀重构一起处理 |
| ARCH-P07 | 请求与投递重试策略 | 路线图 F | 中；配合 APP-P08 |
| ARCH-P08 | MCP / Skill 独立 Manager | 暂缓 | 低；无当前需求 |

---

## 5. 非目标（v0.2.0 明确不做）

以下内容 v0.1.0 已明确延后，v0.2.0 不自动纳入：

- Telegram 或其他平台连接器
- Web 管理面板（OPS-09）
- SQL / NoSQL 存储后端
- 向量检索
- 插件市场、签名、沙箱
- 公开 Go SDK

---

## 6. 范围决定汇总

所有开放问题已关闭。

1. ~~自动沉淀重构范围~~ → 只改缓冲层，详见 §1.1。
2. ~~消息撤回方案~~ → Option A，详见 §2.1。
3. ~~平台投递体验（APP-P08）~~ → 不纳入，详见 §2.2。
4. ~~人工审核闭环（APP-P07）~~ → 不纳入，详见 §3。
5. ~~范围收口~~ → v0.2.0 核心范围：BOT-14 沉淀重构、ARCH-P06 旁路清理、BOT-19 消息撤回、§1.3 入群审批外部 HTTP 接口、§1.4 QQ 官方机器人 Connector，以及对应文档更新。
6. ~~过滤 @全体成员~~ → 取消。OneBot 适配器 `mentions()` 只在 `at` 段 `qq` 等于 Bot 自身 ID 时置 `Mentioned`，@全体（`qq=all`）本就不触发 Bot，无需改动。QQ 官方机器人 Connector 实现时需保持同样语义。
