package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/operation"
)

func TestConfiguredDMFailureNeverInvokesAnswerLLM(t *testing.T) {
	for _, credential := range []bool{false, true} {
		dmCalls, llmCalls := 0, 0
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { dmCalls++; w.WriteHeader(http.StatusServiceUnavailable) }))
		snapshot := testSnapshot()
		snapshot.Runtime.Decision.DM = &config.DecisionModelConfig{Provider: "jev", Endpoint: api.URL, APIKeyRef: "dm/key"}
		client := moduleClient(func(context.Context, llm.Request) (llm.Response, error) {
			llmCalls++
			return llm.Response{Text: `{"matches":"yes"}`}, nil
		})
		key := ""
		if credential {
			key = "key"
		}
		builder, err := (Builder{}).withDecision(context.Background(), snapshot, client, "dm/key", key)
		if err != nil {
			api.Close()
			t.Fatal(err)
		}
		_, err = decision.DecideBool(context.Background(), builder.Decisions, "rule", json.RawMessage(`{}`))
		api.Close()
		want := 0
		if credential {
			want = 1
		}
		if err == nil || dmCalls != want || llmCalls != 0 {
			t.Fatal(err, dmCalls, llmCalls)
		}
	}
}

func TestAutomaticReviewWithoutStoreAndDedupAcrossHotUpdate(t *testing.T) {
	snapshot := testSnapshot()
	p := group.Policy{ReviewEnabled: true, AutoReview: true, ReviewRegex: `\S`}
	snapshot.Runtime.GroupManagement = map[string]group.Policy{"local": p}
	port := &groupPort{Sender: registeredSender(t).registry}
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: port})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	event := group.Event{ID: "request", Kind: group.JoinRequested, ConversationID: "local", ConnectorID: "echo", UserID: "u", Comment: "application", Reference: "opaque", At: time.Now()}
	if r, err := manager.HandleGroupEvent(context.Background(), event); err != nil || r.Verdict != "approved" {
		t.Fatal(r, err)
	}
	next := snapshot.Clone()
	next.Version++
	next.Runtime.Bot.Name = "updated"
	if _, err = manager.Apply(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if r, err := manager.HandleGroupEvent(context.Background(), event); err != nil || r.Verdict != "duplicate" || port.calls.Load() != 1 {
		t.Fatal(r, err, port.calls.Load())
	}
	next = next.Clone()
	next.Version++
	p.ReviewRegex = "changed"
	next.Runtime.GroupManagement["local"] = p
	if _, err = manager.Apply(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	previous := snapshot.Runtime.GroupManagement["local"]
	err = manager.Review(context.Background(), "local", group.ReviewRequest{ConnectorID: "echo", Reference: "opaque", Approve: true, ExpectedPolicy: &previous})
	if err == nil || operation.Describe(err).Code != operation.Conflict || port.calls.Load() != 1 {
		t.Fatal(err, port.calls.Load())
	}
	manager.SetPaused(true)
	event.ID = "paused"
	if r, err := manager.HandleGroupEvent(context.Background(), event); err != nil || r.Verdict != "paused" || port.calls.Load() != 1 {
		t.Fatal(r, err, port.calls.Load())
	}
}
