package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/messaging"
)

type reviewSender struct {
	requests []group.ReviewRequest
	fail     error
}

func (*reviewSender) SendTo(context.Context, string, messaging.OutboundMessage) error { return nil }
func (s *reviewSender) ReviewGroupRequest(_ context.Context, _ string, r group.ReviewRequest) error {
	s.requests = append(s.requests, r)
	return s.fail
}

type reasonReviewSender struct{ *reviewSender }

func (*reasonReviewSender) GroupReviewReasonSupported() bool { return true }

func TestReviewReasonIsOptionalAndOtherErrorsPropagate(t *testing.T) {
	for _, supports := range []bool{false, true} {
		s := &reviewSender{fail: errors.New("platform offline")}
		var sender ConnectorSender = s
		if supports {
			sender = &reasonReviewSender{s}
		}
		r := NewRegistry()
		if err := r.RegisterConnector("one", sender); err != nil {
			t.Fatal(err)
		}
		if err := r.BindConversation(ConversationBinding{ConversationID: "g", ConnectorID: "one", Target: "8"}); err != nil {
			t.Fatal(err)
		}
		status, err := r.Support(context.Background(), "g")
		if err != nil || !status.JoinReview || status.JoinReviewReason != supports || len(s.requests) != 0 {
			t.Fatal(status, err)
		}
		for _, approve := range []bool{false, true} {
			err = r.Review(context.Background(), "g", group.ReviewRequest{Approve: approve, Reason: "拒绝原因"})
			if !errors.Is(err, s.fail) {
				t.Fatal(err)
			}
			got := s.requests[len(s.requests)-1]
			want := ""
			if supports && !approve {
				want = "拒绝原因"
			}
			if got.Reason != want {
				t.Fatal(got)
			}
		}
		if len(s.requests) != 2 {
			t.Fatal("review unexpectedly retried")
		}
	}
}
