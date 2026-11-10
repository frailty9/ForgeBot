package platform

import (
	"context"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/operation"
)

func (r *Registry) groupDestination(id string) (destination, error) {
	r.mu.RLock()
	target := r.conversations[id]
	r.mu.RUnlock()
	if target.sender == nil {
		return target, operation.New(operation.NotConfigured, "no delivery binding for conversation")
	}
	return target, nil
}
func (r *Registry) Support(_ context.Context, id string) (group.Support, error) {
	target, err := r.groupDestination(id)
	if err != nil {
		return group.Support{}, err
	}
	_, review := target.sender.(group.Reviewer)
	_, recall := target.sender.(group.Recaller)
	var messages group.MessageSupport
	if reporter, ok := target.sender.(group.MessageSupportReporter); ok {
		messages = reporter.MessageSupport()
	}
	joinEvents, memberEvents := false, false
	if reporter, ok := target.sender.(group.EventReporter); ok {
		joinEvents, memberEvents = reporter.GroupEventSupport()
	}
	reviewReason := false
	if reporter, ok := target.sender.(group.ReviewReasonReporter); ok {
		reviewReason = review && reporter.GroupReviewReasonSupported()
	}
	return group.Support{MessageSupport: messages, JoinEvents: joinEvents, MemberEvents: memberEvents, JoinReview: review, JoinReviewReason: reviewReason, MessageRecall: recall, Permission: "unknown"}, nil
}

// Recall 实现 group.MessageRecaller：id 为逻辑群 ID，先查群绑定得到
// 具体发送器和平台 target，再调用它的 group.Recaller。只有这次下层调用
// 会进入具体平台实现；Registry 不重复发起另一条撤回请求。
func (r *Registry) Recall(ctx context.Context, id, messageID string) error {
	target, err := r.groupDestination(id)
	if err != nil {
		return err
	}
	port, ok := target.sender.(group.Recaller)
	if !ok {
		return operation.New(operation.Unsupported, "connector does not support message recall")
	}
	return port.RecallMessage(ctx, target.target, messageID)
}
func (r *Registry) Review(ctx context.Context, id string, request group.ReviewRequest) error {
	target, err := r.groupDestination(id)
	if err != nil {
		return err
	}
	port, ok := target.sender.(group.Reviewer)
	if !ok {
		return operation.New(operation.Unsupported, "connector does not support group review")
	}
	reporter, supportsReason := target.sender.(group.ReviewReasonReporter)
	if request.Approve || !supportsReason || !reporter.GroupReviewReasonSupported() {
		request.Reason = ""
	}
	return port.ReviewGroupRequest(ctx, target.target, request)
}
