package proactivereply

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/proactive"
)

type decisionFunc func(context.Context, decision.Request) (decision.Result, error)

func (f decisionFunc) Decide(ctx context.Context, r decision.Request) (decision.Result, error) {
	return f(ctx, r)
}

func testInstance(t *testing.T, d decision.Decider, silence string) (*instance, *proactive.YieldState) {
	t.Helper()
	state := proactive.NewYieldState()
	state.Configure([]string{"g"})
	values := capability.NewValues()
	_ = values.Provide(proactive.YieldKey.ID(), state)
	if d != nil {
		_ = values.Provide(decision.ServiceKey.ID(), d)
	}
	built, err := (Factory{}).BuildWithContext(context.Background(), modules.BuildContext{Capabilities: values.View()},
		json.RawMessage(`{"silence":"`+silence+`","max_window":"0s","groups":{"g":{"enabled":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	i := built.(*instance)
	t.Cleanup(func() { _ = i.Close(context.Background()) })
	return i, state
}

func TestUnrelatedQuotedContentNeverDirectlyYieldsOrDropsPending(t *testing.T) {
	for _, pending := range []bool{false, true} {
		for _, outcome := range []string{"no", "unknown", "error", "yes", "no-model"} {
			t.Run(outcome+map[bool]string{false: "/idle", true: "/pending"}[pending], func(t *testing.T) {
				calls := 0
				var d decision.Decider = decisionFunc(func(context.Context, decision.Request) (decision.Result, error) {
					calls++
					if outcome == "error" {
						return decision.Result{}, errors.New("offline")
					}
					return decision.Result{Answer: decision.Answer{"matches": outcome}}, nil
				})
				if outcome == "no-model" {
					d = nil
				}
				i, state := testInstance(t, d, "1h")
				if pending {
					_, _ = i.handle(context.Background(), messaging.InboundMessage{ConversationID: "g", UserID: "u", MessageID: "q", Text: "question"}, nil)
				}
				_, err := i.handle(context.Background(), messaging.InboundMessage{ConversationID: "g", UserID: "admin", MessageID: "a", Administrator: true,
					ReplyToMessageID: "yesterday", Quoted: &messaging.QuotedMessage{MessageID: "yesterday", Text: "old chat"}, Text: "hello"}, nil)
				if err != nil {
					t.Fatal(err)
				}
				wantYield := pending && outcome == "yes"
				if _, paused := state.Snapshot("g"); paused != wantYield {
					t.Fatal("unrelated quote yielded", pending, outcome, paused)
				}
				if i.pool.HasPending("g", "q") != (pending && !wantYield) {
					t.Fatal("pending messages incorrectly discarded")
				}
				wantCalls := 0
				if pending && d != nil {
					wantCalls = 1
				}
				if calls != wantCalls {
					t.Fatal(calls, wantCalls)
				}
			})
		}
	}
}

func TestQuotedInflightQuestionYieldsAndInvalidatesGeneratedReply(t *testing.T) {
	i, state := testInstance(t, nil, "1ms")
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	revision, _ := state.Snapshot("g")
	_, err := i.handle(context.Background(), messaging.InboundMessage{ConversationID: "g", UserID: "u", MessageID: "q", Text: "question"},
		func(context.Context, messaging.InboundMessage) { close(entered); <-release })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("batch did not enter responder")
	}
	if !i.pool.HasPending("g", "q") {
		t.Fatal("inflight question missing")
	}
	_, err = i.handle(context.Background(), messaging.InboundMessage{ConversationID: "g", UserID: "a", MessageID: "a", Administrator: true, ReplyToMessageID: "q", Text: "answer"}, nil)
	if err != nil || state.CanSend("g", revision) {
		t.Fatal("inflight reply was not invalidated", err)
	}
}
