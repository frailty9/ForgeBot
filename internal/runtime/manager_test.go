package runtime

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/notification"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/platform/memory"
	"github.com/soleapi/forgebot/internal/secret"
)

func TestManagerAppliesLLMChangeAndPreservesOldRuntimeOnFailure(t *testing.T) {
	ctx := context.Background()
	store := configuredSecrets(t, "old-key")
	sender := registeredSender(t)
	factory := &recordingFactory{failModel: "broken"}
	snapshot := testSnapshot()
	manager, err := NewManager(ctx, snapshot, Builder{Sender: sender.registry, Secrets: store, ClientFactory: factory.New})
	if err != nil {
		t.Fatal(err)
	}

	next := snapshot
	next.Version = 2
	next.Runtime.LLM.Model = "model-v2"
	result, err := manager.Apply(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionReplaceLLM || result.Status.Version != 2 {
		t.Fatalf("result = %+v", result)
	}
	assertReply(t, manager, sender.sender, "model-v2:old-key")

	broken := next
	broken.Version = 3
	broken.Runtime.LLM.Model = "broken"
	if _, err := manager.Apply(ctx, broken); err == nil {
		t.Fatal("broken client accepted")
	}
	current, status := manager.Snapshot()
	if current.Version != 2 || current.Runtime.LLM.Model != "model-v2" {
		t.Fatalf("current = %+v", current)
	}
	if status.State != StateReady || status.Version != 2 || status.Error == "" {
		t.Fatalf("status = %+v", status)
	}
	assertReply(t, manager, sender.sender, "model-v2:old-key")
}

func TestManagerRejectsStaticConfigChange(t *testing.T) {
	snapshot := testSnapshot()
	sender := registeredSender(t)
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: sender.registry})
	if err != nil {
		t.Fatal(err)
	}
	next := snapshot
	next.Static.Server.Addr = "127.0.0.1:9999"
	if _, err := manager.Apply(context.Background(), next); !errors.Is(err, ErrStaticConfigChanged) {
		t.Fatalf("error = %v", err)
	}
}

func TestCommitRuntimeNoopStillUsesPersistence(t *testing.T) {
	snapshot := testSnapshot()
	sender := registeredSender(t)
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: sender.registry})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	result, err := manager.CommitRuntime(context.Background(), snapshot.Runtime, func() (config.Snapshot, error) {
		called = true
		next := snapshot
		next.Version = 2
		return next, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called || result.Action != ActionNone || result.Snapshot.Version != 2 {
		t.Fatalf("result = %+v, called = %v", result, called)
	}
}

func TestCommitRuntimeUpdatesLogInPlace(t *testing.T) {
	snapshot := testSnapshot()
	sender := registeredSender(t)
	applied := config.LogConfig{}
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: sender.registry, ApplyLog: func(cfg config.LogConfig) { applied = cfg }})
	if err != nil {
		t.Fatal(err)
	}
	nextRuntime := snapshot.Runtime
	nextRuntime.Log.Level = "debug"
	result, err := manager.CommitRuntime(context.Background(), nextRuntime, func() (config.Snapshot, error) {
		next := snapshot
		next.Version = 2
		next.Runtime = nextRuntime
		return next, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionUpdateLog || applied.Level != "debug" {
		t.Fatalf("result=%+v applied=%+v", result, applied)
	}
}

func TestManagedSecretReplacementSwitchesOnlyAfterValidation(t *testing.T) {
	ctx := context.Background()
	store := configuredSecrets(t, "old-key")
	sender := registeredSender(t)
	factory := &recordingFactory{failKey: "bad-key"}
	manager, err := NewManager(ctx, testSnapshot(), Builder{Sender: sender.registry, Secrets: store, ClientFactory: factory.New})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := secret.NewManagedWriter(store, manager.ReplaceSecret, manager.DeleteSecret)
	if err != nil {
		t.Fatal(err)
	}

	status, err := writer.Replace(ctx, "llm/default", "new-key")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Configured || status.Version != 2 {
		t.Fatalf("status = %+v", status)
	}
	assertReply(t, manager, sender.sender, "model-v1:new-key")

	if _, err := writer.Replace(ctx, "llm/default", "bad-key"); err == nil {
		t.Fatal("bad key accepted")
	}
	value, err := store.Get(ctx, "llm/default")
	if err != nil {
		t.Fatal(err)
	}
	if value != "new-key" {
		t.Fatalf("active key = %q", value)
	}
	assertReply(t, manager, sender.sender, "model-v1:new-key")
}

func TestManagerOwnsConnectorLifecycleAndReplacesPlatformGeneration(t *testing.T) {
	snapshot := testSnapshot()
	var sets []*fakeConnectorSet
	factory := func(config.PlatformConfig) (platform.ConnectorSet, error) {
		set := newFakeConnectorSet()
		sets = append(sets, set)
		return set, nil
	}
	manager, err := NewManager(context.Background(), snapshot, Builder{ConnectorFactory: factory})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, status := manager.Snapshot()
	if len(status.Connectors) != 1 || status.Connectors[0].State != platform.ConnectorRunning {
		t.Fatalf("status = %+v", status)
	}

	nextRuntime := snapshot.Runtime
	nextRuntime.Platforms.Groups = append([]config.GroupBindingConfig(nil), snapshot.Runtime.Platforms.Groups...)
	nextRuntime.Platforms.Groups[0].Target = "next"
	result, err := manager.CommitRuntime(context.Background(), nextRuntime, func() (config.Snapshot, error) {
		next := snapshot
		next.Version = 2
		next.Runtime = nextRuntime
		return next, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionRestartConnectors || len(sets) != 2 {
		t.Fatalf("result=%+v sets=%d", result, len(sets))
	}
	if sets[0].starts != 1 || sets[0].stops != 1 || sets[0].running {
		t.Fatalf("old set = %+v", sets[0])
	}
	if sets[1].starts != 1 || sets[1].stops != 0 || !sets[1].running {
		t.Fatalf("new set = %+v", sets[1])
	}
}

func TestManagerRestoresConnectorsWhenCandidateStartFails(t *testing.T) {
	snapshot := testSnapshot()
	var sets []*fakeConnectorSet
	factory := func(cfg config.PlatformConfig) (platform.ConnectorSet, error) {
		set := newFakeConnectorSet()
		if cfg.Groups[0].Target == "broken" {
			set.failStart = errors.New("listen failed")
		}
		sets = append(sets, set)
		return set, nil
	}
	manager, err := NewManager(context.Background(), snapshot, Builder{ConnectorFactory: factory})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	nextRuntime := snapshot.Runtime
	nextRuntime.Platforms.Groups = append([]config.GroupBindingConfig(nil), snapshot.Runtime.Platforms.Groups...)
	nextRuntime.Platforms.Groups[0].Target = "broken"
	persisted := false
	_, err = manager.CommitRuntime(context.Background(), nextRuntime, func() (config.Snapshot, error) {
		persisted = true
		return config.Snapshot{}, nil
	})
	if err == nil || persisted {
		t.Fatalf("err=%v persisted=%v", err, persisted)
	}
	current, _ := manager.Snapshot()
	if current.Version != snapshot.Version || len(sets) != 2 || sets[0].starts != 2 || !sets[0].running {
		t.Fatalf("current=%+v old=%+v", current, sets[0])
	}
}

func TestManagerRestoresConnectorsWhenPersistenceFails(t *testing.T) {
	snapshot := testSnapshot()
	var sets []*fakeConnectorSet
	factory := func(config.PlatformConfig) (platform.ConnectorSet, error) {
		set := newFakeConnectorSet()
		sets = append(sets, set)
		return set, nil
	}
	manager, err := NewManager(context.Background(), snapshot, Builder{ConnectorFactory: factory})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	nextRuntime := snapshot.Runtime
	nextRuntime.Platforms.Groups = append([]config.GroupBindingConfig(nil), snapshot.Runtime.Platforms.Groups...)
	nextRuntime.Platforms.Groups[0].Target = "next"
	_, err = manager.CommitRuntime(context.Background(), nextRuntime, func() (config.Snapshot, error) {
		return config.Snapshot{}, errors.New("disk full")
	})
	if err == nil {
		t.Fatal("persistence failure accepted")
	}
	current, _ := manager.Snapshot()
	if current.Version != snapshot.Version || len(sets) != 2 {
		t.Fatalf("current=%+v sets=%d", current, len(sets))
	}
	if sets[0].starts != 2 || sets[0].stops != 1 || !sets[0].running {
		t.Fatalf("old set = %+v", sets[0])
	}
	if sets[1].starts != 1 || sets[1].stops != 1 || sets[1].running {
		t.Fatalf("candidate set = %+v", sets[1])
	}
}

type fakeConnectorSet struct {
	starts    int
	stops     int
	running   bool
	failStart error
	errors    chan error
}

func TestNotificationTargetsFollowCurrentRuntimeSnapshot(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Runtime.Operations.Groups = []config.OperatorGroupConfig{{
		ConversationID: "local", Notifications: true, Commands: []string{"status"},
	}}
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: registeredSender(t).registry})
	if err != nil {
		t.Fatal(err)
	}
	if targets := manager.NotificationTargets(); len(targets) != 1 || targets[0] != "local" {
		t.Fatalf("targets = %v", targets)
	}
	next := snapshot
	next.Version = 2
	next.Runtime.Operations.Groups = []config.OperatorGroupConfig{{
		ConversationID: "local", Notifications: false, Commands: []string{"status"},
	}}
	if _, err := manager.Apply(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if targets := manager.NotificationTargets(); len(targets) != 0 {
		t.Fatalf("targets = %v", targets)
	}
}

func TestConnectorFailureDegradesRuntimeAndPublishesEvent(t *testing.T) {
	set := newFakeConnectorSet()
	publisher := &recordingPublisher{events: make(chan notification.Event, 2)}
	manager, err := NewManager(context.Background(), testSnapshot(), Builder{
		ConnectorFactory: func(config.PlatformConfig) (platform.ConnectorSet, error) { return set, nil },
		Notifications:    publisher,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	set.errors <- errors.New("connection lost")
	select {
	case event := <-publisher.events:
		if event.Level != notification.LevelError || event.Component != "runtime" || event.Code != string(ActionRestartConnectors) {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("notification was not published")
	}
	_, status := manager.Snapshot()
	if status.State != StateDegraded || status.Error != "connection lost" {
		t.Fatalf("status = %+v", status)
	}
}

type recordingPublisher struct{ events chan notification.Event }

func (p *recordingPublisher) Publish(event notification.Event) { p.events <- event }

func newFakeConnectorSet() *fakeConnectorSet {
	return &fakeConnectorSet{errors: make(chan error), running: false}
}

func (s *fakeConnectorSet) Send(context.Context, messaging.OutboundMessage) error { return nil }
func (s *fakeConnectorSet) Start(context.Context, platform.Handler) error {
	s.starts++
	if s.failStart != nil {
		return s.failStart
	}
	s.running = true
	return nil
}
func (s *fakeConnectorSet) Stop(context.Context) error {
	s.stops++
	s.running = false
	return nil
}
func (s *fakeConnectorSet) Errors() <-chan error { return s.errors }
func (s *fakeConnectorSet) Status() []platform.ConnectorStatus {
	state := platform.ConnectorStopped
	if s.running {
		state = platform.ConnectorRunning
	}
	return []platform.ConnectorStatus{{ID: "fake", State: state}}
}

type recordingFactory struct {
	mu        sync.Mutex
	failModel string
	failKey   string
}

func (f *recordingFactory) New(cfg llm.ClientConfig) (llm.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cfg.Model == f.failModel || cfg.APIKey == f.failKey {
		return nil, errors.New("client validation failed")
	}
	return &responseClient{protocol: cfg.Protocol, text: cfg.Model + ":" + cfg.APIKey}, nil
}

type responseClient struct {
	protocol llm.Protocol
	text     string
}

func (c *responseClient) Protocol() llm.Protocol { return c.protocol }
func (c *responseClient) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{Text: c.text}, nil
}
func (c *responseClient) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

type senderFixture struct {
	registry *platform.Registry
	sender   *memory.Sender
}

func registeredSender(t *testing.T) senderFixture {
	t.Helper()
	registry := platform.NewRegistry()
	sender := memory.NewSender(slog.Default())
	if err := registry.RegisterConnector("echo", sender); err != nil {
		t.Fatal(err)
	}
	if err := registry.BindConversation(platform.ConversationBinding{ConversationID: "c", ConnectorID: "echo", Target: "c"}); err != nil {
		t.Fatal(err)
	}
	return senderFixture{registry: registry, sender: sender}
}

func configuredSecrets(t *testing.T, value string) *secret.FileStore {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	store, err := secret.NewFileStore(filepath.Join(t.TempDir(), "secrets.json"), key)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.Stage(context.Background(), "llm/default", value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Activate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	return store
}

func testSnapshot() config.Snapshot {
	snapshot := config.DefaultSnapshot()
	snapshot.Static.Server.ShutdownTimeout = 50 * time.Millisecond
	snapshot.Runtime.LLM.Model = "model-v1"
	return snapshot
}

func assertReply(t *testing.T, manager *Manager, sender *memory.Sender, expected string) {
	t.Helper()
	before := len(sender.Sent())
	result, err := manager.HandleInbound(context.Background(), messaging.InboundMessage{
		ConversationID: "c", UserID: "u", MessageID: "m", Text: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted {
		t.Fatalf("result = %+v", result)
	}
	sent := sender.Sent()
	if len(sent) != before+1 || sent[len(sent)-1].Text != expected {
		t.Fatalf("sent = %+v, want %q", sent, expected)
	}
}
