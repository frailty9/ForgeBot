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

首次使用需在静态 YAML 设置文件后端的运营仓储（路径相对 YAML）：

```yaml
group_store_path: ../data/group-operations.json
```

运行配置按逻辑群启用能力，默认全部关闭：

```json
{
  "group_management": {
    "support": {
      "review_enabled": true,
      "announcements": true,
      "recall": true,
      "auto_review": false
    }
  }
}
```

申请默认进入待人工记录。只有同时开启 `review_enabled` / `auto_review` 并配置非空 `review_rule` 才允许自动审核；共享 Decision 只提供 approve/reject/unknown 建议，宿主再检查当前配置和申请状态。不确定或模型失败保留人工处理。审批通过与实际成员入群是两种独立事件。

所有管理 API 需要原有 Bearer 认证：

| 方法与路径 | 行为 |
| --- | --- |
| GET `/v1/admin/groups/support?conversation_id=support` | 查询连接器支持，权限可能未知 |
| GET `/v1/admin/groups/operations?limit=100` | 查询申请与操作记录；私有审核句柄不公开 |
| POST `/v1/admin/groups/review` | `{"id":"记录ID","expected_revision":1,"approve":true,"reason":"理由"}` |
| POST `/v1/admin/groups/announcements` | `{"id":"唯一操作ID","conversation_id":"support","text":"公告正文"}` |
| POST `/v1/admin/groups/recall` | `{"id":"唯一操作ID","conversation_id":"support","message_id":"平台消息引用"}` |

公告与撤回要求部署者提供唯一 ID。同一 ID 和相同内容已成功时返回现有结果，不再次执行；相同 ID 改变内容返回 conflict。审核采用申请 revision 条件更新，不能用另一个动作 ID 重复审批。

执行前记录 executing，执行后记录 completed/failed/unknown。网络超时、远端无法确认或提交结果失败都不能自动重试；进程重启后 executing 转为 unknown，等待部署者核实。默认每份运营文件最多 10000 条、32MiB，达到上限报 busy，不静默删幂等记录。停机后归档与清理须保留需要防重的记录；本版未提供在线自动清理。

OneBot 当前受控测试支持：

| 能力 | API／事件 | 限制 |
| --- | --- | --- |
| 入群申请 | request/group/add → `set_group_add_request` | 原样使用 flag/sub_type，绑定原群和连接器；invite 未纳入自动入群审核 |
| 成员入群 | notice/group_increase | 与申请事件独立，不从审核结果伪造 |
| 文本公告 | `_send_group_notice` | SnowLuma 扩展；非 OneBot11 标准保证，不支持的服务会报错 |
| 撤回消息 | `get_msg` 核实群 → `delete_msg` | 不允许跨群撤回；服务不返回群归属时返回 unsupported |

协议核对：[OneBot11 请求事件](https://github.com/botuniverse/onebot-11/blob/master/event/request.md)、[SnowLuma 请求操作](https://github.com/SnowLuma/SnowLuma/blob/main/packages/onebot/src/actions/request.ts)、[公告扩展](https://github.com/SnowLuma/SnowLuma/blob/main/packages/onebot/src/actions/extended.ts)，2026-11-03。尚未对用户实际运行的 QQ 账号执行真实审核、公告或撤回；受控测试不能替代权限与服务版本的真实验收。
