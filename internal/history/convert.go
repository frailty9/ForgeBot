package history

import (
	"strings"

	"github.com/soleapi/forgebot/internal/messaging"
)

// FromInbound 把收到的群消息转换成记录。只保存文本和是否带媒体，不保存图片内容。
func FromInbound(message messaging.InboundMessage) Record {
	role := RoleMember
	if message.TrustedResponder() {
		role = RoleAdmin
	}
	return Record{
		ConversationID: message.ConversationID, MessageID: strings.TrimSpace(message.MessageID),
		ReplyTo: strings.TrimSpace(message.ReplyToMessageID), UserID: message.UserID, UserName: message.UserName,
		Role: role, Text: message.Text, HasMedia: message.HasMedia(),
		Mentioned: message.Mentioned, Command: message.Command,
	}
}

// FromOutbound 把 Bot 发送成功的消息转换成记录。ReplyTo 是 Bot 回复所引用的那条消息。
func FromOutbound(message messaging.OutboundMessage) Record {
	media := len(message.Media) > 0
	for _, segment := range message.Segments {
		if segment.Type != "text" && segment.Type != "at" && segment.Type != "reply" {
			media = true
		}
	}
	return Record{
		ConversationID: message.ConversationID, ReplyTo: strings.TrimSpace(message.ReplyToMessageID),
		Role: RoleBot, Text: message.Text, HasMedia: media,
	}
}
