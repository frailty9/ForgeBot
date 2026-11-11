package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/platform"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
)

type answerClient struct{ requests chan llm.Request }

func (answerClient) Protocol() llm.Protocol { return llm.OpenAIResponses }
func (c answerClient) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	c.requests <- request
	return llm.Response{Text: "controlled answer"}, nil
}
func (answerClient) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

func TestControlledPollingToRuntimeAnswerAndRecall(t *testing.T) {
	a := testAdapter(t)
	sent := make(chan map[string]json.RawMessage, 1)
	requests := make(chan llm.Request, 1)
	var recalled atomic.Int32
	poll := startupTransport(t, func(r *http.Request) (*http.Response, error) {
		if readPoll(t, r).Timeout == 0 {
			m := testMessage()
			m.Text = "@ForgeBot question"
			m.Entities = []entity{{Type: "mention", Offset: 0, Length: 9}}
			body, _ := json.Marshal(map[string]any{"ok": true, "result": []update{{ID: 10, Message: &m}}})
			return response(200, string(body)), nil
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			sent <- body
			return response(200, `{"ok":true,"result":{"message_id":100}}`), nil
		case strings.HasSuffix(r.URL.Path, "/deleteMessage"):
			recalled.Add(1)
			return response(200, `{"ok":true,"result":true}`), nil
		default:
			return poll(r)
		}
	})
	registry := platform.NewRegistry()
	if err := registry.RegisterConnector(a.ID(), a); err != nil {
		t.Fatal(err)
	}
	if err := registry.BindConversation(platform.ConversationBinding{ConversationID: "support", ConnectorID: a.ID(), Target: "-100"}); err != nil {
		t.Fatal(err)
	}
	snapshot := config.DefaultSnapshot()
	snapshot.Runtime.LLM.Authentication = config.AuthenticationNone
	snapshot.Runtime.LLM.Model = "controlled-model"
	snapshot.Runtime.Platforms = config.PlatformConfig{Connectors: []config.ConnectorConfig{{ID: a.ID(), Type: "telegram", Enabled: true}}, Groups: []config.GroupBindingConfig{{ID: "support", ConnectorID: a.ID(), Target: "-100"}}}
	manager, err := runtimecfg.NewManager(context.Background(), snapshot, runtimecfg.Builder{Sender: registry, ClientFactory: func(llm.ClientConfig) (llm.Client, error) { return answerClient{requests}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	if err := a.Start(context.Background(), manager); err != nil {
		t.Fatal(err)
	}
	defer a.Stop(context.Background())
	select {
	case body := <-sent:
		if string(body["text"]) != `"controlled answer"` || string(body["chat_id"]) != "-100" || !strings.Contains(string(body["reply_parameters"]), `"message_id":42`) {
			t.Fatal(body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("polling update did not produce answer")
	}
	request := <-requests
	data, _ := json.Marshal(request)
	if !strings.Contains(string(data), "question") || strings.Contains(string(data), a.cfg.Token) {
		t.Fatal("invalid model input")
	}
	port, err := manager.ForConversation("support")
	if err != nil {
		t.Fatal(err)
	}
	if err := port.Recall(context.Background(), "42"); err != nil || recalled.Load() != 1 {
		t.Fatal(err, recalled.Load())
	}
	support, err := registry.Support(context.Background(), "support")
	if err != nil || !support.MessageRecall || !support.TextMessages || support.PassiveRepliesOnly || support.OutboundSegments || support.JoinReview || support.MemberEvents || support.JoinEvents {
		t.Fatal(support, err)
	}
	if _, ok := any(a).(group.Reviewer); ok {
		t.Fatal("Telegram must not expose automatic join review")
	}
}
