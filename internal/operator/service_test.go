package operator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/operations"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
	searchcontract "github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/settings"
)

type fakeRuntime struct {
	snapshot config.Snapshot
	status   runtimecfg.Status
	paused   bool
	restarts int
	commits  int
	reloads  int
}

func (r *fakeRuntime) Snapshot() (config.Snapshot, runtimecfg.Status) { return r.snapshot, r.status }
func (r *fakeRuntime) Paused() bool                                   { return r.paused }
func (r *fakeRuntime) SetPaused(value bool)                           { r.paused = value }
func (r *fakeRuntime) Restart(context.Context, string) error {
	r.restarts++
	return nil
}
func (r *fakeRuntime) ReloadSearch(context.Context, string) (searchcontract.ReloadResult, error) {
	r.reloads++
	return searchcontract.ReloadResult{Documents: 2, Sections: 5, Warnings: 1, Took: 15 * time.Millisecond}, nil
}
func (r *fakeRuntime) CommitRuntime(_ context.Context, cfg config.RuntimeConfig, persist runtimecfg.Persist) (runtimecfg.ApplyResult, error) {
	r.commits++
	r.snapshot.Runtime = cfg
	persisted, err := persist()
	if err != nil {
		return runtimecfg.ApplyResult{}, err
	}
	r.snapshot = persisted
	return runtimecfg.ApplyResult{Snapshot: persisted}, nil
}

func TestStatusPauseAndResume(t *testing.T) {
	runtime := &fakeRuntime{snapshot: config.DefaultSnapshot(), status: runtimecfg.Status{State: runtimecfg.StateReady}}
	service, err := New(runtime, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(context.Background(), operations.Request{Command: operations.CommandStatus})
	if err != nil || !strings.Contains(result.Reply, "Runtime: ready") || !strings.Contains(result.Reply, "Bot: on") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := service.Execute(context.Background(), operations.Request{Command: operations.CommandPause}); err != nil || !runtime.paused {
		t.Fatalf("pause err=%v paused=%v", err, runtime.paused)
	}
	if _, err := service.Execute(context.Background(), operations.Request{Command: operations.CommandResume}); err != nil || runtime.paused {
		t.Fatalf("resume err=%v paused=%v", err, runtime.paused)
	}
}

func TestRestartUsesDeferredFollowup(t *testing.T) {
	runtime := &fakeRuntime{snapshot: config.DefaultSnapshot()}
	service, _ := New(runtime, nil)
	result, err := service.Execute(context.Background(), operations.Request{
		Command: operations.CommandRestart, ConversationID: "ops",
	})
	if err != nil || result.Followup == nil || runtime.restarts != 0 {
		t.Fatalf("result=%+v err=%v restarts=%d", result, err, runtime.restarts)
	}
	text, err := result.Followup(context.Background())
	if err != nil || text != "Runtime 重启完成。" || runtime.restarts != 1 {
		t.Fatalf("text=%q err=%v restarts=%d", text, err, runtime.restarts)
	}
}

func TestGateRequiresStoreAndUsesDeferredCommit(t *testing.T) {
	runtime := &fakeRuntime{snapshot: config.DefaultSnapshot()}
	service, _ := New(runtime, nil)
	_, err := service.Execute(context.Background(), operations.Request{Command: operations.CommandGate, Arguments: []string{"on"}})
	if err == nil || !strings.Contains(err.Error(), "存储不可用") {
		t.Fatalf("err=%v", err)
	}

	store := &fakeStore{snapshot: runtime.snapshot}
	service, _ = New(runtime, store)
	result, err := service.Execute(context.Background(), operations.Request{
		Command: operations.CommandGate, Arguments: []string{"on"}, ConversationID: "ops",
	})
	if err != nil || result.Followup == nil || runtime.commits != 0 {
		t.Fatalf("result=%+v err=%v commits=%d", result, err, runtime.commits)
	}
	text, err := result.Followup(context.Background())
	if err != nil || text != "Gate 已切换为 on。" || runtime.commits != 1 || !runtime.snapshot.Runtime.Gate.Enabled {
		t.Fatalf("text=%q err=%v commits=%d", text, err, runtime.commits)
	}
}

func TestReloadDocsUsesDeferredFollowup(t *testing.T) {
	runtime := &fakeRuntime{snapshot: config.DefaultSnapshot()}
	service, _ := New(runtime, nil)
	result, err := service.Execute(context.Background(), operations.Request{Command: operations.CommandReloadDocs, ConversationID: "ops"})
	if err != nil || result.Followup == nil || runtime.reloads != 0 {
		t.Fatalf("result=%+v err=%v reloads=%d", result, err, runtime.reloads)
	}
	text, err := result.Followup(context.Background())
	if err != nil || runtime.reloads != 1 || !strings.Contains(text, "文档 2") || !strings.Contains(text, "分段 5") {
		t.Fatalf("text=%q err=%v reloads=%d", text, err, runtime.reloads)
	}
}

type fakeStore struct{ snapshot config.Snapshot }

func (s *fakeStore) Current(context.Context) (config.Snapshot, error) { return s.snapshot, nil }
func (s *fakeStore) RuntimeAt(context.Context, uint64) (config.RuntimeConfig, error) {
	return config.RuntimeConfig{}, errors.New("not implemented")
}
func (s *fakeStore) Update(_ context.Context, request settings.UpdateRequest) (config.Snapshot, error) {
	s.snapshot.Version++
	s.snapshot.Runtime = request.Runtime
	return s.snapshot, nil
}
func (s *fakeStore) Rollback(context.Context, uint64, uint64, string) (config.Snapshot, error) {
	return config.Snapshot{}, errors.New("not implemented")
}
func (s *fakeStore) History(context.Context, int) ([]settings.RevisionMeta, error) {
	return nil, errors.New("not implemented")
}
