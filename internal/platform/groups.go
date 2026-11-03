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
	_, announce := target.sender.(group.Announcer)
	_, recall := target.sender.(group.Recaller)
	return group.Support{JoinReview: review, Announcement: announce, Recall: recall, Permission: "unknown"}, nil
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
	return port.ReviewGroupRequest(ctx, target.target, request)
}
func (r *Registry) Announce(ctx context.Context, id, text string) error {
	target, err := r.groupDestination(id)
	if err != nil {
		return err
	}
	port, ok := target.sender.(group.Announcer)
	if !ok {
		return operation.New(operation.Unsupported, "connector does not support announcements")
	}
	return port.PublishGroupAnnouncement(ctx, target.target, text)
}
func (r *Registry) Recall(ctx context.Context, id, message string) error {
	target, err := r.groupDestination(id)
	if err != nil {
		return err
	}
	port, ok := target.sender.(group.Recaller)
	if !ok {
		return operation.New(operation.Unsupported, "connector does not support recall")
	}
	return port.RecallGroupMessage(ctx, target.target, message)
}
