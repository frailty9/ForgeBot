package group

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/operation"
)

// Policies 返回逻辑群当前的审批视图；每次调用都读取最新运行代。
type Policies func(string) ReviewTarget
type Service struct {
	actions Actions
	policy  Policies
	now     func() time.Time
	logger  *slog.Logger
	mu      sync.Mutex
	reviews map[string]time.Time
}

func New(actions Actions, policy Policies, loggers ...*slog.Logger) *Service {
	if policy == nil {
		policy = func(string) ReviewTarget { return ReviewTarget{} }
	}
	logger := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &Service{actions: actions, policy: policy, now: time.Now, logger: logger, reviews: map[string]time.Time{}}
}

// DeliverEvent 的发送作用域只限当前已验证事件；模块不接收全局 Sender。
func (s *Service) DeliverEvent(ctx context.Context, event Event, handlerID string, reply EventReply, send func(context.Context, string) error) (EventResult, error) {
	if event.Kind != MemberJoined || event.Self || event.Historical {
		return EventResult{Verdict: "ignored"}, nil
	}
	if strings.TrimSpace(reply.Text) == "" || len(reply.Text) > 8192 {
		return EventResult{}, operation.New(operation.Invalid, "invalid event reply")
	}
	if err := send(ctx, reply.Text); err != nil {
		return EventResult{Accepted: true, Verdict: "send_failed"}, err
	}
	return EventResult{Accepted: true, Verdict: "sent"}, nil
}
