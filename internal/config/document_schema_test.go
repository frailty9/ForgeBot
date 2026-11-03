package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFutureDocumentDoesNotFallBackToOldRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	current := RuntimeDocument{SchemaVersion: RuntimeSchemaVersion + 1, ID: RuntimeDocumentID, Version: 5, UpdatedAt: time.Now(), Runtime: DefaultRuntime()}
	data, _ := json.Marshal(current)
	var future map[string]any
	if err := json.Unmarshal(data, &future); err != nil {
		t.Fatal(err)
	}
	future["future_field"] = "must not roll back"
	data, _ = json.Marshal(future)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	legacy, _ := json.Marshal(DefaultRuntime())
	if err := os.WriteFile(path+".bak", legacy, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadRuntimeDocumentWithRecovery(path, nil); !errors.Is(err, ErrRuntimeSchemaUnsupported) {
		t.Fatalf("future schema silently rolled back: %v", err)
	}
}
