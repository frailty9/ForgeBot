// Package history 是核心层的近期消息容器：按群保存最近的群消息（收到的和 Bot 发出的），
// 逐条滑动，只在进程内存中，不落盘。它没有开关，模块通过只读能力读取，并可订阅
// “新消息进入”和“消息移出”两种事件。
package history

import "time"

type Role string

const (
	RoleMember Role = "member"
	RoleAdmin  Role = "admin"
	RoleBot    Role = "bot"
)

// Record 是容器里的一条消息。Seq 在群内单调递增，消息移出后也不复用，模块可用
// “群 + Seq”稳定指向一条记录。Bot 发出的消息目前没有平台消息 ID（MessageID 为空）。
// At 是宿主收到或发出的时间，保证同群内按 Seq 单调，不使用平台时间。
type Record struct {
	Seq            uint64 `json:"seq"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id,omitempty"`
	ReplyTo        string `json:"reply_to,omitempty"`
	// QuotedMessageID/QuotedText 保存适配器已解析的引用原文，不触发额外查询。
	// 文本沿用容器字符上限；纯值字段保持 Reader 和事件的副本语义。
	QuotedMessageID string `json:"quoted_message_id,omitempty"`
	QuotedText      string `json:"quoted_text,omitempty"`
	// QuotedFromBot 表示被引用的是本 Bot 的消息；TextTruncated/QuotedTruncated 表示正文超过
	// max_text_chars 已被截断，消费方不能把它当作完整原文。
	QuotedFromBot   bool      `json:"quoted_from_bot,omitempty"`
	TextTruncated   bool      `json:"text_truncated,omitempty"`
	QuotedTruncated bool      `json:"quoted_truncated,omitempty"`
	UserID          string    `json:"user_id,omitempty"`
	UserName        string    `json:"user_name,omitempty"`
	Role            Role      `json:"role"`
	Text            string    `json:"text,omitempty"`
	HasMedia        bool      `json:"has_media,omitempty"`
	Mentioned       bool      `json:"mentioned,omitempty"`
	Command         bool      `json:"command,omitempty"`
	At              time.Time `json:"at"`
}

type EvictReason string

const (
	// EvictCapacity：群内条数达到上限，最旧的一条被挤出。
	EvictCapacity EvictReason = "capacity"
	// EvictExpired：超过保留时长，在下次写入时顺带清理。
	EvictExpired EvictReason = "expired"
	// EvictConfig：热更新调小了条数上限。
	EvictConfig EvictReason = "config"
)

// Subscriber 由模块注册，宿主只投递给当前一代模块。回调在写入完成后、容器锁之外同步调用，
// 同一容器内按写入顺序依次投递；回调必须很快返回，耗时工作由模块自己转到后台。
// 回调里的 panic 会被隔离并记录日志，不影响其他订阅者和容器。
type Subscriber struct {
	ID       string
	Appended func(Record)
	Evicted  func(conversationID string, records []Record, reason EvictReason)
}
