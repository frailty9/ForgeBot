package group

import (
	"context"
	"encoding/json"
	"regexp"
	"time"

	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/operation"
)

const maxReviewEvents = 10000

// claimReview 只保存有界的内存去重键，不保存申请内容或持久审批记录。
func (s *Service) claimReview(event Event) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for key, expires := range s.reviews {
		if !expires.After(now) {
			delete(s.reviews, key)
		}
	}
	key := ID(event.ConnectorID, event.ConversationID, event.ID)
	if _, exists := s.reviews[key]; exists {
		return false, nil
	}
	if len(s.reviews) >= maxReviewEvents {
		return false, operation.New(operation.Busy, "group review deduplication capacity exhausted")
	}
	s.reviews[key] = now.Add(24 * time.Hour)
	return true, nil
}

func (s *Service) HandleEvent(ctx context.Context, event Event) (result EventResult, resultErr error) {
	defer func() {
		// 私有句柄和申请内容不进入日志；普通日志足以排查决策和执行。
		s.logger.InfoContext(ctx, "group join review", "conversation_id", event.ConversationID,
			"connector_id", event.ConnectorID, "event_id", event.ID,
			"verdict", result.Verdict, "failure", operation.Describe(resultErr))
	}()
	if err := ctx.Err(); err != nil {
		return EventResult{}, err
	}
	policy, decider, bound := s.policy(event.ConversationID)
	if !bound {
		return EventResult{}, operation.New(operation.NotFound, "conversation is not bound")
	}
	if event.Kind != JoinRequested || event.Self || event.Historical {
		return EventResult{Verdict: "ignored"}, nil
	}
	if !policy.AutoReview {
		return EventResult{Verdict: "disabled"}, nil
	}
	if err := policy.Validate(); err != nil {
		return EventResult{}, operation.Wrap("group.review.policy", operation.Invalid, operation.NotStarted, err)
	}
	if len(event.Comment) > 8192 || len(event.Reference) > 4096 || len(event.ID) > 4096 {
		return EventResult{}, operation.New(operation.Invalid, "join event content exceeds limit")
	}
	if event.ID == "" || event.Reference == "" || event.ConnectorID == "" || event.UserID == "" {
		return EventResult{}, operation.New(operation.Invalid, "join request identity is required")
	}
	if event.At.IsZero() {
		event.At = s.now()
	}
	if !event.At.Add(24*time.Hour).After(s.now()) || event.At.After(s.now().Add(5*time.Minute)) {
		return EventResult{Verdict: "expired"}, nil
	}
	claimed, err := s.claimReview(event)
	if err != nil {
		return EventResult{Verdict: "busy"}, err
	}
	if !claimed {
		return EventResult{Verdict: "duplicate"}, nil
	}
	if s.actions == nil {
		return EventResult{}, operation.New(operation.Unsupported, "group actions are unavailable")
	}
	approve, err := s.evaluateReview(decision.WithBudget(ctx, 1), policy, decider, event.Comment)
	if err != nil {
		s.logger.WarnContext(ctx, "group review undecided", "conversation_id", event.ConversationID,
			"event_id", event.ID, "failure", operation.Describe(err))
		if ctx.Err() != nil {
			return EventResult{Accepted: true, Verdict: "undecided"}, ctx.Err()
		}
		return EventResult{Accepted: true, Verdict: "undecided"}, nil
	}
	current, _, bound := s.policy(event.ConversationID)
	if !bound || !current.Equal(policy) {
		return EventResult{Accepted: true, Verdict: "policy_changed"}, nil
	}
	if err := ctx.Err(); err != nil {
		return EventResult{}, err
	}
	support, err := s.actions.Support(ctx, event.ConversationID)
	if err != nil {
		return EventResult{}, err
	}
	if !support.JoinReview {
		return EventResult{}, operation.New(operation.Unsupported, "connector does not support join review")
	}
	reason := ""
	if !approve && support.JoinReviewReason {
		reason = policy.RejectReason
	}
	err = s.actions.Review(ctx, event.ConversationID, ReviewRequest{ConnectorID: event.ConnectorID,
		Reference: event.Reference, Approve: approve, Reason: reason, ExpectedPolicy: &policy})
	if err != nil {
		return EventResult{Accepted: true, Verdict: "review_failed"}, err
	}
	verdict := "rejected"
	if approve {
		verdict = "approved"
	}
	return EventResult{Accepted: true, Verdict: verdict}, nil
}

func (s *Service) evaluateReview(ctx context.Context, policy Policy, decider decision.Decider, application string) (bool, error) {
	if policy.ReviewRegex != "" {
		pattern, err := regexp.Compile(policy.ReviewRegex)
		if err != nil {
			return false, err
		}
		matches := pattern.MatchString(application)
		if !policy.ModelEnabled() || (policy.Combine() == "and" && !matches) || (policy.Combine() == "or" && matches) {
			return matches, nil
		}
	}
	state, err := json.Marshal(map[string]string{"application": application})
	if err != nil {
		return false, err
	}
	return decision.DecideBool(ctx, decider, policy.ReviewRule, state)
}
