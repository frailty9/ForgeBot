package settings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/config"
)

func TestJSONFileStoreUpdateConflictHistoryAndReload(t *testing.T) {
	path := writeRuntimeFile(t)
	times := []time.Time{
		time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 10, 2, 0, 0, 0, time.UTC),
	}
	index := 0
	now := func() time.Time {
		value := times[index]
		if index < len(times)-1 {
			index++
		}
		return value
	}
	store, err := newJSONFileStore(path, config.DefaultStatic(), 2, now)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	updated := current.Runtime
	updated.Bot.Name = "UpdatedBot"
	next, err := store.Update(context.Background(), UpdateRequest{ExpectedVersion: 1, Runtime: updated, Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Version != 2 || next.Runtime.Bot.Name != "UpdatedBot" {
		t.Fatalf("next = %+v", next)
	}
	if _, err := store.Update(context.Background(), UpdateRequest{ExpectedVersion: 1, Runtime: updated, Source: "stale"}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	history, err := store.History(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Version != 2 || history[1].Version != 1 {
		t.Fatalf("history = %+v", history)
	}
	latest, err := store.History(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 1 || latest[0].Version != 2 {
		t.Fatalf("latest = %+v", latest)
	}
	reloaded, err := NewJSONFileStore(path, config.DefaultStatic())
	if err != nil {
		t.Fatal(err)
	}
	reloadedCurrent, err := reloaded.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reloadedCurrent.Version != 2 || reloadedCurrent.Runtime.Bot.Name != "UpdatedBot" {
		t.Fatalf("reloaded = %+v", reloadedCurrent)
	}
}

func TestJSONFileStoreRollbackCreatesMonotonicVersion(t *testing.T) {
	path := writeRuntimeFile(t)
	store, err := NewJSONFileStore(path, config.DefaultStatic())
	if err != nil {
		t.Fatal(err)
	}
	current, _ := store.Current(context.Background())
	updated := current.Runtime
	updated.Bot.Name = "VersionTwo"
	if _, err := store.Update(context.Background(), UpdateRequest{ExpectedVersion: 1, Runtime: updated, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := store.Rollback(context.Background(), 1, 2, "rollback:test")
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Version != 3 || rolledBack.Runtime.Bot.Name != "ForgeBot" {
		t.Fatalf("rollback = %+v", rolledBack)
	}
	if _, err := store.Rollback(context.Background(), 99, 3, "rollback:missing"); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("missing error = %v", err)
	}
}

func TestJSONFileStoreWritesReadableDurationsAndNoSecret(t *testing.T) {
	path := writeRuntimeFile(t)
	store, err := NewJSONFileStore(path, config.DefaultStatic())
	if err != nil {
		t.Fatal(err)
	}
	current, _ := store.Current(context.Background())
	if _, err := store.Update(context.Background(), UpdateRequest{ExpectedVersion: 1, Runtime: current.Runtime, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"timeout": "1m0s"`) || !strings.Contains(text, `"dedup_ttl": "1m0s"`) {
		t.Fatalf("durations are not readable: %s", text)
	}
	if strings.Contains(text, `"api_key"`) {
		t.Fatalf("secret field persisted: %s", text)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
}

func TestJSONFileStoreReloadsFromPreviousBackup(t *testing.T) {
	path := writeRuntimeFile(t)
	store, err := NewJSONFileStore(path, config.DefaultStatic())
	if err != nil {
		t.Fatal(err)
	}
	current, _ := store.Current(context.Background())
	updated := current.Runtime
	updated.Bot.Name = "UpdatedBot"
	if _, err := store.Update(context.Background(), UpdateRequest{ExpectedVersion: 1, Runtime: updated, Source: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewJSONFileStore(path, config.DefaultStatic())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := recovered.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 1 || snapshot.Runtime.Bot.Name != "ForgeBot" {
		t.Fatalf("recovered = %+v", snapshot)
	}
	backupBefore, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = recovered.Update(context.Background(), UpdateRequest{ExpectedVersion: snapshot.Version, Runtime: snapshot.Runtime, Source: "after-recovery"}); err != nil {
		t.Fatal(err)
	}
	backupAfter, err := os.ReadFile(path + ".bak")
	if err != nil || string(backupBefore) != string(backupAfter) {
		t.Fatal("valid recovery backup overwritten by corrupt primary", err)
	}
}

func writeRuntimeFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime.json")
	runtime := config.DefaultRuntime()
	data, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
