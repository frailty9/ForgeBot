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
	ID() string
	Start(context.Context, Handler) error
	Stop(context.Context) error
}

type Sender interface {
	Send(context.Context, messaging.OutboundMessage) error
}

type ConnectorSender interface {
	SendTo(context.Context, string, messaging.OutboundMessage) error
}

type ConversationBinding struct {
	ConversationID string `json:"conversation_id"`
	ConnectorID    string `json:"connector_id"`
	Target         string `json:"target"`
}

type destination struct {
	sender ConnectorSender
	target string
}

type Registry struct {
	mu            sync.RWMutex
	connectors    map[string]ConnectorSender
	conversations map[string]destination
}

func NewRegistry() *Registry {
	return &Registry{connectors: make(map[string]ConnectorSender), conversations: make(map[string]destination)}
}

func (r *Registry) RegisterConnector(id string, sender ConnectorSender) error {
	if id == "" {
		return fmt.Errorf("connector id is required")
	}
	if sender == nil {
		return fmt.Errorf("sender for connector %s is nil", id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.connectors[id]; exists {
		return fmt.Errorf("sender already registered for connector %s", id)
	}
	r.connectors[id] = sender
	return nil
}

func (r *Registry) BindConversation(binding ConversationBinding) error {
	if binding.ConversationID == "" || binding.ConnectorID == "" || binding.Target == "" {
		return fmt.Errorf("conversation id, connector id, and target are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	sender := r.connectors[binding.ConnectorID]
	if sender == nil {
		return fmt.Errorf("connector %s is not registered", binding.ConnectorID)
	}
	if _, exists := r.conversations[binding.ConversationID]; exists {
		return fmt.Errorf("conversation %s is already bound", binding.ConversationID)
	}
	r.conversations[binding.ConversationID] = destination{sender: sender, target: binding.Target}
	return nil
}

func (r *Registry) Send(ctx context.Context, msg messaging.OutboundMessage) error {
	r.mu.RLock()
	destination := r.conversations[msg.ConversationID]
	r.mu.RUnlock()
	if destination.sender == nil {
		return fmt.Errorf("no delivery binding for conversation %s", msg.ConversationID)
	}
	return destination.sender.SendTo(ctx, destination.target, msg)
}

func (r *Registry) Conversations() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conversations := make([]string, 0, len(r.conversations))
	for id := range r.conversations {
		conversations = append(conversations, id)
	}
	return conversations
}
