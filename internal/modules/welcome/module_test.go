package welcome

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/soleapi/forgebot/internal/group"
)

func TestWelcomeOnlyActualNewMemberAndFiniteTemplates(t *testing.T) {
	item, err := (Factory{}).Build(context.Background(), json.RawMessage(`{"groups":{"g":{"enabled":true,"text":"欢迎 {user}（{user_id}）加入 {conversation_id}"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	handle := item.(*instance).ExtendedRegistrations().GroupEvents[0].Handle
	event := group.Event{Kind: group.MemberJoined, ConversationID: "g", UserID: "123", UserName: "Alice"}
	reply, err := handle(context.Background(), event)
	if err != nil || reply == nil || reply.Text != "欢迎 Alice（123）加入 g" {
		t.Fatal(reply, err)
	}
	for _, alter := range []func(*group.Event){func(e *group.Event) { e.Kind = group.JoinRequested }, func(e *group.Event) { e.Self = true }, func(e *group.Event) { e.Historical = true }, func(e *group.Event) { e.ConversationID = "other" }} {
		copy := event
		alter(&copy)
		reply, err := handle(context.Background(), copy)
		if err != nil || reply != nil {
			t.Fatal("unexpected welcome", copy, reply, err)
		}
	}
	for _, raw := range []string{`{"groups":{"g":{"enabled":true,"text":" "}}}`, `{"groups":{"g":{"enabled":true,"text":"{unknown}"}}}`, `{"typo":1}`} {
		if err := (Factory{}).ValidateOptions(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid welcome config accepted", raw)
		}
	}
}
