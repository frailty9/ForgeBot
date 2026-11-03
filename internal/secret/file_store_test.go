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

func TestFileStoreRecoversInterruptedReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	store, err := NewFileStore(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	candidate, err := store.Stage(ctx, "llm/default", "active-value")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Activate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	// 模拟旧文件已改名、候选文件尚未就位时进程终止。
	if err = os.Rename(path, path+".replace-backup"); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewFileStore(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	value, err := recovered.Get(ctx, "llm/default")
	if err != nil || value != "active-value" {
		t.Fatal("interrupted replacement lost active credentials", err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("primary file was not restored", err)
	}
}

func TestFileStoreDoesNotTreatCorruptBackupAsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(path+".replace-backup", []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(path, testKey()); err == nil {
		t.Fatal("corrupt credentials silently replaced with empty store")
	}
}

func TestExpiredCandidatesCannotActivateAfterDiscardOrDelete(t *testing.T) {
	for _, discard := range []bool{true, false} {
		store, err := NewFileStore(filepath.Join(t.TempDir(), "secrets.json"), testKey())
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		old, err := store.Stage(ctx, "llm/default", "old")
		if err != nil {
			t.Fatal(err)
		}
		if discard {
			err = store.Discard(ctx, old)
		} else {
			err = store.Delete(ctx, old.Ref)
		}
		if err != nil {
			t.Fatal(err)
		}
		current, err := store.Stage(ctx, old.Ref, "new")
		if err != nil {
			t.Fatal(err)
		}
		if current.Version <= old.Version {
			t.Fatal("candidate identity reused")
		}
		if _, err = store.Activate(ctx, old); !errors.Is(err, ErrCandidateNotFound) {
			t.Fatalf("stale candidate accepted: %v", err)
		}
		if _, err = store.Activate(ctx, current); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLegacyCredentialUpgradeKeepsOriginalEncryptedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	store, err := NewFileStore(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	candidate, err := store.Stage(ctx, "llm/default", "original")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Activate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	store.state.FormatVersion = 1
	legacy, err := json.Marshal(store.state)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewFileStore(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = loaded.Stage(ctx, "llm/default", "replacement"); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".format-v1.bak")
	if err != nil || string(backup) != string(legacy) {
		t.Fatal("legacy encrypted file was not preserved", err)
	}
}

func TestInspectFileDoesNotRestoreInterruptedReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	store, err := NewFileStore(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	candidate, err := store.Stage(ctx, "llm/default", "key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Activate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path, path+".replace-backup"); err != nil {
		t.Fatal(err)
	}
	reader, err := InspectFile(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	if value, err := reader.Get(ctx, "llm/default"); err != nil || value != "key" {
		t.Fatal("read-only recovery inspection failed", err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("inspection restored file")
	}
}
