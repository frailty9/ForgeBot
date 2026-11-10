package memory

import (
	"context"
	"log/slog"
	"sync"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
)

type Sender struct {
	logger *slog.Logger
	mu     sync.Mutex
	sent   []messaging.OutboundMessage
}

type Adapter struct {
	id     string
	sender *Sender
	errors chan error
}

func (*Sender) MessageSupport() group.MessageSupport { return group.MessageSupport{TextMessages: true} }

func NewAdapter(id string, logger *slog.Logger) *Adapter {
	return &Adapter{id: id, sender: NewSender(logger), errors: make(chan error)}
}

func (a *Adapter) ID() string                                    { return a.id }
func (a *Adapter) Sender() platform.ConnectorSender              { return a.sender }
func (a *Adapter) Errors() <-chan error                          { return a.errors }
func (a *Adapter) Start(context.Context, platform.Handler) error { return nil }
func (a *Adapter) Stop(context.Context) error                    { return nil }

func NewSender(logger *slog.Logger) *Sender {
	if logger == nil {
		logger = slog.Default()
	}
	return &Sender{logger: logger}
}

func (s *Sender) SendTo(ctx context.Context, target string, msg messaging.OutboundMessage) error {
	s.mu.Lock()
	s.sent = append(s.sent, msg)
	s.mu.Unlock()
	s.logger.InfoContext(ctx, "outbound message", "conversation_id", msg.ConversationID, "target", target, "reply_to", msg.ReplyToMessageID, "text", msg.Text)
	return nil
}

func (s *Sender) Sent() []messaging.OutboundMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]messaging.OutboundMessage, len(s.sent))
	copy(out, s.sent)
	return out
}
