package platform

import (
	"context"
	"fmt"
	"sync"

	"github.com/soleapi/forgebot/internal/messaging"
)

type Handler interface {
	HandleInbound(context.Context, messaging.InboundMessage) (HandleResult, error)
}

type HandleResult struct {
	Accepted bool   `json:"accepted"`
	Verdict  string `json:"verdict"`
}

type Adapter interface {
	Name() string
	Start(context.Context, Handler) error
}

type Sender interface {
	Send(context.Context, messaging.OutboundMessage) error
}

type Registry struct {
	mu      sync.RWMutex
	senders map[string]Sender
}

func NewRegistry() *Registry {
	return &Registry{senders: make(map[string]Sender)}
}

func (r *Registry) RegisterSender(platform string, sender Sender) error {
	if platform == "" {
		return fmt.Errorf("platform is required")
	}
	if sender == nil {
		return fmt.Errorf("sender for %s is nil", platform)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.senders[platform]; exists {
		return fmt.Errorf("sender already registered for platform %s", platform)
	}
	r.senders[platform] = sender
	return nil
}

func (r *Registry) Send(ctx context.Context, msg messaging.OutboundMessage) error {
	r.mu.RLock()
	sender := r.senders[msg.Platform]
	r.mu.RUnlock()
	if sender == nil {
		return fmt.Errorf("no sender registered for platform %s", msg.Platform)
	}
	return sender.Send(ctx, msg)
}

func (r *Registry) Platforms() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	platforms := make([]string, 0, len(r.senders))
	for name := range r.senders {
		platforms = append(platforms, name)
	}
	return platforms
}
