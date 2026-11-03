package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/settings"
)

func TestFileBackendOwnsConfigurationAndCredentials(t *testing.T) {
	static := config.DefaultStatic()
	static.RuntimeConfigPath = filepath.Join(t.TempDir(), "runtime.json")
	static.SecretStorePath = filepath.Join(filepath.Dir(static.RuntimeConfigPath), "secrets.json")
	initial, _ := json.Marshal(config.DefaultRuntime())
	if err := os.WriteFile(static.RuntimeConfigPath, initial, 0600); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	backend, err := Open(static, key)
	if err != nil {
		t.Fatal(err)
	}
	if backend.Kind() != config.FileBackend {
		t.Fatal(backend.Kind())
	}
	ctx := context.Background()
	current, err := backend.Configuration().Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next := current.Runtime.Clone()
	next.Bot.Name = "Changed"
	updated, err := backend.Configuration().Update(ctx, settings.UpdateRequest{ExpectedVersion: current.Version, Runtime: next, Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Configuration().Update(ctx, settings.UpdateRequest{ExpectedVersion: current.Version, Runtime: next, Source: "stale"}); !errors.Is(err, settings.ErrVersionConflict) {
		t.Fatalf("stale=%v", err)
	}
	candidate, err := backend.Credentials().Stage(ctx, "llm/test", "private-value")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Credentials().Activate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(static, key)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Configuration().Current(ctx)
	if err != nil || loaded.Version != updated.Version || loaded.Runtime.Bot.Name != "Changed" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	value, err := reopened.Credentials().Get(ctx, "llm/test")
	if err != nil || value != "private-value" {
		t.Fatal("credential roundtrip failed", err)
	}
	backup, err := os.ReadFile(static.RuntimeConfigPath + ".bak")
	if err != nil || string(backup) != string(initial) {
		t.Fatal("legacy backup not preserved", err)
	}
	doc, err := config.ReadRuntimeDocument(static.RuntimeConfigPath, nil)
	if err != nil || doc.SchemaVersion != config.RuntimeSchemaVersion || doc.ID != config.RuntimeDocumentID {
		t.Fatalf("identity=%+v err=%v", doc, err)
	}
}

func TestBackendNeverFallsBackOrAliasesFiles(t *testing.T) {
	static := config.DefaultStatic()
	static.Storage.Backend = "sql"
	if _, err := Open(static, nil); operation.Describe(err).Code != operation.Unsupported {
		t.Fatal(err)
	}
	static.Storage.Backend = config.FileBackend
	static.RuntimeConfigPath = filepath.Join(t.TempDir(), "state.json")
	static.SecretStorePath = static.RuntimeConfigPath
	if _, err := Open(static, make([]byte, 32)); operation.Describe(err).Code != operation.Invalid {
		t.Fatal(err)
	}
	static.SecretStorePath = ""
	if _, err := Open(static, nil); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing configuration unexpectedly accepted: %v", err)
	}
}
