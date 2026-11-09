package runtime

import (
	"context"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/history"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
)

// recordingSender 让 Bot 发出的每条群消息在发送成功后进入近期消息容器。
// 所有出站消息（回答、主动回复、欢迎语、运维回复、进度提示）都经过它；发送失败的不记录。
type recordingSender struct {
	next    platform.Sender
	history *history.Window
}

func (s recordingSender) Send(ctx context.Context, message messaging.OutboundMessage) error {
	if err := s.next.Send(ctx, message); err != nil {
		return err
	}
	s.history.Append(history.FromOutbound(message))
	return nil
}

func withHistory(sender platform.Sender, window *history.Window) platform.Sender {
	if sender == nil || window == nil {
		return sender
	}
	return recordingSender{next: sender, history: window}
}

func newHistory(snapshot config.Snapshot, builder Builder) *history.Window {
	return history.New(snapshot.Runtime.MessageHistory, boundConversationIDs(snapshot.Runtime), builder.Logger)
}
