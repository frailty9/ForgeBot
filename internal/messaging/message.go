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
	Platform  string    `json:"platform"`
	ChatID    string    `json:"chat_id"`
	UserID    string    `json:"user_id"`
	UserName  string    `json:"user_name,omitempty"`
	MessageID string    `json:"message_id"`
	Text      string    `json:"text,omitempty"`
	Media     []Media   `json:"media,omitempty"`
	Mentioned bool      `json:"mentioned,omitempty"`
	Command   bool      `json:"command,omitempty"`
	Group     bool      `json:"group,omitempty"`
	At        time.Time `json:"at,omitempty"`
}

func (m InboundMessage) SessionKey() string {
	return m.Platform + ":" + m.ChatID
}

func (m InboundMessage) Addressed() bool {
	return m.Mentioned || m.Command || !m.Group
}

func (m InboundMessage) HasMedia() bool {
	return len(m.Media) > 0
}

type OutboundMessage struct {
	Platform         string  `json:"platform"`
	ChatID           string  `json:"chat_id"`
	UserID           string  `json:"user_id,omitempty"`
	ReplyToMessageID string  `json:"reply_to_message_id,omitempty"`
	Text             string  `json:"text,omitempty"`
	Media            []Media `json:"media,omitempty"`
}
