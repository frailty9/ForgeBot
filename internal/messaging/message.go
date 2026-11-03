// Package messaging defines the protocol-neutral message model shared by
// platform adapters, the bot engine, and outbound senders.
package messaging

import "time"

type MediaKind string

const (
	MediaImage MediaKind = "image"
	MediaAudio MediaKind = "audio"
	MediaVideo MediaKind = "video"
	MediaFile  MediaKind = "file"
)

type Media struct {
	Kind     MediaKind `json:"kind"`
	MimeType string    `json:"mime_type,omitempty"`
	URL      string    `json:"url,omitempty"`
	Data     []byte    `json:"data,omitempty"`
	Name     string    `json:"name,omitempty"`
}

// QuotedMessage is protocol-neutral context resolved from a platform reply.
// It remains separate from the current message so commands, admission, and
// deduplication never interpret quoted content as new user input.
type QuotedMessage struct {
	MessageID string  `json:"message_id"`
	Text      string  `json:"text,omitempty"`
	Media     []Media `json:"media,omitempty"`
}

type InboundMessage struct {
	// PromptSuffix is adapter-provided system-instruction context. It is kept
	// out of the public message JSON surface so callers cannot override it.
	PromptSuffix     string         `json:"-"`
	ConversationID   string         `json:"conversation_id"`
	UserID           string         `json:"user_id"`
	UserName         string         `json:"user_name,omitempty"`
	MessageID        string         `json:"message_id"`
	ReplyToMessageID string         `json:"reply_to_message_id,omitempty"`
	Quoted           *QuotedMessage `json:"quoted,omitempty"`
	Text             string         `json:"text,omitempty"`
	Media            []Media        `json:"media,omitempty"`
	Mentioned        bool           `json:"mentioned,omitempty"`
	Command          bool           `json:"command,omitempty"`
	Administrator    bool           `json:"-"`
	// PlatformRole 仅由可信连接器填写，不能从测试HTTP消息反序列化提权。
	PlatformRole      string    `json:"-"`
	ProactiveRevision uint64    `json:"-"`
	At                time.Time `json:"at,omitempty"`
}

// TrustedResponder 用于人机协作，不等同于 ForgeBot 配置/凭据管理权限。
func (m InboundMessage) TrustedResponder() bool {
	return m.Administrator || m.PlatformRole == "owner" || m.PlatformRole == "admin"
}

func (m InboundMessage) SessionKey() string {
	return m.ConversationID
}

func (m InboundMessage) Addressed() bool {
	return m.Mentioned || m.Command
}

func (m InboundMessage) HasMedia() bool {
	return len(m.Media) > 0 || m.Quoted != nil && len(m.Quoted.Media) > 0
}

func (m InboundMessage) HasQuotedContent() bool {
	return m.Quoted != nil && (m.Quoted.Text != "" || len(m.Quoted.Media) > 0)
}

type OutboundMessage struct {
	ConversationID   string  `json:"conversation_id"`
	ReplyToMessageID string  `json:"reply_to_message_id,omitempty"`
	Text             string  `json:"text,omitempty"`
	Media            []Media `json:"media,omitempty"`
}
