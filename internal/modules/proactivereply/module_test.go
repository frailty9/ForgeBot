package proactivereply_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/proactivereply"
	"github.com/soleapi/forgebot/internal/proactive"
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

func TestAdminQuoteYieldsGroupButNeverConsumesMention(t *testing.T) {
	set := build(t, `{"silence":"1h","yield_duration":"5m","groups":{"support":{"enabled":true}}}`)
	participant := set.Participants()[0]
	question := messaging.InboundMessage{ConversationID: "support", UserID: "u", MessageID: "q", Text: "question"}
	if _, err := participant.Handle(context.Background(), question, nil); err != nil {
		t.Fatal(err)
	}
	admin := messaging.InboundMessage{ConversationID: "support", UserID: "admin", PlatformRole: "owner", MessageID: "a", ReplyToMessageID: "q", Text: "answer and ask", Mentioned: true}
	if result, err := participant.Handle(context.Background(), admin, nil); err != nil || result.Consume {
		t.Fatalf("admin @ swallowed: %+v %v", result, err)
	}
	question.MessageID = "q2"
	if result, err := participant.Handle(context.Background(), question, nil); err != nil || result.Verdict != "proactive_yielded" || !result.Consume {
		t.Fatalf("yield ignored: %+v %v", result, err)
	}
	question.Mentioned = true
	question.MessageID = "q3"
	if result, err := participant.Handle(context.Background(), question, nil); err != nil || result.Consume {
		t.Fatal("mention suppressed by yield", result, err)
	}
}

func TestYieldSharedAcrossModuleGenerationsAndCheckedBeforeSend(t *testing.T) {
	state := proactive.NewYieldState()
	state.Configure([]string{"support"})
	registry := modules.NewRegistry()
	if err := registry.Register(proactivereply.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	values := capability.NewValues()
	if err := values.Provide(proactive.YieldKey.ID(), state); err != nil {
		t.Fatal(err)
	}
	on := true
	create := func() *modules.Set {
		set, err := registry.BuildWithCapabilities(context.Background(), map[string]config.ModuleConfig{proactivereply.ID: {Enabled: &on, Options: json.RawMessage(`{"silence":"1h","groups":{"support":{"enabled":true}}}`)}}, nil, values)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = set.Close(context.Background()) })
		return set
	}
	old := create()
	revision, _ := state.Snapshot("support")
	state.Suspend("support", time.Minute)
	for _, set := range []*modules.Set{old, create()} {
		result, err := set.Chain().Reply(context.Background(), hooks.Info{ConversationID: "support", Trigger: "proactive", ProactiveRevision: revision}, hooks.ReplyInput{Text: "already generated", Kind: "final"})
		if err != nil || !result.Suppress {
			t.Fatalf("stale proactive send: %+v %v", result, err)
		}
		result, err = set.Chain().Reply(context.Background(), hooks.Info{ConversationID: "support", Trigger: "direct"}, hooks.ReplyInput{Text: "explicit mention", Kind: "final"})
		if err != nil || result.Suppress {
			t.Fatal("direct reply suppressed", result, err)
		}
	}
}

type interventionDecider struct {
	calls  int
	answer string
}

func (d *interventionDecider) Decide(_ context.Context, r decision.Request) (decision.Result, error) {
	d.calls++
	return decision.Result{Answer: decision.Answer{"intervened": d.answer}}, nil
}

func TestOrdinaryAdminChatDoesNotYieldButSemanticAnswerDoes(t *testing.T) {
	state := proactive.NewYieldState()
	state.Configure([]string{"support"})
	decider := &interventionDecider{answer: "no"}
	values := capability.NewValues()
	_ = values.Provide(proactive.YieldKey.ID(), state)
	_ = values.Provide(decision.ServiceKey.ID(), decision.Decider(decider))
	registry := modules.NewRegistry()
	if err := registry.Register(proactivereply.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	on := true
	set, err := registry.BuildWithCapabilities(context.Background(), map[string]config.ModuleConfig{proactivereply.ID: {Enabled: &on, Options: json.RawMessage(`{"silence":"1h","groups":{"support":{"enabled":true}}}`)}}, nil, values)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	participant := set.Participants()[0]
	admin := messaging.InboundMessage{ConversationID: "support", UserID: "a", MessageID: "a1", Text: "hello", Administrator: true}
	_, _ = participant.Handle(context.Background(), admin, nil)
	if decider.calls != 0 {
		t.Fatal("admin idle chat caused needless decision")
	}
	question := messaging.InboundMessage{ConversationID: "support", UserID: "u", MessageID: "q", Text: "question"}
	_, _ = participant.Handle(context.Background(), question, nil)
	_, _ = participant.Handle(context.Background(), admin, nil)
	if _, paused := state.Snapshot("support"); paused {
		t.Fatal("ordinary chat yielded")
	}
	decider.answer = "yes"
	admin.MessageID = "a2"
	admin.Text = "answer"
	_, _ = participant.Handle(context.Background(), admin, nil)
	if _, paused := state.Snapshot("support"); !paused {
		t.Fatal("trusted semantic answer did not yield")
	}
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
