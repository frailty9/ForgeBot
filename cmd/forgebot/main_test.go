package main

import (
	"context"
	"encoding/base64"
	"flag"
	"io"
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
	if err := initializeAPIKey(cfg, "llm/default", ""); err != nil {
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

	if err := initializeAPIKey(cfg, "llm/default", ""); err == nil || !strings.Contains(err.Error(), "already configured") {
		t.Fatalf("second initialization error = %v", err)
	}
}

func TestInitializeAPIKeySupportsIndependentReferences(t *testing.T) {
	key := make([]byte, 32)
	t.Setenv("FORGEBOT_SECRET_KEY", base64.StdEncoding.EncodeToString(key))
	cfg := config.DefaultSnapshot()
	cfg.Static.SecretStorePath = filepath.Join(t.TempDir(), "secrets.json")
	for _, ref := range []string{"decision/default", "mcp/custom", "llm/replacement"} {
		t.Setenv("FORGEBOT_LLM_API_KEY", "value-for-"+ref)
		if err := initializeAPIKey(cfg, ref, ""); err != nil {
			t.Fatal(err)
		}
	}
	store, err := secret.NewFileStore(cfg.Static.SecretStorePath, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"decision/default", "mcp/custom", "llm/replacement"} {
		if value, err := store.Get(context.Background(), ref); err != nil || value != "value-for-"+ref {
			t.Fatalf("reference %q was not initialized correctly: %v", ref, err)
		}
	}
	if status, err := store.Status(context.Background(), cfg.Runtime.LLM.APIKeyRef); err != nil || status.Configured {
		t.Fatal("explicit target also changed default credential", err)
	}
	t.Setenv("FORGEBOT_LLM_API_KEY", "must-not-overwrite")
	if err := initializeAPIKey(cfg, "decision/default", ""); err == nil {
		t.Fatal("existing explicit reference overwritten")
	}
	for _, ref := range []string{"", "   ", "../invalid", "/absolute"} {
		if err := initializeAPIKey(cfg, ref, ""); err == nil {
			t.Fatal("invalid reference accepted", ref)
		}
	}
	ref, err := initialKeyRef("  mcp/custom  ")
	if err != nil || ref != "mcp/custom" {
		t.Fatal("reference normalization failed", ref, err)
	}
}

func TestInlineAPIKeyTakesPriorityAndStaysEncrypted(t *testing.T) {
	key := make([]byte, 32)
	t.Setenv("FORGEBOT_SECRET_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("FORGEBOT_LLM_API_KEY", "environment-value")
	cfg := config.DefaultSnapshot()
	cfg.Static.SecretStorePath = filepath.Join(t.TempDir(), "secrets.json")
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	ref := flags.String("init-api-key", "", "")
	if err := flags.Parse([]string{"-init-api-key", "decision/default", "inline-value"}); err != nil {
		t.Fatal(err)
	}
	value, err := apiKeyArgument(true, flags.Args())
	if err != nil {
		t.Fatal(err)
	}
	if err = initializeAPIKey(cfg, *ref, value); err != nil {
		t.Fatal(err)
	}
	store, err := secret.NewFileStore(cfg.Static.SecretStorePath, key)
	if err != nil {
		t.Fatal(err)
	}
	if stored, err := store.Get(context.Background(), *ref); err != nil || stored != "inline-value" {
		t.Fatal("inline value did not override environment", err)
	}
	data, err := os.ReadFile(cfg.Static.SecretStorePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "inline-value") || strings.Contains(string(data), "environment-value") {
		t.Fatal("plaintext value persisted")
	}
	t.Setenv("FORGEBOT_LLM_API_KEY", "")
	if err = initializeAPIKey(cfg, "mcp/custom", "another-inline-value"); err != nil {
		t.Fatal("inline initialization required environment", err)
	}
}

func TestAPIKeyArgumentsRejectExtraOrBlankValuesWithoutEcho(t *testing.T) {
	for _, tc := range []struct {
		initialize bool
		args       []string
	}{
		{false, []string{"private-value"}},
		{true, []string{"private-value", "extra"}},
		{true, []string{" "}},
	} {
		if _, err := apiKeyArgument(tc.initialize, tc.args); err == nil {
			t.Fatal("invalid arguments accepted")
		} else if strings.Contains(err.Error(), "private-value") {
			t.Fatal("error exposed key")
		}
	}
	if value, err := apiKeyArgument(true, nil); err != nil || value != "" {
		t.Fatal("environment fallback unavailable", err)
	}
}
