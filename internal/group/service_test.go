package group

import (
	"context"
	"testing"

	"github.com/soleapi/forgebot/internal/messaging"
)

// 宿主只复核结构边界（有内容、数量有界、每段有 type），不解释段的含义。
func TestDeliverEventChecksOnlyReplyStructure(t *testing.T) {
	joined := Event{Kind: MemberJoined, ConversationID: "g", UserID: "1"}
	tooMany := make([]messaging.Segment, maxReplySegments+1)
	for i := range tooMany {
		tooMany[i] = messaging.Segment{Type: "text"}
	}
	invalid := []EventReply{
		{},
		{Text: " "},
		{Text: "hi", Segments: []messaging.Segment{{Type: ""}}},
		{Text: "hi", Segments: tooMany},
	}
	for _, reply := range invalid {
		sent := false
		_, err := New(nil, nil).DeliverEvent(context.Background(), joined, "welcome", reply, func(context.Context, EventReply) error {
			sent = true
			return nil
		})
		if err == nil || sent {
			t.Fatalf("invalid reply delivered: %#v", reply)
		}
	}
	valid := []EventReply{
		{Text: "欢迎 @Alice", Segments: []messaging.Segment{{Type: "text", Data: map[string]any{"text": "欢迎 "}}, {Type: "at", Data: map[string]any{"qq": "123"}}}},
		{Segments: []messaging.Segment{{Type: "image", Data: map[string]any{"file": "https://example.com/a.png"}}}},
	}
	for _, reply := range valid {
		var got EventReply
		result, err := New(nil, nil).DeliverEvent(context.Background(), joined, "welcome", reply, func(_ context.Context, r EventReply) error {
			got = r
			return nil
		})
		if err != nil || result.Verdict != "sent" || len(got.Segments) != len(reply.Segments) {
			t.Fatal(result, err, got)
		}
	}
}
