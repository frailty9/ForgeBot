package proactivereply_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/proactivereply"
)

func build(t *testing.T, raw string) *modules.Set {
	t.Helper()
	registry := modules.NewRegistry()
	if err := registry.Register(proactivereply.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	enabled := true
	set, err := registry.Build(context.Background(), map[string]config.ModuleConfig{
		proactivereply.ID: {Enabled: &enabled, Options: json.RawMessage(raw)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	return set
}

func TestConfiguredGroupBuffersUnaddressedAndPassesAddressed(t *testing.T) {
	set := build(t, `{"silence":"1h","groups":{"support":{"enabled":true}}}`)
	participant := set.Participants()[0]
	message := messaging.InboundMessage{ConversationID: "support", UserID: "u", MessageID: "m", Text: "question"}
	result, err := participant.Handle(context.Background(), message, nil)
	if err != nil || !result.Consume || !result.Accepted || result.Verdict != "absorbed" {
		t.Fatalf("unaddressed result = %+v, %v", result, err)
	}
	message.MessageID = "m2"
	message.Mentioned = true
	result, err = participant.Handle(context.Background(), message, nil)
	if err != nil || result.Consume {
		t.Fatalf("addressed result = %+v, %v", result, err)
	}
}

func TestSilenceFlushUsesDeferredResponder(t *testing.T) {
	set := build(t, `{"silence":"1ms","groups":{"support":{"enabled":true}}}`)
	responses := make(chan messaging.InboundMessage, 1)
	message := messaging.InboundMessage{ConversationID: "support", UserID: "u", UserName: "user", MessageID: "m", Text: "question"}
	result, err := set.Participants()[0].Handle(context.Background(), message, func(_ context.Context, input messaging.InboundMessage) { responses <- input })
	if err != nil || !result.Consume {
		t.Fatalf("result = %+v, %v", result, err)
	}
	select {
	case response := <-responses:
		if response.ConversationID != "support" || response.MessageID != "m" || response.Text == "" {
			t.Fatal(response)
		}
	case <-time.After(time.Second):
		t.Fatal("proactive response did not flush")
	}
}

func TestOptionsRejectUnknownAndInvalidValues(t *testing.T) {
	factory := proactivereply.Factory{}
	for _, raw := range []string{`{"unknown":1}`, `{"silence":"bad"}`, `{"max_concurrent":0}`} {
		if err := factory.ValidateOptions(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid options accepted", raw)
		}
	}
}
