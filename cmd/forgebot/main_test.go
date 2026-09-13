package main

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/secret"
)

func TestInitializeAPIKeyCreatesEncryptedSecret(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "secrets.json")
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	t.Setenv("FORGEBOT_SECRET_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("FORGEBOT_LLM_API_KEY", "initial-provider-key")
	t.Setenv("FORGEBOT_SECRET_STORE", storePath)

	cfg := config.DefaultSnapshot()
	cfg.Static.SecretStorePath = storePath
	cfg.Runtime.LLM.APIKeyRef = "llm/default"
	if err := initializeAPIKey(cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "initial-provider-key") {
		t.Fatal("API key was persisted in plaintext")
	}
	store, err := secret.NewFileStore(storePath, key)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Get(context.Background(), "llm/default")
	if err != nil || value != "initial-provider-key" {
		t.Fatalf("stored API key = %q, err = %v", value, err)
	}

	if err := initializeAPIKey(cfg); err == nil || !strings.Contains(err.Error(), "already configured") {
		t.Fatalf("second initialization error = %v", err)
	}
}
