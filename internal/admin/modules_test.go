package admin

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/replyexperience"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/platform/memory"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
	"github.com/soleapi/forgebot/internal/settings"
)

type memoryConfigStore struct {
	mu       sync.Mutex
	snapshot config.Snapshot
	versions map[uint64]config.RuntimeConfig
}

var _ settings.Store = (*memoryConfigStore)(nil)

func (s *memoryConfigStore) Current(context.Context) (config.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot.Clone(), nil
}
func (s *memoryConfigStore) RuntimeAt(_ context.Context, version uint64) (config.RuntimeConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.versions[version]
	if !ok {
		return config.RuntimeConfig{}, settings.ErrVersionNotFound
	}
	return value.Clone(), nil
}
func (s *memoryConfigStore) Update(_ context.Context, request settings.UpdateRequest) (config.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.ExpectedVersion != s.snapshot.Version {
		return config.Snapshot{}, settings.ErrVersionConflict
	}
	s.snapshot.Version++
	s.snapshot.Runtime = request.Runtime.Clone()
	s.versions[s.snapshot.Version] = request.Runtime.Clone()
	return s.snapshot.Clone(), nil
}
func (s *memoryConfigStore) Rollback(ctx context.Context, target, expected uint64, source string) (config.Snapshot, error) {
	runtime, err := s.RuntimeAt(ctx, target)
	if err != nil {
		return config.Snapshot{}, err
	}
	return s.Update(ctx, settings.UpdateRequest{ExpectedVersion: expected, Runtime: runtime, Source: source})
}
func (s *memoryConfigStore) History(context.Context, int) ([]settings.RevisionMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []settings.RevisionMeta
	for version := s.snapshot.Version; version > 0; version-- {
		result = append(result, settings.RevisionMeta{Version: version})
	}
	return result, nil
}

func TestModuleAdminFlowUsesStoreInterface(t *testing.T) {
	ctx := context.Background()
	snapshot := config.DefaultSnapshot()
	registry := modules.NewRegistry()
	if err := registry.Register(replyexperience.Factory{}, hooks.Native, 100, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	sender := platform.NewRegistry()
	if err := sender.RegisterConnector("echo", memory.NewSender(nil)); err != nil {
		t.Fatal(err)
	}
	if err := sender.BindConversation(platform.ConversationBinding{ConversationID: "local", ConnectorID: "echo", Target: "local"}); err != nil {
		t.Fatal(err)
	}
	manager, err := runtimecfg.NewManager(ctx, snapshot, runtimecfg.Builder{Sender: sender, Modules: registry})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(ctx)
	store := &memoryConfigStore{snapshot: snapshot.Clone(), versions: map[uint64]config.RuntimeConfig{1: snapshot.Runtime.Clone()}}
	service, err := New(manager, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	invalid := snapshot.Runtime.Clone()
	invalid.Modules = map[string]config.ModuleConfig{replyexperience.ID: {Options: json.RawMessage(`{"unknown":1}`)}}
	if err := service.Validate(invalid); err == nil {
		t.Fatal("invalid module passed admin validation")
	}
	if service.RuntimeStatus().Version != 1 {
		t.Fatal("validation changed runtime")
	}
	disabled := false
	updated := snapshot.Runtime.Clone()
	updated.Modules = map[string]config.ModuleConfig{replyexperience.ID: {Enabled: &disabled}}
	result, err := service.Update(ctx, UpdateRequest{ExpectedVersion: 1, Runtime: updated})
	if err != nil || result.Status.Version != 2 {
		t.Fatal(result, err)
	}
	public, err := service.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	module := public["runtime"].(map[string]any)["modules"].(map[string]any)[replyexperience.ID].(map[string]any)
	if module["enabled"] != false || service.RuntimeStatus().Modules[0].State != "disabled" {
		t.Fatal(module)
	}
	if _, err := service.Update(ctx, UpdateRequest{ExpectedVersion: 1, Runtime: snapshot.Runtime}); !errors.Is(err, settings.ErrVersionConflict) {
		t.Fatal(err)
	}
	restored, err := service.Rollback(ctx, RollbackRequest{TargetVersion: 1, ExpectedVersion: 2})
	if err != nil || restored.Snapshot.Version != 3 || service.RuntimeStatus().Modules[0].State != "active" {
		t.Fatal(restored, err)
	}
	history, err := service.History(ctx, 10)
	if err != nil || len(history) != 3 {
		t.Fatal(history, err)
	}
}
