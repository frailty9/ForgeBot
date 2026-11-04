package group

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/operation"
)

type Policies func(string) (Policy, decision.Decider, bool)
type Service struct {
	store   Store
	actions Actions
	policy  Policies
	now     func() time.Time
	logger  *slog.Logger
	mu      sync.Mutex
	reviews map[string]time.Time
}

func New(store Store, actions Actions, policy Policies, loggers ...*slog.Logger) *Service {
	if policy == nil {
		policy = func(string) (Policy, decision.Decider, bool) { return Policy{}, nil, false }
	}
	logger := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &Service{store: store, actions: actions, policy: policy, now: time.Now, logger: logger, reviews: map[string]time.Time{}}
}

func ID(parts ...string) string {
	data, _ := json.Marshal(parts)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Service) List(ctx context.Context, limit int) ([]Record, error) {
	if s.store == nil {
		return nil, operation.New(operation.NotConfigured, "group operation store is not configured")
	}
	records, err := s.store.List(ctx, limit)
	for i := range records {
		records[i] = records[i].Public()
	}
	return records, err
}

type ActionRequest struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	Text           string `json:"text,omitempty"`
	MessageID      string `json:"message_id,omitempty"`
}

func (s *Service) Perform(ctx context.Context, kind string, request ActionRequest) (Record, error) {
	if s.store == nil {
		return Record{}, operation.New(operation.NotConfigured, "group operation store is not configured")
	}
	if s.actions == nil {
		return Record{}, operation.New(operation.Unsupported, "group actions are unavailable")
	}
	if request.ID == "" || len(request.ID) > 128 || request.ConversationID == "" {
		return Record{}, operation.New(operation.Invalid, "operation id and conversation are required")
	}
	policy, _, bound := s.policy(request.ConversationID)
	if !bound {
		return Record{}, operation.New(operation.NotFound, "conversation is not bound")
	}
	support, err := s.actions.Support(ctx, request.ConversationID)
	if err != nil {
		return Record{}, err
	}
	var call func() error
	switch kind {
	case "announcement":
		if !policy.Announcements {
			return Record{}, operation.New(operation.Disabled, "announcements are disabled")
		}
		if !support.Announcement {
			return Record{}, operation.New(operation.Unsupported, "connector does not support announcements")
		}
		if strings.TrimSpace(request.Text) == "" || len(request.Text) > 8192 {
			return Record{}, operation.New(operation.Invalid, "announcement requires 1..8192 bytes")
		}
		call = func() error { return s.actions.Announce(ctx, request.ConversationID, request.Text) }
	case "recall":
		if !policy.Recall {
			return Record{}, operation.New(operation.Disabled, "recall is disabled")
		}
		if !support.Recall {
			return Record{}, operation.New(operation.Unsupported, "connector does not support recall")
		}
		if request.MessageID == "" || len(request.MessageID) > 256 {
			return Record{}, operation.New(operation.Invalid, "recall message id is required")
		}
		call = func() error { return s.actions.Recall(ctx, request.ConversationID, request.MessageID) }
	default:
		return Record{}, operation.New(operation.Unsupported, "unknown group action")
	}
	data, _ := json.Marshal(request)
	hash := sha256.Sum256(data)
	r := Record{ID: ID("action", kind, request.ConversationID, request.ID), Kind: kind, ConversationID: request.ConversationID, CreatedAt: s.now(), Status: Pending, Fingerprint: fmt.Sprintf("%x", hash)}
	saved, created, err := s.store.Create(ctx, r)
	if err != nil {
		return saved, err
	}
	if !created {
		if saved.Fingerprint != r.Fingerprint {
			return saved.Public(), operation.New(operation.Conflict, "idempotency key reused with different action content")
		}
		if saved.Status == Done {
			return saved.Public(), nil
		}
		return saved.Public(), operation.New(operation.Conflict, "action already started; inspect execution state before retry")
	}
	return s.execute(ctx, saved, call)
}

func (s *Service) execute(ctx context.Context, r Record, call func() error) (Record, error) {
	if err := ctx.Err(); err != nil {
		return r.Public(), err
	}
	r.Status = Executing
	r, err := s.store.Update(ctx, r.Revision, r)
	if err != nil {
		return r.Public(), err
	}
	err = call()
	if err == nil {
		r.Status = Done
	} else {
		r.Failure = operation.Describe(err)
		r.Status = Failed
		if r.Failure.Effect == operation.EffectUnknown || r.Failure.Effect == operation.Partial {
			r.Status = Uncertain
		}
	}
	// 即使调用方取消，仍有界地落盘执行结果；失败则返回未知，绝不自动重试副作用。
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	saved, persistErr := s.store.Update(persistCtx, r.Revision, r)
	if persistErr != nil {
		return r.Public(), operation.Wrap("group.record.result", operation.Internal, operation.EffectUnknown, persistErr)
	}
	return saved.Public(), err
}

// DeliverEvent 的发送作用域只限当前已验证事件；模块不接收全局 Sender。
func (s *Service) DeliverEvent(ctx context.Context, event Event, handlerID string, reply EventReply, send func(context.Context, string) error) (EventResult, error) {
	if s.store == nil {
		return EventResult{}, operation.New(operation.NotConfigured, "group event delivery requires operation store")
	}
	if event.Kind != MemberJoined || event.Self || event.Historical {
		return EventResult{Verdict: "ignored"}, nil
	}
	if strings.TrimSpace(reply.Text) == "" || len(reply.Text) > 8192 || event.ID == "" {
		return EventResult{}, operation.New(operation.Invalid, "invalid event reply")
	}
	if event.At.IsZero() || event.At.Before(s.now().Add(-5*time.Minute)) || event.At.After(s.now().Add(time.Minute)) {
		return EventResult{Verdict: "expired"}, nil
	}
	r, created, err := s.store.Create(ctx, Record{ID: ID("event_reply", handlerID, event.ConnectorID, event.ConversationID, event.ID), Kind: "event_reply", ConversationID: event.ConversationID, ConnectorID: event.ConnectorID, UserID: event.UserID, CreatedAt: event.At, Status: Pending})
	if err != nil {
		return EventResult{}, err
	}
	if !created {
		return EventResult{Verdict: "duplicate"}, nil
	}
	_, err = s.execute(ctx, r, func() error { return send(ctx, reply.Text) })
	if err != nil {
		return EventResult{Accepted: true, Verdict: "send_failed"}, err
	}
	return EventResult{Accepted: true, Verdict: "sent"}, nil
}
