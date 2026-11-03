package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/secret"
)

type forbiddenDMSecrets struct{ t *testing.T }

func (s forbiddenDMSecrets) Get(context.Context, string) (string, error) {
	s.t.Error("disabled DM resolved credential")
	return "", nil
}

func TestDisabledDMUsesLLMFallbackWithoutResolvingCredential(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Runtime.Decision.DM = &config.DecisionModelConfig{Disabled: true, Provider: "jev", Endpoint: "http://127.0.0.1:1", APIKeyRef: "unused"}
	modelCalls := 0
	client := moduleClient(func(context.Context, llm.Request) (llm.Response, error) {
		modelCalls++
		return llm.Response{Text: `{"scope":"in_domain","respond":"yes","evidence":"none"}`}, nil
	})
	builder, err := (Builder{Secrets: forbiddenDMSecrets{t}}).withDecision(context.Background(), snapshot, client, "", "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := builder.Decisions.Decide(context.Background(), decision.Request{TaskID: decision.AnswerTaskID, Version: 1, State: json.RawMessage(`{"message":"hello"}`)})
	if err != nil || result.Source != "llm" || len(result.Attempts) != 1 || modelCalls != 1 {
		t.Fatalf("disabled DM not bypassed: %+v %v", result, err)
	}
}

func TestNoDMUsesIsolatedLLMDecisionAndRejectsOutside(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Runtime.AnswerPolicy.Scope = "配置文档"
	calls := 0
	client := moduleClient(func(_ context.Context, r llm.Request) (llm.Response, error) {
		calls++
		if len(r.Tools) != 0 || len(r.Messages) != 1 || r.MaxTokens != 512 {
			t.Fatal("scope decision used chat path", r)
		}
		return llm.Response{Text: `{"scope":"outside","respond":"yes","evidence":"none"}`}, nil
	})
	sender := registeredSender(t)
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: sender.registry, Secrets: configuredSecrets(t, "key"), ClientFactory: func(llm.ClientConfig) (llm.Client, error) { return client, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	if _, err = manager.HandleInbound(context.Background(), messaging.InboundMessage{ConversationID: "c", UserID: "u", MessageID: "m", Text: "其他专业问题", Mentioned: true}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(sender.sender.Sent()) != 1 {
		t.Fatal("outside request reached answer model")
	}
}

func TestDMCredentialReplacementRebuildsDecisionProvider(t *testing.T) {
	var keys []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Authorization"))
		var input struct {
			Questions map[string]any `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		answers := map[string]any{}
		for name := range input.Questions {
			task := decision.AnswerTask("scope")
			options := task.Questions[name].Options
			value := decision.Unknown
			if name == "scope" {
				value = "outside"
			}
			if name == "respond" {
				value = "yes"
			}
			if name == "evidence" {
				value = "none"
			}
			probabilities := map[string]float64{}
			for option := range options {
				probabilities[option] = 0
			}
			probabilities[value] = 1
			answers[name] = map[string]any{"type": "choice", "choice": value, "confidence": 1, "probabilities": probabilities}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
	}))
	defer api.Close()
	snapshot := testSnapshot()
	snapshot.Runtime.AnswerPolicy.Scope = "scope"
	snapshot.Runtime.Decision.DM = &config.DecisionModelConfig{Provider: "jev", Endpoint: api.URL, APIKeyRef: "dm/key"}
	store := configuredSecrets(t, "answer-key")
	candidate, err := store.Stage(context.Background(), "dm/key", "old-dm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Activate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: registeredSender(t).registry, Secrets: store, ClientFactory: (&recordingFactory{}).New})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	writer, err := secret.NewManagedWriter(store, manager.ReplaceSecret, manager.DeleteSecret)
	if err != nil {
		t.Fatal(err)
	}
	send := func(id string) {
		t.Helper()
		if _, err := manager.HandleInbound(context.Background(), messaging.InboundMessage{ConversationID: "c", UserID: "u", MessageID: id, Text: id, Mentioned: true}); err != nil {
			t.Fatal(err)
		}
	}
	send("first")
	if _, err = writer.Replace(context.Background(), "dm/key", "new-dm"); err != nil {
		t.Fatal(err)
	}
	send("second")
	if len(keys) != 2 || keys[0] != "Bearer old-dm" || keys[1] != "Bearer new-dm" {
		t.Fatal("stale DM credential", keys)
	}
}
