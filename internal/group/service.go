package group

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/operation"
)

// Policies 返回逻辑群当前的审批视图；每次调用都读取最新运行代。
type Policies func(string) ReviewTarget
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
		policy = func(string) ReviewTarget { return ReviewTarget{} }
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

// execute 先落 executing 再调用外部副作用，结果有界落盘；超时等未知结果
// 记为 unknown，不自动重试。当前仅欢迎语事件投递使用。
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
