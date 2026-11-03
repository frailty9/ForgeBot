package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/replyexperience"
	"github.com/soleapi/forgebot/internal/settings"
)

type trackedModuleFactory struct {
	mu        sync.Mutex
	instances []*trackedModule
}
type trackedModule struct {
	suffix string
	closes atomic.Int32
	closed chan struct{}
}

func (*trackedModuleFactory) Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: "custom.reply", DefaultEnabled: true, Hooks: []hooks.Stage{hooks.BeforeReply}}
}
func (*trackedModuleFactory) ValidateOptions(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var value struct {
		Suffix string `json:"suffix"`
	}
	return json.Unmarshal(raw, &value)
}
func (*trackedModuleFactory) PublicOptions(json.RawMessage) (any, error) { return nil, nil }
func (f *trackedModuleFactory) Build(_ context.Context, raw json.RawMessage) (modules.Instance, error) {
	var opts struct {
		Suffix string `json:"suffix"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &opts); err != nil {
			return nil, err
		}
	}
	if opts.Suffix == "broken" {
		return nil, errors.New("candidate failed")
	}
	instance := &trackedModule{suffix: opts.Suffix, closed: make(chan struct{})}
	f.mu.Lock()
	f.instances = append(f.instances, instance)
	f.mu.Unlock()
	return instance, nil
}
func (f *trackedModuleFactory) all() []*trackedModule {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*trackedModule(nil), f.instances...)
}
func (i *trackedModule) Close(context.Context) error {
	if i.closes.Add(1) == 1 {
		close(i.closed)
	}
	return nil
}
func (i *trackedModule) Registrations() hooks.Registrations {
	return hooks.Registrations{BeforeReply: []hooks.Registration[hooks.ReplyInput, hooks.ReplyResult]{{ID: "suffix", Handle: func(_ context.Context, _ hooks.Info, input hooks.ReplyInput) (hooks.ReplyResult, error) {
		text := input.Text + i.suffix
		return hooks.ReplyResult{Text: &text}, nil
	}}}}
}
func moduleConfig(suffix string) map[string]config.ModuleConfig {
	raw, _ := json.Marshal(map[string]string{"suffix": suffix})
	return map[string]config.ModuleConfig{"custom.reply": {Options: raw}}
}
func moduleRegistry(t *testing.T, factory modules.Factory) *modules.Registry {
	t.Helper()
	registry := modules.NewRegistry()
	if err := registry.Register(factory, hooks.Native, 100, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	return registry
}

type moduleClient func(context.Context, llm.Request) (llm.Response, error)

func (moduleClient) Protocol() llm.Protocol { return llm.OpenAIResponses }
func (client moduleClient) Complete(ctx context.Context, request llm.Request) (llm.Response, error) {
	return client(ctx, request)
}
func (moduleClient) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

func TestModulesGenerationPinningAndRetirementLimit(t *testing.T) {
	factory := &trackedModuleFactory{}
	sender := registeredSender(t)
	snapshot := testSnapshot()
	snapshot.Static.Server.ShutdownTimeout = time.Millisecond
	snapshot.Runtime.Modules = moduleConfig("-old")
	entered := make(chan struct{})
	release := make(chan struct{})
	var blocked atomic.Bool
	client := moduleClient(func(_ context.Context, _ llm.Request) (llm.Response, error) {
		if blocked.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		return llm.Response{Text: "reply"}, nil
	})
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: sender.registry, Modules: moduleRegistry(t, factory), Secrets: configuredSecrets(t, "key"), ClientFactory: func(llm.ClientConfig) (llm.Client, error) { return client, nil }})
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer manager.Shutdown(context.Background())
	defer unblock()
	done := make(chan error, 1)
	go func() {
		_, err := manager.HandleInbound(context.Background(), messaging.InboundMessage{ConversationID: "c", UserID: "u", MessageID: "old", Text: "hello", Mentioned: true})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request not entered")
	}
	next := snapshot.Clone()
	next.Version = 2
	next.Runtime.Modules = moduleConfig("-new")
	if _, err := manager.Apply(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if factory.all()[0].closes.Load() != 0 {
		t.Fatal("old module closed in use")
	}
	assertReply(t, manager, sender.sender, "reply-new")
	third := next.Clone()
	third.Version = 3
	third.Runtime.Modules = moduleConfig("-third")
	if _, err := manager.Apply(context.Background(), third); !errors.Is(err, ErrRuntimeBusy) {
		t.Fatal(err)
	}
	if len(factory.all()) != 2 {
		t.Fatal("candidate built while busy")
	}
	_, status := manager.Snapshot()
	if len(status.Modules) != 1 || status.Modules[0].State != "active" {
		t.Fatal(status.Modules)
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("old request stuck")
	}
	retirement := manager.retiring
	select {
	case <-retirement:
	case <-time.After(time.Second):
		t.Fatal("retirement stuck")
	}
	sent := sender.sender.Sent()
	if sent[len(sent)-1].Text != "reply-old" {
		t.Fatal("old request switched hooks", sent)
	}
	if _, err := manager.Apply(context.Background(), third); err != nil {
		t.Fatal(err)
	}
	if factory.all()[0].closes.Load() != 1 || factory.all()[1].closes.Load() != 1 {
		t.Fatal("incorrect close count")
	}
}

func TestModulesCandidateFailuresAndSnapshotIsolation(t *testing.T) {
	factory := &trackedModuleFactory{}
	snapshot := testSnapshot()
	snapshot.Runtime.Modules = moduleConfig("-old")
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: registeredSender(t).registry, Modules: moduleRegistry(t, factory)})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	snapshot.Runtime.Modules["custom.reply"].Options[0] = '['
	current, _ := manager.Snapshot()
	current.Runtime.Modules["custom.reply"].Options[0] = '['
	intact, _ := manager.Snapshot()
	if string(intact.Runtime.Modules["custom.reply"].Options) != `{"suffix":"-old"}` {
		t.Fatal("snapshot aliased")
	}
	cfg := intact.Runtime.Clone()
	cfg.Modules = moduleConfig("-new")
	for _, failure := range []error{errors.New("disk failed"), settings.ErrVersionConflict} {
		if _, err := manager.CommitRuntime(context.Background(), cfg, func() (config.Snapshot, error) { return config.Snapshot{}, failure }); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		all := factory.all()
		select {
		case <-all[len(all)-1].closed:
		case <-time.After(time.Second):
			t.Fatal("candidate not closed")
		}
		if all[0].closes.Load() != 0 {
			t.Fatal("old module closed")
		}
	}
	cfg.Modules = moduleConfig("broken")
	persisted := false
	if _, err := manager.CommitRuntime(context.Background(), cfg, func() (config.Snapshot, error) { persisted = true; return config.Snapshot{}, nil }); err == nil || persisted {
		t.Fatal("failed candidate persisted")
	}
	result, err := manager.Apply(context.Background(), intact)
	if err != nil {
		t.Fatal(err)
	}
	result.Snapshot.Runtime.Modules["custom.reply"].Options[0] = '['
	after, _ := manager.Snapshot()
	if after.Runtime.Modules["custom.reply"].Options[0] != '{' {
		t.Fatal("Apply returned alias")
	}
}

func TestNativeModuleDisableAndRollback(t *testing.T) {
	registry := moduleRegistry(t, replyexperience.Factory{})
	sender := registeredSender(t)
	snapshot := testSnapshot()
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: sender.registry, Modules: registry, Secrets: configuredSecrets(t, "test-key"), ClientFactory: func(cfg llm.ClientConfig) (llm.Client, error) {
		return &responseClient{protocol: cfg.Protocol, text: "test reply"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	message := messaging.InboundMessage{ConversationID: "c", UserID: "u", MessageID: "m", Mentioned: true}
	result, err := manager.HandleInbound(context.Background(), message)
	if err != nil || !result.Accepted {
		t.Fatal(result, err)
	}
	next := snapshot.Clone()
	next.Version = 2
	disabled := false
	next.Runtime.Modules = map[string]config.ModuleConfig{replyexperience.ID: {Enabled: &disabled}}
	if _, err := manager.Apply(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	result, err = manager.HandleInbound(context.Background(), message)
	if err != nil || result.Verdict != "empty_input" {
		t.Fatal(result, err)
	}
	_, status := manager.Snapshot()
	if status.Modules[0].State != "disabled" || len(status.Modules[0].Hooks) != 0 {
		t.Fatal(status.Modules)
	}
	restored := snapshot.Clone()
	restored.Version = 3
	if _, err := manager.Apply(context.Background(), restored); err != nil {
		t.Fatal(err)
	}
	message.MessageID = "restored"
	result, err = manager.HandleInbound(context.Background(), message)
	if err != nil || !result.Accepted {
		t.Fatal(result, err)
	}
	public, err := manager.PublicModules(restored.Runtime)
	if err != nil || public[replyexperience.ID].(map[string]any)["enabled"] != true {
		t.Fatal(public, err)
	}
}
