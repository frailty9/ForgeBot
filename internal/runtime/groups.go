package runtime

import (
	"context"
	"time"

	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/operation"
)

func (m *Manager) groupPolicy(id string) (group.Policy, decision.Decider, bool) {
	g := m.acquire()
	if g == nil {
		return group.Policy{}, nil, false
	}
	defer g.mu.RUnlock()
	for _, binding := range g.snapshot.Runtime.Platforms.Groups {
		if binding.ID == id {
			return g.snapshot.Runtime.GroupManagement[id], g.decisions, true
		}
	}
	return group.Policy{}, nil, false
}
func (m *Manager) HandleGroupEvent(ctx context.Context, event group.Event) (group.EventResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	g := m.acquire()
	if g == nil {
		return group.EventResult{}, operation.New(operation.Unavailable, "runtime is unavailable")
	}
	bound := false
	for _, binding := range g.snapshot.Runtime.Platforms.Groups {
		if binding.ID == event.ConversationID && binding.ConnectorID == event.ConnectorID {
			bound = true
			break
		}
	}
	if event.Kind == group.MemberJoined && bound {
		defer g.mu.RUnlock()
		if m.Paused() {
			return group.EventResult{Verdict: "paused"}, nil
		}
		result := group.EventResult{Verdict: "ignored"}
		for _, registration := range g.modules.GroupEvents() {
			reply, err := registration.Handle(ctx, event)
			if err != nil {
				return result, err
			}
			if reply == nil {
				continue
			}
			result, err = m.groups.DeliverEvent(ctx, event, registration.ID, *reply, func(ctx context.Context, text string) error {
				if m.Paused() {
					return operation.New(operation.Disabled, "runtime is paused")
				}
				return g.engine.SendEventReply(ctx, event.ConversationID, text)
			})
			if err != nil {
				return result, err
			}
		}
		return result, nil
	}
	g.mu.RUnlock()
	if !bound {
		return group.EventResult{}, operation.New(operation.NotFound, "event does not match a bound connector and conversation")
	}
	if m.Paused() {
		return group.EventResult{Verdict: "paused"}, nil
	}
	return m.groups.HandleEvent(ctx, event)
}
func (m *Manager) Support(ctx context.Context, id string) (group.Support, error) {
	g := m.acquire()
	if g == nil {
		return group.Support{}, operation.New(operation.Unavailable, "runtime is unavailable")
	}
	defer g.mu.RUnlock()
	actions, ok := g.connectors.(group.Actions)
	if !ok {
		actions, ok = m.builder.Sender.(group.Actions)
	}
	if !ok {
		return group.Support{Permission: "unknown"}, nil
	}
	return actions.Support(ctx, id)
}
func (m *Manager) Review(ctx context.Context, id string, request group.ReviewRequest) error {
	return m.groupAction(ctx, id, request.ConnectorID, "review", request.ExpectedPolicy, func(ctx context.Context, actions group.Actions) error {
		support, err := actions.Support(ctx, id)
		if err != nil {
			return err
		}
		if !support.JoinReview {
			return operation.New(operation.Unsupported, "connector does not support join review")
		}
		if request.Approve || !support.JoinReviewReason {
			request.Reason = ""
		}
		return actions.Review(ctx, id, request)
	})
}
func (m *Manager) groupAction(ctx context.Context, id, connectorID, kind string, expected *group.Policy, call func(context.Context, group.Actions) error) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	g := m.acquire()
	if g == nil {
		return operation.New(operation.Unavailable, "runtime is unavailable")
	}
	defer g.mu.RUnlock()
	bound := false
	for _, binding := range g.snapshot.Runtime.Platforms.Groups {
		if binding.ID == id && (connectorID == "" || connectorID == binding.ConnectorID) {
			bound = true
			break
		}
	}
	if !bound {
		return operation.New(operation.Conflict, "conversation binding changed")
	}
	policy := g.snapshot.Runtime.GroupManagement[id]
	if expected != nil && !policy.Equal(*expected) {
		return operation.New(operation.Conflict, "group review policy changed")
	}
	if kind == "review" && !policy.AutoReview {
		return operation.New(operation.Disabled, "group operation disabled by current configuration")
	}
	if m.Paused() {
		return operation.New(operation.Disabled, "runtime is paused")
	}
	actions, ok := g.connectors.(group.Actions)
	if !ok {
		actions, ok = m.builder.Sender.(group.Actions)
	}
	if !ok {
		return operation.New(operation.Unsupported, "connector does not support group operations")
	}
	return call(ctx, actions)
}

func (m *Manager) ListGroupOperations(ctx context.Context, limit int) ([]group.Record, error) {
	return m.groups.List(ctx, limit)
}
