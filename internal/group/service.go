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

const maxReplySegments = 64

// validReply 只复核回复的结构边界：有内容（纯文本或消息段）、数量与长度有界、
// 每个消息段都有 type。段的具体含义由连接器解释，宿主不检查。
func validReply(reply EventReply) bool {
	if len(reply.Text) > 8192 || len(reply.Segments) > maxReplySegments {
		return false
	}
	if strings.TrimSpace(reply.Text) == "" && len(reply.Segments) == 0 {
		return false
	}
	for _, segment := range reply.Segments {
		if strings.TrimSpace(segment.Type) == "" {
			return false
		}
	}
	return true
}

// DeliverEvent 的发送作用域只限当前已验证事件；模块不接收全局 Sender。
func (s *Service) DeliverEvent(ctx context.Context, event Event, handlerID string, reply EventReply, send func(context.Context, EventReply) error) (EventResult, error) {
	if event.Kind != MemberJoined || event.Self || event.Historical {
		return EventResult{Verdict: "ignored"}, nil
	}
	if !validReply(reply) {
		return EventResult{}, operation.New(operation.Invalid, "invalid event reply")
	}
	if err := send(ctx, reply); err != nil {
		return EventResult{Accepted: true, Verdict: "send_failed"}, err
	}
	return EventResult{Accepted: true, Verdict: "sent"}, nil
}
