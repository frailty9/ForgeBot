# 群事件与管理操作

## 先把群绑定到 Bot

所有群级功能都写在**逻辑群 ID** 下，而不是平台群号下。逻辑群 ID 在 `platforms.groups` 中定义，把“哪个连接器的哪个群号”映射成一个 Bot 内部名字，其余配置都引用这个名字：

```text
platforms.connectors[].id = "qq-main"          ← 一个 QQ 账号（OneBot 端点）
        ↓ connector_id
platforms.groups[] = {id: "support", connector_id: "qq-main", target: "10001"}
        ↓ 逻辑群 ID "support"（target 10001 是 QQ 群号）
modules.native.join_review.options.groups.support  ← 入群自动审批（规则在模块层统一配置）
modules.native.welcome.options.groups.support  ← 新人欢迎
modules.native.proactive_reply.options.groups.support  ← 主动插话
operator.conversation_id = "ops"               ← 唯一的运维群：运维命令与故障通知
```

完整可加载的 QQ 示例见 [`configs/runtime.onebot.example.json`](../configs/runtime.onebot.example.json)：一个 QQ 账号绑定两个群，`support` 开启入群审批、新人欢迎和主动插话，`ops` 只作为运维群。使用时把 `target` 换成你的 QQ 群号，`user_id` 换成管理员 QQ 号，并配置 OneBot 端点与回答模型。

几条约束：

- 逻辑群 ID 必须先出现在 `platforms.groups` 中。各模块的 `groups`（入群审批、欢迎、主动插话）、沉淀的 `conversation_ids`，以及 `operator.conversation_id` 引用未绑定的 ID 时，配置加载失败并指出是哪个模块的哪个群 ID。模块关闭时也会检查，避免重新开启时才发现写错。
- `connector_id` 必须指向已启用的连接器；启用的 OneBot 连接器至少要绑定一个群。
- 同一个连接器与群号的组合不能重复绑定；一个连接器可以绑定多个群。
- 默认示例 [`runtime.example.json`](../configs/runtime.example.json) 为了能直接在本地运行，只绑定了 Echo 测试群 `local`，QQ 连接器是关闭的。

## 新人欢迎

启用 `native.welcome` 模块并按逻辑群设置模板：

```json
{
  "modules": {
    "native.welcome": {
      "enabled": true,
      "options": {
        "groups": {
          "support": {"enabled": true, "message": "欢迎 {user} 加入！请先阅读群公告。"}
        }
      }
    }
  }
}
```

`message` 可以写成字符串，也可以写成消息段数组，用于 @ 新成员等场景：

```json
"support": {
  "enabled": true,
  "message": [
    {"type": "text", "data": {"text": "欢迎 "}},
    {"type": "at", "data": {"qq": "{user_id}"}},
    {"type": "text", "data": {"text": " 加入！账户、订单、退款等问题请联系 "}},
    {"type": "at", "data": {"qq": "10001"}}
  ]
}
```

消息段的形状沿用 OneBot：`{"type": …, "data": {…}}`。**Bot 不检查段的类型和 `data` 的内容，原样交给 OneBot**，所以 OneBot 实现支持的段（`face`、`image`、`at` 等）都可以写；写错参数或类型，是 OneBot 在发送时报错，Bot 加载配置时发现不了。Bot 只检查：数组不能为空、最多 64 段、每段要有 `type`、不能有拼错的顶层键（如 `dta`）。字符串写法等价于只有一个 `text` 段，纯文本不超过 4096 字节。旧版的 `text` 字段已移除，配置里残留会报未知字段，请改名为 `message`。

**占位符**：`{user}`、`{user_id}`、`{conversation_id}` 会在所有字符串值里替换（包括 `at` 的 `qq`、`image` 的 `file` 等），数字和对象原样保留。替换发生在 JSON 解码之后，昵称里的引号、花括号或 `[CQ:...]` 只是文本，不会改变消息段结构。

**没有保护 `@全体`**：`{"type": "at", "data": {"qq": "all"}}` 会被原样发出，能否 @ 全体取决于 Bot 账号在群里的权限和 OneBot 实现。如果不想让配置误写出 @ 全体，请自行注意。

**不支持消息段的连接器**（如本地 Echo）发送纯文本形式：`text` 段取其 `data.text`，`at` 段写成 `@昵称`（目标是新成员时）或 `@号码`，图片、表情等无法降级的段省略。OneBot 发送的是消息段本身，不使用纯文本形式。

文本里支持 `{user}`、`{user_id}`、`{conversation_id}`，仅做一次确定性替换，不调用模型。OneBot 入群通知只带用户 ID，适配器会用 `get_group_member_info` 查询新成员的群名片（没有则用昵称）填入 `{user}`；查询失败或超时（3 秒）时 `{user}` 退回用户 ID，欢迎语照常发送。欢迎只基于实际 MemberJoined：收到入群事件就发送欢迎语，不做去重、不落盘；申请批准不触发，Bot 自己入群和历史快照不欢迎。默认纯文本，只有在 `message` 里写了 `at` 段才会 @ 人。

欢迎经模块群事件接口返回受限回复意图，宿主投递。关闭模块或群开关不发送；管理员接答让位不影响欢迎，全局 pause 则抑制。发送失败不自动补发。OneBot 始终以结构化段发送，模板或显示名内的 CQ 字面量（如 `[CQ:at,qq=all]`）只会作为文本，不会转换成控制段。

群操作是协议无关的可选接口。v0.1.0 只提供入群申请审核；未实现的连接器返回 unsupported。公告和撤回已挂起，见文末说明。接口支持不代表当前账号具备权限，支持查询目前返回 `permission=unknown`，实际请求结果单独分类。

支持查询与能力目录另列 `join_events` / `member_events` / `join_review_reason`，未实现事件的连接器明确为 false；它们是本地实现声明，不证明远端服务正在上报事件或当前账号有权限。`join_review_reason` 默认为 false，OneBot 实现为 true。

## 入群自动审批

入群审批由 `native.join_review` 模块配置。一个部署对应一个业务，所以**审批规则在模块层统一写一份，所有开启的群共用**；`groups` 只决定哪些群开启。模块启用且群的 `enabled` 为 `true` 时，Bot 按规则自动同意或拒绝该群的入群申请；模块关闭、群关闭或不在列表中时，Bot 不处理入群申请，全部由管理员在原平台处理。

```json
{
  "modules": {
    "native.join_review": {
      "enabled": true,
      "options": {
        "regex": "\\S",
        "model_enabled": false,
        "rule": "申请内容须说明入群目的，并符合本群主题。",
        "combine": "and",
        "reject_reason": "请填写符合本群要求的入群申请说明。",
        "groups": {
          "support": {"enabled": true},
          "support-backup": {"enabled": false}
        }
      }
    }
  }
}
```

只要 `groups` 中列了群（包括关闭的群），规则就必须至少启用一种判断：非空 `regex`、启用模型判断，或两者组合，否则加载配置时报错。不同群需要完全不同的审批标准时，通常说明它们属于不同业务，应分开部署。模块只提供规则；判断、去重、执行前复核与调用连接器都由宿主完成。自动审批直接使用平台事件中的私有句柄，批准不伪造实际成员入群事件。

旧配置中顶层的 `group_management`（含 `auto_review`、`review_regex` 等字段）已删除，残留会导致配置加载失败。迁移方法：把规则字段移到 `modules.native.join_review.options` 下并去掉 `review_` 前缀（`review_model_enabled` 改为 `model_enabled`），删掉 `auto_review`；原来开启审批的群写成 `groups.<群ID>: {"enabled": true}`，并把模块 `enabled` 设为 `true`。原来各群规则不同的，需要合并为一份。

- `regex`：Go regexp（RE2）表达式，匹配**申请内容**；例如 `\S` 要求至少一个非空白字符。不支持前瞻 `(?=...)`、后顾和反向引用。不自动去除空白或补锚点，完整匹配可显式使用 `^...$`。最多 4096 字节，无效表达式在加载/热更新时拒绝。

  OneBot（QQ）的申请内容有两种来源：群没有设置验证问题时，是申请人填写的申请理由；设置了验证问题时，OneBot 服务端会把它拼成 `问题：xxx` 换行 `答案：yyy`。适配器会取出其中的**答案**作为申请内容，所以规则只需要描述答案本身，例如要求恰好 10 位大写字母或数字就写 `^[0-9A-Z]{10}$`，不用考虑“问题：”“答案：”前缀。分隔以第一处“换行加答案：”为准，申请人在答案里再写“答案：”不会改变被匹配的内容。验证问题本身不参与匹配，也不能用来区分不同的问题。没有这个结构的内容（比如申请理由）原样匹配。排查规则为何不匹配时，把日志等级设为 `debug`，会输出原始 `comment` 和提取后的 `application`，审批句柄始终不会输出。
- `model_enabled`：开启后使用共享 `DecideBool(ctx, rule, state)`，判断标准为 `rule`（非空，最多 4096 字节）。DM 已配置且启用时使用 DM，否则使用回答 LLM。每次最多一个模型请求，DM 故障直接返回错误，不转调 LLM。
- `combine`：`and`（默认）或 `or`，始终先执行正则。
- `reject_reason`：固定拒绝理由，最多 2048 字节；适配器不支持时忽略文本，仍执行拒绝，不额外发送聊天消息。

| 组合 | 正则结果 | 后续行为 |
| --- | --- | --- |
| AND | 不通过 | 立即拒绝，不调用模型 |
| AND | 通过 | 调用模型；通过则同意，明确不通过则拒绝 |
| OR | 通过 | 立即同意，不调用模型 |
| OR | 不通过 | 调用模型；通过则同意，明确不通过则拒绝 |

仅启用一种判断时直接使用该项结果。

注意 `regex` 与 `or` 的组合：正则一旦通过就直接同意，不再调用模型。示例里的 `\S` 只要求有任意非空白字符，若配合 `or` 等于几乎全部放行；使用 `or` 时应改成真正反映入群要求的表达式。模型失败、超时或无法判断时均不操作，管理员在原平台查看和处理申请。执行前再次核对当前策略、绑定、暂停和连接器能力，策略改变则放弃旧判断。

省略 `model_enabled` 时，非空 `rule` 表示启用模型；显式 false 可保留规则文本而关闭模型。纯正则审批保持 `model_enabled` 为 false；组合审批再改为 true。

审批不持久化记录，也不提供 Bot 侧人工审批入口。进程内只保留连接器/群/事件 ID 的去重键，最长 24 小时、最多 10000 个；容量耗尽不执行新审批。去重跨热更新保留、重启清空。判断与执行结果写普通日志；网络失败、未知结果不自动重试，管理员可在原平台核实。

## 群管理 API

所有管理 API 需要原有 Bearer 认证：

| 方法与路径 | 行为 |
| --- | --- |
| GET `/v1/admin/groups/support?conversation_id=support` | 查询连接器支持，权限可能未知 |

欢迎语与入群审批都不保存操作记录，也没有查询记录的管理 API。`POST /v1/admin/groups/review` 已移除，人工审批在对应平台执行。

OneBot 当前受控测试支持：

| 能力 | API／事件 | 限制 |
| --- | --- | --- |
| 入群申请 | request/group/add → `set_group_add_request` | 原样使用 flag/sub_type，绑定原群和连接器；invite 未纳入自动入群审核 |
| 成员入群 | notice/group_increase | 与申请事件独立，不从审核结果伪造 |
| 消息撤回 | 宿主共享能力 → `delete_msg` | 传入消息 ID，不依赖 history，不设宿主统一时限；平台决定账号权限和结果 |

协议核对：[OneBot11 请求事件](https://github.com/botuniverse/onebot-11/blob/master/event/request.md)、[SnowLuma 请求操作](https://github.com/SnowLuma/SnowLuma/blob/main/packages/onebot/src/actions/request.ts)，2026-11-03。尚未对用户实际运行的 QQ 账号执行真实审核；受控测试不能替代权限与服务版本的真实验收。

## 宿主单条消息撤回能力

v0.2.0 提供 `group.message_recall.v1`，供宿主服务和内置模块调用。它只执行“撤回指定消息 ID”，不判断违规、不规定何时调用，也不要求消息仍在 history 中。2026-11-10 用户纠正了此前的产品偏差：删除管理员引用 `/recall` 的聊天入口，并撤掉附带的直接引用 Bot 让位增强；管理员手动处置仍直接使用聊天平台自身能力。

消息 ID 可能在不同连接器中重复，因此先按逻辑群取得绑定后的能力，执行时只传消息 ID：

```go
// 模块在 Descriptor.Requires 中声明 group.RecallKey.ID()。
provider, ok := capability.Resolve(build.Capabilities, group.RecallKey)
// ok 应在模块构建边界检查；以下为运行时调用示意。
recall, err := provider.ForConversation("support")
if err != nil {
    return err
}
return recall.Recall(ctx, messageID)
```

宿主也可直接调用 `Manager.ForConversation` 取得同样的小接口，不向模块交付完整 Runtime。操作使用绑定的连接器和平台群目标；执行前复核当前绑定。无关配置热更新后仍可使用原能力；改绑或解绑后原能力返回 conflict，调用方应重新按群取得能力，不会把旧平台消息 ID 发往新目标。全局暂停和运行时关闭仍有效。

能力不要求管理员发起聊天消息，不使用引用关系，不检索或重放历史，也不读取消息正文。宿主不设两分钟撤回限制；消息超过 history TTL、已滑出窗口，或者来自更早的时间，都可按 ID 发起操作。OneBot 直接调用 `delete_msg`，具体权限/平台规则由返回结果表达，不能把某一平台的规则当成所有连接器的统一限制。

支持查询中的 `message_recall` 只表示连接器实现该操作，账号权限独立为 unknown 或已知结果。不支持、无权限、平台拒绝等都作为操作失败返回，不停止整个连接器。请求超时保留“超时 + 执行结果未知”，不自动重试、不自动发群提示。能力层不提供通用去重；调用者根据其业务和副作用确定性决定如何避免重复执行。

本版只注册可调用能力，不默认增加撤回 HTTP API、LLM 工具、定时审查、独立违规模型请求或其他业务触发。后续功能可以在已做的判断中复用它，避免为撤回本身重复付出模型调用成本；这些调用策略不是当前实现。

撤回操作不自动修订 history、短期问答上下文、已捕获的沉淀会话或已发布 Markdown。它不依赖这些容器，也不额外添加 removed 事件或改绑清理 history 的机制。真实平台副作用验收仍待完成。

## 仍挂起：群公告

v0.1.0 移除了公告和撤回，v0.2.0 仅恢复上述基础撤回能力。OneBot11 标准没有群公告接口，此前依赖的 `_send_group_notice` 是部分服务端扩展，公告保持挂起。
