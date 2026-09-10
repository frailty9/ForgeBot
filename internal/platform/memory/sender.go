package memory

import (
	"context"
	"log/slog"
	"sync"

	"github.com/soleapi/forgebot/internal/messaging"
)

type Sender struct {
	logger *slog.Logger
	mu     sync.Mutex
	sent   []messaging.OutboundMessage
}

func NewSender(logger *slog.Logger) *Sender {
	if logger == nil {
		logger = slog.Default()
	}
	return &Sender{logger: logger}
}

func (s *Sender) Send(ctx context.Context, msg messaging.OutboundMessage) error {
	s.mu.Lock()
	s.sent = append(s.sent, msg)
	s.mu.Unlock()
	s.logger.InfoContext(ctx, "outbound message", "platform", msg.Platform, "chat_id", msg.ChatID, "reply_to", msg.ReplyToMessageID, "text", msg.Text)
	return nil
}

func (s *Sender) Sent() []messaging.OutboundMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]messaging.OutboundMessage, len(s.sent))
	copy(out, s.sent)
	return out
}
