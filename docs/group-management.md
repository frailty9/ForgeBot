# 群事件与管理操作

## 新人欢迎

启用 `native.welcome` 模块并按逻辑群设置模板：

```json
{
  "modules": {
    "native.welcome": {
      "enabled": true,
      "options": {
        "groups": {
          "support": {"enabled": true, "text": "欢迎 {user} 加入！请先阅读群公告。"}
        }
      }
    }
  }
}
```

支持 `{user}`、`{user_id}`、`{conversation_id}`，仅做一次确定性替换，不调用模型。OneBot 入群事件缺少显示名时用用户 ID。欢迎只基于实际 MemberJoined，申请批准不触发；Bot 自己、历史快照和超过 5 分钟的旧事件不欢迎。默认纯文本，不自动 @ 新人。

欢迎使用同一个 `group_store_path` 运营仓储防重，经模块群事件接口返回受限回复意图，宿主投递。关闭模块或群开关不发送；管理员接答让位不影响欢迎，全局 pause 则抑制。发送失败或未知不自动补发。OneBot 以结构化文本段发送，模板或显示名内的 CQ 字面量不会转换成控制段。

群操作是协议无关的可选接口。连接器可以分别实现申请审核、公告发布和消息撤回；未实现时返回 unsupported。接口支持不代表当前账号具备权限，支持查询目前返回 `permission=unknown`，实际请求结果单独分类。

支持查询与能力目录另列 `join_events` / `member_events` / `join_review_reason`，未实现事件的连接器明确为 false；它们是本地实现声明，不证明远端服务正在上报事件或当前账号有权限。`join_review_reason` 默认为 false，OneBot 实现为 true。

欢迎、公告和撤回的防重需要在静态 YAML 设置运营仓储（路径相对 YAML）。入群自动审批不依赖此仓储：

```yaml
group_store_path: ../data/group-operations.json
```

运行配置按逻辑群启用能力，默认全部关闭：

```json
{
  "group_management": {
    "support": {
      "review_enabled": false,
      "auto_review": false,
      "review_regex": "\\S",
      "review_model_enabled": false,
      "review_rule": "申请内容须说明入群目的，并符合本群主题。",
      "review_combine": "and",
      "reject_reason": "请填写符合本群要求的入群申请说明。",
      "announcements": false,
      "recall": false
    }
  }
}
```

## 入群自动审批

同时开启 `review_enabled` / `auto_review` 后，至少启用一种判断：非空 `review_regex`、启用模型判断，或两者组合。仅启用 `review_enabled` 不会保存申请或自动审批。自动审批直接使用平台事件中的私有句柄，批准不伪造实际成员入群事件。

- `review_regex`：Go regexp 表达式，匹配申请原文；例如 `\S` 要求至少一个非空白字符。不自动去除空白或补锚点，完整匹配可显式使用 `^...$`。最多 4096 字节，无效表达式在加载/热更新时拒绝。
- `review_model_enabled`：开启后使用共享 `DecideBool(ctx, rule, state)`，判断标准为 `review_rule`（非空，最多 4096 字节）。DM 已配置且启用时使用 DM，否则使用回答 LLM。每次最多一个模型请求，DM 故障直接返回错误，不转调 LLM。
- `review_combine`：`and`（默认）或 `or`，始终先执行正则。
- `reject_reason`：固定拒绝理由，最多 2048 字节；适配器不支持时忽略文本，仍执行拒绝，不额外发送聊天消息。

| 组合 | 正则结果 | 后续行为 |
| --- | --- | --- |
| AND | 不通过 | 立即拒绝，不调用模型 |
| AND | 通过 | 调用模型；通过则同意，明确不通过则拒绝 |
| OR | 通过 | 立即同意，不调用模型 |
| OR | 不通过 | 调用模型；通过则同意，明确不通过则拒绝 |

仅启用一种判断时直接使用该项结果。模型失败、超时或无法判断时均不操作，管理员在原平台查看和处理申请。执行前再次核对当前策略、绑定、暂停和连接器能力，策略改变则放弃旧判断。

旧配置省略 `review_model_enabled` 时，非空 `review_rule` 表示启用模型；显式 false 可保留规则文本而关闭模型。上例保留完整参数并关闭功能。纯正则审批仅需开启两个总开关，保持模型开关为 false；组合审批再将模型开关改为 true。

审批不持久化记录，也不提供 Bot 侧人工审批入口。进程内只保留连接器/群/事件 ID 的去重键，最长 24 小时、最多 10000 个；容量耗尽不执行新审批。去重跨热更新保留、重启清空。判断与执行结果写普通日志；网络失败、未知结果不自动重试，管理员可在原平台核实。

## 其他群管理 API

所有管理 API 需要原有 Bearer 认证：

| 方法与路径 | 行为 |
| --- | --- |
| GET `/v1/admin/groups/support?conversation_id=support` | 查询连接器支持，权限可能未知 |
| GET `/v1/admin/groups/operations?limit=100` | 查询欢迎、公告和撤回等运营记录；不生成新的审批记录 |
| POST `/v1/admin/groups/announcements` | `{"id":"唯一操作ID","conversation_id":"support","text":"公告正文"}` |
| POST `/v1/admin/groups/recall` | `{"id":"唯一操作ID","conversation_id":"support","message_id":"平台消息引用"}` |

公告与撤回要求部署者提供唯一 ID。同一 ID 和相同内容已成功时返回现有结果，不再次执行；相同 ID 改变内容返回 conflict。`POST /v1/admin/groups/review` 已移除，人工审批在对应平台执行。

公告、撤回和欢迎投递执行前记录 executing，执行后记录 completed/failed/unknown。网络超时、远端无法确认或提交结果失败都不能自动重试；进程重启后 executing 转为 unknown，等待部署者核实。默认每份运营文件最多 10000 条、32MiB，达到上限报 busy，不静默删幂等记录。停机后归档与清理须保留需要防重的记录；本版未提供在线自动清理。

OneBot 当前受控测试支持：

| 能力 | API／事件 | 限制 |
| --- | --- | --- |
| 入群申请 | request/group/add → `set_group_add_request` | 原样使用 flag/sub_type，绑定原群和连接器；invite 未纳入自动入群审核 |
| 成员入群 | notice/group_increase | 与申请事件独立，不从审核结果伪造 |
| 文本公告 | `_send_group_notice` | SnowLuma 扩展；非 OneBot11 标准保证，不支持的服务会报错 |
| 撤回消息 | `get_msg` 核实群 → `delete_msg` | 不允许跨群撤回；服务不返回群归属时返回 unsupported |

协议核对：[OneBot11 请求事件](https://github.com/botuniverse/onebot-11/blob/master/event/request.md)、[SnowLuma 请求操作](https://github.com/SnowLuma/SnowLuma/blob/main/packages/onebot/src/actions/request.ts)、[公告扩展](https://github.com/SnowLuma/SnowLuma/blob/main/packages/onebot/src/actions/extended.ts)，2026-11-03。尚未对用户实际运行的 QQ 账号执行真实审核、公告或撤回；受控测试不能替代权限与服务版本的真实验收。
