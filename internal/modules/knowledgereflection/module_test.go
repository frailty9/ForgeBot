package knowledgereflection_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/agent"
	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/conversation"
	"github.com/soleapi/forgebot/internal/history"
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

type fakeSearch struct{ reloads atomic.Int64 }

func (*fakeSearch) Search(context.Context, searchcontract.Query) (searchcontract.Response, error) {
	return searchcontract.Response{}, nil
}
func (s *fakeSearch) Reload(context.Context) (searchcontract.ReloadResult, error) {
	s.reloads.Add(1)
	return searchcontract.ReloadResult{Documents: 1}, nil
}
func (*fakeSearch) Status() searchcontract.Status {
	return searchcontract.Status{Enabled: true, State: searchcontract.StateReady}
}

type fakePublisher struct {
	mu      sync.Mutex
	entries []knowledge.Entry
}

func (p *fakePublisher) Publish(entry knowledge.Entry) (knowledge.PublishResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries = append(p.entries, entry)
	return knowledge.PublishResult{Status: knowledge.Published}, nil
}

func (p *fakePublisher) snapshot() []knowledge.Entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]knowledge.Entry(nil), p.entries...)
}

type fakePublisherFactory struct{ publisher *fakePublisher }

func (f fakePublisherFactory) New(string) (knowledge.Publisher, error) { return f.publisher, nil }

func reflectionSet(t *testing.T, interval string) (*modules.Set, *fakeSearch, *fakePublisher, *history.Window) {
	t.Helper()
	registry := modules.NewRegistry()
	if err := registry.Register(knowledgereflection.Factory{}, hooks.Native, 0, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	search := &fakeSearch{}
	publisher := &fakePublisher{}
	initial := capability.NewValues()
	window := history.New(history.Policy{}, []string{"support"}, nil)
	_ = initial.Provide(history.ReaderKey.ID(), history.Reader(window))
	_ = initial.Provide(agent.CompleterKey.ID(), agent.Completer(fakeCompleter{}))
	_ = initial.Provide(searchcontract.ControlKey.ID(), searchcontract.Control(search))
	_ = initial.Provide(knowledge.PublisherFactoryKey.ID(), knowledge.PublisherFactory(fakePublisherFactory{publisher}))
	_ = initial.Provide(conversation.CatalogKey.ID(), conversation.Catalog(conversation.Set{"support": {}}))
	enabled := true
	options, _ := json.Marshal(map[string]any{"conversation_ids": []string{"support"}, "quiet_window": "1ms", "max_session_duration": "1s", "scan_interval": interval})
	set, err := registry.BuildWithCapabilities(context.Background(), map[string]config.ModuleConfig{knowledgereflection.ID: {Enabled: &enabled, Options: options}}, nil, initial)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	window.SetSubscribers(set.HistorySubscribers())
	return set, search, publisher, window
}

func TestHistoryScanPublishesAndReloads(t *testing.T) {
	set, search, publisher, window := reflectionSet(t, "1h")
	if len(set.Observers()) != 0 || len(set.HistorySubscribers()) != 1 || set.HistorySubscribers()[0].Evicted != nil {
		t.Fatal("reflection must only subscribe to new history messages")
	}
	window.Append(history.FromInbound(messaging.InboundMessage{ConversationID: "support", UserID: "member", MessageID: "q", Text: "How?"}))
	window.Append(history.FromInbound(messaging.InboundMessage{ConversationID: "support", UserID: "admin", MessageID: "a", ReplyToMessageID: "q", Text: "Rotate it.", PlatformRole: "admin"}))
	control, ok := capability.Resolve(set.Capabilities(), reflectioncontract.ControlKey)
	if !ok {
		t.Fatal("reflection control is missing")
	}
	time.Sleep(5 * time.Millisecond)
	result, err := control.Scan(context.Background())
	if err != nil || result.Processed != 1 || len(publisher.snapshot()) != 1 || search.reloads.Load() != 1 {
		t.Fatalf("result=%+v err=%v entries=%d reloads=%d", result, err, len(publisher.snapshot()), search.reloads.Load())
	}
}

func TestBackgroundJobStartsOnlyAfterActivation(t *testing.T) {
	set, _, _, _ := reflectionSet(t, "1h")
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

func TestPeriodicJobPublishesWithoutAnotherMessage(t *testing.T) {
	set, _, publisher, window := reflectionSet(t, "2ms")
	window.Append(history.FromInbound(messaging.InboundMessage{ConversationID: "support", UserID: "member", MessageID: "q", Text: "How?"}))
	window.Append(history.FromInbound(messaging.InboundMessage{ConversationID: "support", UserID: "admin", MessageID: "a", ReplyToMessageID: "q", Text: "Rotate it.", PlatformRole: "owner"}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	set.Start(ctx)
	deadline := time.Now().Add(time.Second)
	for len(publisher.snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(publisher.snapshot()) != 1 {
		t.Fatal("quiet session was not processed by the periodic job")
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	window.Append(history.FromInbound(messaging.InboundMessage{ConversationID: "support", UserID: "admin", MessageID: "later", ReplyToMessageID: "q", Text: "Later.", PlatformRole: "admin"}))
	control, _ := capability.Resolve(set.Capabilities(), reflectioncontract.ControlKey)
	if status := control.Status(); status.Active || status.Pending.Sessions != 0 {
		t.Fatalf("retired reflection accepted work: %+v", status)
	}
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
