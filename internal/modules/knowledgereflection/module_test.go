package knowledgereflection_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/agent"
	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/conversation"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/knowledge"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/knowledgereflection"
	reflectioncontract "github.com/soleapi/forgebot/internal/reflection"
	searchcontract "github.com/soleapi/forgebot/internal/search"
)

type fakeCompleter struct{}

func (fakeCompleter) Model() string { return "test-model" }
func (fakeCompleter) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{Text: `{"accept":true,"title":"Key rotation","question":"How?","answer":"Rotate it.","tags":["key"],"confidence":0.99,"reason":"reusable"}`}, nil
}

type fakeSearch struct{ reloads int }

func (*fakeSearch) Search(context.Context, searchcontract.Query) (searchcontract.Response, error) {
	return searchcontract.Response{}, nil
}
func (s *fakeSearch) Reload(context.Context) (searchcontract.ReloadResult, error) {
	s.reloads++
	return searchcontract.ReloadResult{Documents: 1}, nil
}
func (*fakeSearch) Status() searchcontract.Status {
	return searchcontract.Status{Enabled: true, State: searchcontract.StateReady}
}

type fakePublisher struct{ entries []knowledge.Entry }

func (p *fakePublisher) Publish(entry knowledge.Entry) (knowledge.PublishResult, error) {
	p.entries = append(p.entries, entry)
	return knowledge.PublishResult{Status: knowledge.Published}, nil
}

type fakePublisherFactory struct{ publisher *fakePublisher }

func (f fakePublisherFactory) New(string) (knowledge.Publisher, error) { return f.publisher, nil }

func reflectionSet(t *testing.T) (*modules.Set, *fakeSearch, *fakePublisher) {
	t.Helper()
	registry := modules.NewRegistry()
	if err := registry.Register(knowledgereflection.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	search := &fakeSearch{}
	publisher := &fakePublisher{}
	initial := capability.NewValues()
	_ = initial.Provide(agent.CompleterKey.ID(), agent.Completer(fakeCompleter{}))
	_ = initial.Provide(searchcontract.ControlKey.ID(), searchcontract.Control(search))
	_ = initial.Provide(knowledge.PublisherFactoryKey.ID(), knowledge.PublisherFactory(fakePublisherFactory{publisher}))
	_ = initial.Provide(conversation.CatalogKey.ID(), conversation.Catalog(conversation.Set{"support": {}}))
	enabled := true
	options, _ := json.Marshal(map[string]any{"conversation_ids": []string{"support"}, "quiet_window": "1ms", "max_session_duration": "1s", "scan_interval": "1h"})
	set, err := registry.BuildWithCapabilities(context.Background(), map[string]config.ModuleConfig{knowledgereflection.ID: {Enabled: &enabled, Options: options}}, nil, initial)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	return set, search, publisher
}

func TestObserverScanPublishesAndReloads(t *testing.T) {
	set, search, publisher := reflectionSet(t)
	observer := set.Observers()[0]
	now := time.Now().Add(-time.Second)
	observer.Observe(messaging.InboundMessage{ConversationID: "support", UserID: "member", MessageID: "q", Text: "How?", At: now})
	observer.Observe(messaging.InboundMessage{ConversationID: "support", UserID: "admin", MessageID: "a", ReplyToMessageID: "q", Text: "Rotate it.", Administrator: true, At: now.Add(time.Millisecond)})
	control, ok := capability.Resolve(set.Capabilities(), reflectioncontract.ControlKey)
	if !ok {
		t.Fatal("reflection control is missing")
	}
	result, err := control.Scan(context.Background())
	if err != nil || result.Processed != 1 || len(publisher.entries) != 1 || search.reloads != 1 {
		t.Fatalf("result=%+v err=%v entries=%d reloads=%d", result, err, len(publisher.entries), search.reloads)
	}
}

func TestBackgroundJobStartsOnlyAfterActivation(t *testing.T) {
	set, _, _ := reflectionSet(t)
	control, _ := capability.Resolve(set.Capabilities(), reflectioncontract.ControlKey)
	if control.Status().Active {
		t.Fatal("candidate job started before activation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	set.Start(ctx)
	deadline := time.Now().Add(time.Second)
	for !control.Status().Active && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !control.Status().Active {
		t.Fatal("reflection job did not activate")
	}
	cancel()
}

func TestBuildRejectsMissingCapabilitiesAndUnknownConversation(t *testing.T) {
	registry := modules.NewRegistry()
	if err := registry.Register(knowledgereflection.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	enabled := true
	options := json.RawMessage(`{"conversation_ids":["missing"]}`)
	if _, err := registry.Build(context.Background(), map[string]config.ModuleConfig{knowledgereflection.ID: {Enabled: &enabled, Options: options}}, nil); err == nil {
		t.Fatal("missing dependencies were accepted")
	}
}
