package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/structuredsearch"
	"github.com/soleapi/forgebot/internal/notification"
	searchcontract "github.com/soleapi/forgebot/internal/search"
)

func searchRegistry(t *testing.T) *modules.Registry {
	t.Helper()
	registry := modules.NewRegistry()
	if err := registry.Register(structuredsearch.Factory{}, hooks.Native, 300, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	return registry
}

func searchSnapshot(t *testing.T, root string) config.Snapshot {
	t.Helper()
	snapshot := testSnapshot()
	enabled := true
	options, err := json.Marshal(map[string]any{"document_root": root})
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Runtime.Modules = map[string]config.ModuleConfig{
		structuredsearch.ID: {Enabled: &enabled, Options: options},
	}
	return snapshot
}

func documentRoot(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "guide.md"), []byte("# Guide\n\nAuthentication uses an API key."), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestManagerBuildsSearchModuleAndClassifiesModuleChanges(t *testing.T) {
	firstRoot := documentRoot(t, "first")
	snapshot := searchSnapshot(t, firstRoot)
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: registeredSender(t).registry, Modules: searchRegistry(t)})
	if err != nil {
		t.Fatal(err)
	}
	_, status := manager.Snapshot()
	if !status.Search.Enabled || status.Search.State != searchcontract.StateReady {
		t.Fatalf("search status = %+v", status.Search)
	}
	current, _ := manager.Snapshot()
	next := current.Runtime
	secondRoot := documentRoot(t, "second")
	enabled := true
	options, _ := json.Marshal(map[string]any{"document_root": secondRoot})
	next.Modules = map[string]config.ModuleConfig{structuredsearch.ID: {Enabled: &enabled, Options: options}}
	result, err := manager.CommitRuntime(context.Background(), next, persistedSnapshot(current, 2, next))
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionReplaceModules {
		t.Fatalf("action = %s", result.Action)
	}
}

func TestManagerSearchCandidateFailureDoesNotPersist(t *testing.T) {
	snapshot := searchSnapshot(t, documentRoot(t, "working"))
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: registeredSender(t).registry, Modules: searchRegistry(t)})
	if err != nil {
		t.Fatal(err)
	}
	next := snapshot.Runtime
	enabled := true
	options, _ := json.Marshal(map[string]any{"document_root": filepath.Join(t.TempDir(), "missing")})
	next.Modules = map[string]config.ModuleConfig{structuredsearch.ID: {Enabled: &enabled, Options: options}}
	persisted := false
	_, err = manager.CommitRuntime(context.Background(), next, func() (config.Snapshot, error) {
		persisted = true
		return config.Snapshot{}, nil
	})
	if err == nil || persisted {
		t.Fatalf("err=%v persisted=%v", err, persisted)
	}
	if _, reloadErr := manager.ReloadSearch(context.Background(), ""); reloadErr != nil {
		t.Fatal(reloadErr)
	}
}

func TestManagerReloadSearchPublishesSourceAwareEvent(t *testing.T) {
	snapshot := searchSnapshot(t, documentRoot(t, "working"))
	publisher := &recordingPublisher{events: make(chan notification.Event, 2)}
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: registeredSender(t).registry, Modules: searchRegistry(t), Notifications: publisher})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.ReloadSearch(context.Background(), "ops")
	if err != nil || result.Documents != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	event := <-publisher.events
	if event.Component != "search" || event.Code != "reloaded" || event.ExcludeConversationID != "ops" {
		t.Fatalf("event = %+v", event)
	}
}

func persistedSnapshot(current config.Snapshot, version uint64, runtime config.RuntimeConfig) Persist {
	return func() (config.Snapshot, error) {
		next := current
		next.Version = version
		next.Runtime = runtime
		return next, nil
	}
}
