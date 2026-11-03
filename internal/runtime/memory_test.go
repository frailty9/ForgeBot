package runtime

import (
	"context"
	"testing"

	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
)

func TestConversationSurvivesReplacementAndPolicyDisableClears(t *testing.T) {
	snapshot := testSnapshot()
	var messages []llm.Message
	client := moduleClient(func(_ context.Context, r llm.Request) (llm.Response, error) {
		messages = r.Messages
		return llm.Response{Text: "answer"}, nil
	})
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: registeredSender(t).registry, Secrets: configuredSecrets(t, "key"), ClientFactory: func(llm.ClientConfig) (llm.Client, error) { return client, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	send := func(id string) {
		t.Helper()
		if _, err := manager.HandleInbound(context.Background(), messaging.InboundMessage{ConversationID: "c", UserID: "u", MessageID: id, Text: id, Mentioned: true}); err != nil {
			t.Fatal(err)
		}
	}
	send("first")
	next := snapshot.Clone()
	next.Version++
	next.Runtime.Bot.Name = "new name"
	if _, err = manager.Apply(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	send("followup")
	if len(messages) != 3 || messages[0].Text() != "first" {
		t.Fatal("replacement lost context", messages)
	}
	if err = manager.Restart(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	send("after restart")
	if len(messages) != 5 {
		t.Fatal("bot thread restart lost process memory", messages)
	}
	next.Version++
	next.Runtime.ConversationMemory.Disabled = true
	if _, err = manager.Apply(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	send("disabled")
	if len(messages) != 1 {
		t.Fatal("disabled memory reused context", messages)
	}
	next.Version++
	next.Runtime.ConversationMemory.Disabled = false
	if _, err = manager.Apply(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	send("enabled")
	if len(messages) != 1 {
		t.Fatal("re-enabled memory resurrected old context", messages)
	}
}
