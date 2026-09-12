package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/notification"
	searchcontract "github.com/soleapi/forgebot/internal/search"
)

type runtimeSearch struct {
	id      string
	reloads int
	status  searchcontract.Status
	err     error
}

type blockingRuntimeSearch struct {
	entered chan struct{}
	release chan struct{}
}

func (*blockingRuntimeSearch) Search(context.Context, searchcontract.Query) (searchcontract.Response, error) {
	return searchcontract.Response{}, nil
}
func (s *blockingRuntimeSearch) Reload(ctx context.Context) (searchcontract.ReloadResult, error) {
	close(s.entered)
	select {
	case <-s.release:
		return searchcontract.ReloadResult{}, nil
	case <-ctx.Done():
		return searchcontract.ReloadResult{}, ctx.Err()
	}
}
func (*blockingRuntimeSearch) Status() searchcontract.Status {
	return searchcontract.Status{Enabled: true, State: searchcontract.StateReady}
}

func (s *runtimeSearch) Search(context.Context, searchcontract.Query) (searchcontract.Response, error) {
	return searchcontract.Response{}, nil
}
func (s *runtimeSearch) Reload(context.Context) (searchcontract.ReloadResult, error) {
	s.reloads++
	if s.err != nil {
		return searchcontract.ReloadResult{}, s.err
	}
	return searchcontract.ReloadResult{Documents: 2, Sections: 5, Took: time.Millisecond, CompletedAt: time.Now().UTC()}, nil
}
func (s *runtimeSearch) Status() searchcontract.Status { return s.status }

func TestManagerBuildsSearchOnlyWhenConfigurationChanges(t *testing.T) {
	snapshot := searchSnapshot(t, "first")
	var built []*runtimeSearch
	factory := func(_ context.Context, cfg config.SearchConfig) (searchcontract.Retriever, error) {
		search := &runtimeSearch{id: cfg.DocumentRoot, status: searchcontract.Status{Enabled: true, State: searchcontract.StateReady, Documents: 1, Sections: 2}}
		built = append(built, search)
		return search, nil
	}
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: registeredSender(t).registry, SearchFactory: factory})
	if err != nil {
		t.Fatal(err)
	}
	if len(built) != 1 {
		t.Fatalf("initial builds = %d", len(built))
	}
	nextRuntime := snapshot.Runtime
	nextRuntime.Bot.Name = "Updated"
	if _, err := manager.CommitRuntime(context.Background(), nextRuntime, persistedSnapshot(snapshot, 2, nextRuntime)); err != nil {
		t.Fatal(err)
	}
	if len(built) != 1 {
		t.Fatalf("non-search update rebuilt index: %d", len(built))
	}
	if err := manager.Restart(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	if len(built) != 1 {
		t.Fatalf("restart rebuilt index: %d", len(built))
	}
	current, _ := manager.Snapshot()
	nextRuntime = current.Runtime
	nextRuntime.Search.DocumentRoot = filepath.Join(t.TempDir(), "second")
	result, err := manager.CommitRuntime(context.Background(), nextRuntime, persistedSnapshot(current, 3, nextRuntime))
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionReplaceSearch || len(built) != 2 {
		t.Fatalf("result=%+v builds=%d", result, len(built))
	}
}

func TestManagerSearchCandidateFailureDoesNotPersistOrPublish(t *testing.T) {
	snapshot := searchSnapshot(t, "working")
	working := &runtimeSearch{status: searchcontract.Status{Enabled: true, State: searchcontract.StateReady}}
	factory := func(_ context.Context, cfg config.SearchConfig) (searchcontract.Retriever, error) {
		if filepath.Base(cfg.DocumentRoot) == "broken" {
			return nil, errors.New("index failed")
		}
		return working, nil
	}
	manager, err := NewManager(context.Background(), snapshot, Builder{Sender: registeredSender(t).registry, SearchFactory: factory})
	if err != nil {
		t.Fatal(err)
	}
	next := snapshot.Runtime
	next.Search.DocumentRoot = filepath.Join(t.TempDir(), "broken")
	persisted := false
	_, err = manager.CommitRuntime(context.Background(), next, func() (config.Snapshot, error) {
		persisted = true
		return config.Snapshot{}, nil
	})
	if err == nil || persisted {
		t.Fatalf("err=%v persisted=%v", err, persisted)
	}
	current, _ := manager.Snapshot()
	if current.Version != snapshot.Version {
		t.Fatalf("current version = %d", current.Version)
	}
	if _, err := manager.ReloadSearch(context.Background(), ""); err != nil || working.reloads != 1 {
		t.Fatalf("old search reload err=%v reloads=%d", err, working.reloads)
	}
}

func TestManagerReloadSearchPublishesSourceAwareEvent(t *testing.T) {
	snapshot := searchSnapshot(t, "working")
	search := &runtimeSearch{status: searchcontract.Status{Enabled: true, State: searchcontract.StateReady}}
	publisher := &recordingPublisher{events: make(chan notification.Event, 2)}
	manager, err := NewManager(context.Background(), snapshot, Builder{
		Sender: registeredSender(t).registry, Notifications: publisher,
		SearchFactory: func(context.Context, config.SearchConfig) (searchcontract.Retriever, error) { return search, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.ReloadSearch(context.Background(), "ops")
	if err != nil || result.Documents != 2 || search.reloads != 1 {
		t.Fatalf("result=%+v err=%v reloads=%d", result, err, search.reloads)
	}
	event := <-publisher.events
	if event.Component != "search" || event.Code != "reloaded" || event.ExcludeConversationID != "ops" {
		t.Fatalf("event = %+v", event)
	}
	_, status := manager.Snapshot()
	if status.Search.State != searchcontract.StateReady {
		t.Fatalf("status = %+v", status)
	}
}

func TestManagerRejectsConcurrentSearchReload(t *testing.T) {
	snapshot := searchSnapshot(t, "working")
	search := &blockingRuntimeSearch{entered: make(chan struct{}), release: make(chan struct{})}
	manager, err := NewManager(context.Background(), snapshot, Builder{
		Sender:        registeredSender(t).registry,
		SearchFactory: func(context.Context, config.SearchConfig) (searchcontract.Retriever, error) { return search, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, reloadErr := manager.ReloadSearch(context.Background(), "")
		done <- reloadErr
	}()
	<-search.entered
	if _, err := manager.ReloadSearch(context.Background(), ""); !errors.Is(err, searchcontract.ErrReloadInProgress) {
		t.Fatalf("concurrent reload error = %v", err)
	}
	close(search.release)
	if err := <-done; err != nil {
		t.Fatalf("first reload error = %v", err)
	}
}

func searchSnapshot(t *testing.T, suffix string) config.Snapshot {
	t.Helper()
	snapshot := testSnapshot()
	snapshot.Runtime.Search.Enabled = true
	snapshot.Runtime.Search.DocumentRoot = filepath.Join(t.TempDir(), suffix)
	return snapshot
}

func persistedSnapshot(current config.Snapshot, version uint64, runtime config.RuntimeConfig) Persist {
	return func() (config.Snapshot, error) {
		next := current
		next.Version = version
		next.Runtime = runtime
		return next, nil
	}
}
