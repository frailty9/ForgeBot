// Package platform contains connector interfaces and the registry that maps
// logical conversations to platform-specific delivery targets.
package platform

import (
	"context"
	"errors"
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
	Sender() ConnectorSender
	// Start returns after the connector is ready to receive traffic.
	Start(context.Context, Handler) error
	Stop(context.Context) error
	Errors() <-chan error
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

type ConnectorState string

const (
	ConnectorStopped  ConnectorState = "stopped"
	ConnectorRunning  ConnectorState = "running"
	ConnectorDegraded ConnectorState = "degraded"
)

type ConnectorStatus struct {
	ID    string         `json:"id"`
	State ConnectorState `json:"state"`
	Error string         `json:"error,omitempty"`
}

type ConnectorSet interface {
	Sender
	Start(context.Context, Handler) error
	Stop(context.Context) error
	Errors() <-chan error
	Status() []ConnectorStatus
}

type Set struct {
	registry    *Registry
	adapters    []Adapter
	errors      chan error
	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	started     bool
	mu          sync.RWMutex
	status      map[string]ConnectorStatus
}

func NewSet(adapters []Adapter, bindings []ConversationBinding) (*Set, error) {
	registry := NewRegistry()
	set := &Set{
		registry: registry, adapters: append([]Adapter(nil), adapters...),
		errors: make(chan error, max(1, len(adapters))), status: make(map[string]ConnectorStatus, len(adapters)),
	}
	for _, adapter := range adapters {
		if adapter == nil {
			return nil, errors.New("connector adapter is nil")
		}
		if err := registry.RegisterConnector(adapter.ID(), adapter.Sender()); err != nil {
			return nil, err
		}
		set.status[adapter.ID()] = ConnectorStatus{ID: adapter.ID(), State: ConnectorStopped}
	}
	for _, binding := range bindings {
		if err := registry.BindConversation(binding); err != nil {
			return nil, err
		}
	}
	return set, nil
}

func (s *Set) Send(ctx context.Context, message messaging.OutboundMessage) error {
	return s.registry.Send(ctx, message)
}

func (s *Set) Start(ctx context.Context, handler Handler) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.started {
		return errors.New("connector set is already started")
	}
	watchCtx, cancel := context.WithCancel(ctx)
	started := make([]Adapter, 0, len(s.adapters))
	for _, adapter := range s.adapters {
		if err := adapter.Start(ctx, handler); err != nil {
			cancel()
			s.setStatus(adapter.ID(), ConnectorDegraded, err)
			for index := len(started) - 1; index >= 0; index-- {
				_ = started[index].Stop(context.Background())
				s.setStatus(started[index].ID(), ConnectorStopped, nil)
			}
			return fmt.Errorf("start connector %s: %w", adapter.ID(), err)
		}
		started = append(started, adapter)
		s.setStatus(adapter.ID(), ConnectorRunning, nil)
		go s.forwardErrors(watchCtx, adapter)
	}
	s.cancel = cancel
	s.started = true
	return nil
}

func (s *Set) Stop(ctx context.Context) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if !s.started {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	var result error
	for index := len(s.adapters) - 1; index >= 0; index-- {
		adapter := s.adapters[index]
		if err := adapter.Stop(ctx); err != nil {
			result = errors.Join(result, fmt.Errorf("stop connector %s: %w", adapter.ID(), err))
		} else {
			s.setStatus(adapter.ID(), ConnectorStopped, nil)
		}
	}
	s.cancel = nil
	s.started = false
	return result
}

func (s *Set) Errors() <-chan error { return s.errors }

func (s *Set) Status() []ConnectorStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]ConnectorStatus, 0, len(s.adapters))
	for _, adapter := range s.adapters {
		result = append(result, s.status[adapter.ID()])
	}
	return result
}

func (s *Set) forwardErrors(ctx context.Context, adapter Adapter) {
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-adapter.Errors():
			if !ok {
				return
			}
			if err == nil {
				continue
			}
			s.setStatus(adapter.ID(), ConnectorDegraded, err)
			select {
			case s.errors <- fmt.Errorf("connector %s: %w", adapter.ID(), err):
			default:
			}
		}
	}
}

func (s *Set) setStatus(id string, state ConnectorState, err error) {
	status := ConnectorStatus{ID: id, State: state}
	if err != nil {
		status.Error = err.Error()
	}
	s.mu.Lock()
	s.status[id] = status
	s.mu.Unlock()
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
