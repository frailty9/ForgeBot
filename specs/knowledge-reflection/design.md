# 定时扫描反思设计草案

## 1. 核心流程

```text
已完成身份解析的群消息
        |
        v
Reflection Observer -> bounded in-memory Buffer
                              |
                         periodic scan
                              |
            reply relation + administrator gate
                              |
                              v
                 Reflector (classify/normalize)
                              |
                    deduplicate + validate
                              |
                              v
             Managed Publisher -> extra/*.md
                              |
                    one reload per batch
```

扫描不是平台历史抓取，也不是定时遍历全部聊天记录。它只扫描 ForgeBot 进程内短期观察窗口，因此满足无聊天历史存储的约束。

## 2. 模块边界

- `internal/reflection/buffer`：按 `conversation_id + message_id` 保存短期消息，维护回复反向关系、TTL、容量和扫描水位；
- `internal/reflection/scanner`：单飞定时任务，快照本轮未处理消息，执行规则过滤和候选编排；
- `internal/reflection/reflector`：协议中立接口，输入一个明确问答对，输出 `accept/title/question/answer/tags/confidence/reason`；
- `internal/knowledge`：知识条目和发布结果契约；
- `internal/knowledge/markdown`：只能管理 `extra` 子树的原子 Markdown 发布器；
- `internal/app`：长期持有 Buffer 与 Scanner，并连接 Runtime 的当前反思器、管理员标记和 Search reload 能力。

Scanner 不放进 Bot Engine generation。配置热切换可以替换策略或反思器，但不会意外丢失仍在 TTL 内的观察窗口；进程重启则明确清空。

## 3. 暂存数据

每条观察消息只保留：逻辑群组 ID、平台用户 ID、消息 ID、引用消息 ID、文本、管理员标记和时间。媒体只记录存在性，不下载内容。以下消息不进入缓冲：未启用群组、命令、空文本、Bot 自身事件以及超过单条字符限制的内容。

建议首版默认：

- 扫描周期 `15m`；
- 消息 TTL `24h`；
- 每群最多 `1000` 条；
- 全局最多 `10000` 条；
- 单条最多 `4000` rune；
- 每批最多反思 `50` 对；
- 同时只运行一个扫描批次。

扫描水位不能简单标记“消息已看过即永久忽略”。普通问题在管理员回答到来前仍须保留；管理员答案在成功发布、确定拒绝或超过 TTL 后才进入终态。可重试错误使用有限退避，不在每个周期无限调用 LLM。

## 4. 候选识别与反思

首版确定性候选条件：

1. 答案作者在该逻辑群组中被标记为管理员；
2. 答案带 `ReplyToMessageID`；
3. 被引用消息仍在同一群组缓冲内且作者不是管理员；
4. 问题和答案均不是命令且满足长度限制。

这些条件通过后才调用 Reflector。Reflector 使用独立、无工具的 LLM 请求和结构化输出，不接入普通 Bot 对话，不允许调用 `search_docs` 以外的任何能力。它负责判断回答是否具有长期复用价值、是否足够完整，并生成规范 Markdown 内容。群消息始终作为不可信数据包裹，不能覆盖反思规则。

发布前可用规范化问答哈希做精确去重，并用结构化检索寻找近似主题供 Reflector 判断是否跳过。检索结果只用于去重决策，不允许反向修改人工文档。

## 5. 发布格式与安全

文件名使用稳定来源摘要，例如 `extra/<sha256-prefix>.md`。同一问答重复投递会定位到同一路径。文件包含受控 frontmatter：

```yaml
---
title: API Key 如何轮换
tags: [api-key, console]
managed_by: forgebot
source_conversation_id: support
source_question_message_id: "123"
source_answer_message_id: "124"
created_at: 2026-09-12T08:00:00Z
---
```

正文只保存整理后的问题与答案，不保留整段聊天上下文或用户名。发布器固定根目录的最终路径，拒绝符号链接、reparse point、路径穿越和非 `.md` 后缀；使用同目录临时文件、flush、关闭和原子 rename。它不作为 LLM 工具暴露。

人工文档和 `extra` 之外的文件永远只读。后续若支持撤销，也只能操作带 `managed_by: forgebot` 标记的 `extra/*.md`。

## 6. 批次与失败语义

Scanner 先完成本批次所有候选判断，再逐项原子发布。只要至少发布一项，批次末尾调用一次 `runtime.Manager.ReloadSearch`：

- 全部写入成功且 reload 成功：候选进入 `published` 终态；
- 某项写入失败：仅该项保留为可重试，其他项不回滚；
- 写入成功但 reload 失败：文档保留，旧索引继续服务，发送通知；下一批先尝试 reload 再处理新发布；
- 配置在扫描中变化：本轮使用开始时的不可变策略快照；禁用后发布步骤再次检查开关并中止。

状态只暴露缓冲数量、候选/发布/跳过/失败计数、最近扫描时间和错误摘要，不暴露原始问答正文。

## 7. 热更新配置建议

```json
{
  "reflection": {
    "enabled": false,
    "mode": "auto",
    "conversation_ids": ["support"],
    "scan_interval": "15m",
    "message_ttl": "24h",
    "max_messages_per_conversation": 1000,
    "max_messages_total": 10000,
    "max_message_chars": 4000,
    "max_pairs_per_scan": 50,
    "min_confidence": 0.9,
    "extra_directory": "extra"
  }
}
```

`extra_directory` 必须是单个相对目录名，默认且建议固定为 `extra`。它最终解析到当前 Search `document_root` 下；Search 未启用、根目录不可用或 `.md` 未列入允许扩展名时，Reflection 配置不得启用。

首版建议只实现 `off/auto`，强关联管理员回复满足高置信度才自动发布。审核工作流需要候选持久化和管理 UI，会引入新的产品边界，可在 Web 管理面板阶段单独设计，而不伪装成无状态功能。

## 8. 需要确认的产品决策

1. 默认扫描周期是否采用 `15m`；
2. 首版是否只实现 `auto`，并严格限定管理员显式引用回复；
3. 是否允许在 Markdown frontmatter 保存群组与消息 ID 作为可追溯来源；
4. 反思整理是否复用当前 LLM 配置，还是允许独立选择更便宜的模型。
