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

type InboundMessage struct {
	// PromptSuffix is adapter-provided system-instruction context. It is kept
	// out of the public message JSON surface so callers cannot override it.
	PromptSuffix     string    `json:"-"`
	ConversationID   string    `json:"conversation_id"`
	UserID           string    `json:"user_id"`
	UserName         string    `json:"user_name,omitempty"`
	MessageID        string    `json:"message_id"`
	ReplyToMessageID string    `json:"reply_to_message_id,omitempty"`
	Text             string    `json:"text,omitempty"`
	Media            []Media   `json:"media,omitempty"`
	Mentioned        bool      `json:"mentioned,omitempty"`
	Command          bool      `json:"command,omitempty"`
	Administrator    bool      `json:"-"`
	At               time.Time `json:"at,omitempty"`
}

func (m InboundMessage) SessionKey() string {
	return m.ConversationID
}

func (m InboundMessage) Addressed() bool {
	return m.Mentioned || m.Command
}

func (m InboundMessage) HasMedia() bool {
	return len(m.Media) > 0
}

type OutboundMessage struct {
	ConversationID   string  `json:"conversation_id"`
	ReplyToMessageID string  `json:"reply_to_message_id,omitempty"`
	Text             string  `json:"text,omitempty"`
	Media            []Media `json:"media,omitempty"`
}
