package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestDisabledDMUsesLLMWithoutResolvingCredential(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Runtime.Decision.DM = &config.DecisionModelConfig{Disabled: true, Provider: "jev", Endpoint: "http://127.0.0.1:1", APIKeyRef: "unused"}
	modelCalls := 0
	client := moduleClient(func(context.Context, llm.Request) (llm.Response, error) {
		modelCalls++
		return llm.Response{Text: `{"matches":"yes"}`}, nil
	})
	builder, err := (Builder{Secrets: forbiddenDMSecrets{t}}).withDecision(context.Background(), snapshot, client, "", "")
	if err != nil {
		t.Fatal(err)
	}
	matched, err := decision.DecideBool(context.Background(), builder.Decisions, "rule", json.RawMessage(`{"message":"hello"}`))
	if err != nil || !matched || modelCalls != 1 {
		t.Fatalf("disabled DM not bypassed: %v %v calls=%d", matched, err, modelCalls)
	}
}

// 明确 @ 只请求回答模型一次，范围来自 system_prompt；无 DM 时决策走隔离的 LLM 路径。
func TestMentionAnswersOnceAndNoDMDecisionStaysIsolated(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Runtime.Bot.SystemPrompt = "只回答配置文档相关问题"
	calls := 0
	client := moduleClient(func(_ context.Context, r llm.Request) (llm.Response, error) {
		calls++
		if !strings.Contains(r.Instructions, "配置文档") {
			t.Error("system prompt missing from request instructions")
		}
		return llm.Response{Text: "answer"}, nil
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
		t.Fatalf("mention made %d model calls", calls)
	}
	isolated := moduleClient(func(_ context.Context, r llm.Request) (llm.Response, error) {
		if len(r.Tools) != 0 || len(r.Messages) != 1 || r.MaxTokens != 512 {
			t.Error("decision used chat path", r)
		}
		return llm.Response{Text: `{"matches":"no"}`}, nil
	})
	builder, err := (Builder{}).withDecision(context.Background(), snapshot, isolated, "", "")
	if err != nil {
		t.Fatal(err)
	}
	matched, err := decision.DecideBool(context.Background(), builder.Decisions, "rule", json.RawMessage(`{"message":"hello"}`))
	if err != nil || matched {
		t.Fatalf("%v %v", matched, err)
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
			probabilities := map[string]float64{"yes": 1, "no": 0, decision.Unknown: 0}
			value := "yes"
			answers[name] = map[string]any{"type": "choice", "choice": value, "confidence": 1, "probabilities": probabilities}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
	}))
	defer api.Close()
	snapshot := testSnapshot()
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
	// 直接调用当前代的共享服务，验证凭据替换后重建 DM 供应商。
	send := func(id string) {
		t.Helper()
		current := manager.acquire()
		defer current.mu.RUnlock()
		if _, err := decision.DecideBool(context.Background(), current.decisions, "rule", json.RawMessage(`{"message":"`+id+`"}`)); err != nil {
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
