package secret

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileStoreStagesActivatesAndNeverWritesPlaintext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	fixed := time.Date(2026, 9, 10, 2, 3, 4, 0, time.UTC)
	store, err := newFileStore(path, testKey(), func() time.Time { return fixed })
	if err != nil {
		t.Fatal(err)
	}

	candidate, err := store.Stage(context.Background(), "llm/default", "super-secret-api-key")
	if err != nil {
		t.Fatal(err)
	}
	status, err := store.Status(context.Background(), "llm/default")
	if err != nil {
		t.Fatal(err)
	}
	if status.Configured {
		t.Fatal("candidate was exposed as active")
	}
	if _, err := store.Get(context.Background(), "llm/default"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get before activation = %v", err)
	}

	status, err = store.Activate(context.Background(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Configured || status.Version != 1 || !status.UpdatedAt.Equal(fixed) {
		t.Fatalf("status = %+v", status)
	}
	value, err := store.Get(context.Background(), "llm/default")
	if err != nil {
		t.Fatal(err)
	}
	if value != "super-secret-api-key" {
		t.Fatalf("value = %q", value)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "super-secret-api-key") {
		t.Fatalf("plaintext persisted: %s", data)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
}

func TestFileStoreDiscardPreservesActiveSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	store, err := NewFileStore(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Stage(context.Background(), "llm/default", "old")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Activate(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second, err := store.Stage(context.Background(), "llm/default", "new")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Discard(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	value, err := store.Get(context.Background(), "llm/default")
	if err != nil {
		t.Fatal(err)
	}
	if value != "old" {
		t.Fatalf("active value changed to %q", value)
	}
}

func TestFileStoreReloadAndDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	store, err := NewFileStore(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.Stage(context.Background(), "llm/vendor", "key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Activate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewFileStore(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	value, err := reloaded.Get(context.Background(), "llm/vendor")
	if err != nil || value != "key" {
		t.Fatalf("reload = %q, %v", value, err)
	}
	if err := reloaded.Delete(context.Background(), "llm/vendor"); err != nil {
		t.Fatal(err)
	}
	status, err := reloaded.Status(context.Background(), "llm/vendor")
	if err != nil {
		t.Fatal(err)
	}
	if status.Configured {
		t.Fatal("deleted secret remains configured")
	}
}

func TestFileStoreRejectsWrongEncryptionKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	store, err := NewFileStore(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.Stage(context.Background(), "llm/default", "key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Activate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	wrong := make([]byte, 32)
	for i := range wrong {
		wrong[i] = byte(i + 1)
	}
	if _, err := NewFileStore(path, wrong); err == nil {
		t.Fatal("wrong encryption key accepted")
	}
}

func testKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}
