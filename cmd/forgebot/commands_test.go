package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/soleapi/forgebot/internal/app"
	"github.com/soleapi/forgebot/internal/catalog"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/modules/joinreview"
	"github.com/soleapi/forgebot/internal/secret"
)

func TestInitDoctorAndAppPreparationFromEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	var output bytes.Buffer
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}]}`)
	}))
	defer api.Close()
	handled, err := runCommand([]string{"init", "-dir", dir, "-model", "test-model", "-base-url", api.URL}, &output)
	if !handled || err != nil {
		t.Fatal(output.String(), err)
	}
	path := filepath.Join(dir, "forgebot.local.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runCommand([]string{"init", "-dir", dir}, &output); err == nil {
		t.Fatal("init overwrote files")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("init changed existing config")
	}
	cfg, _, err := config.Load(config.LoadOptions{Path: path, LookupEnv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Static.Server.Addr = "127.0.0.1:0"
	// init 把主密钥写进 YAML，无需再设置环境变量。
	if _, err = secret.ParseKey(cfg.Static.SecretKey); err != nil {
		t.Fatal("init did not write a usable secret_key", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "master.local.key")); !os.IsNotExist(err) {
		t.Fatal("init still wrote a separate key file")
	}
	t.Setenv("FORGEBOT_LLM_API_KEY", "temporary-test-key")
	if err = doctor(cfg, false, false, &output); err == nil {
		t.Fatal("uninitialized credential marked ready")
	}
	if err = initializeAPIKey(cfg, cfg.Runtime.LLM.APIKeyRef, ""); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err = doctor(cfg, false, false, &output); err != nil {
		t.Fatal(output.String(), err)
	}
	if calls.Load() != 0 {
		t.Fatal("doctor sent remote request without probe")
	}
	approvalConfig := cfg.Clone()
	approvalConfig.Static.GroupStorePath = ""
	on := true
	reviewOptions, _ := json.Marshal(map[string]any{"groups": map[string]any{approvalConfig.Runtime.Platforms.Groups[0].ID: map[string]any{"regex": `\S`}}})
	approvalConfig.Runtime.Modules = map[string]config.ModuleConfig{joinreview.ID: {Enabled: &on, Options: reviewOptions}}
	var approvalOutput bytes.Buffer
	if err = doctor(approvalConfig, false, false, &approvalOutput); err != nil {
		t.Fatal("automatic approval incorrectly requires an operation store", approvalOutput.String(), err)
	}
	if strings.Contains(output.String(), "temporary-test-key") || strings.Contains(output.String(), cfg.Static.SecretKey) {
		t.Fatal("doctor exposed credentials")
	}
	if err = doctor(cfg, true, false, &output); err != nil || calls.Load() != 1 {
		t.Fatal("explicit model probe", err, calls.Load())
	}
	root, err := app.New(cfg, nil)
	if err != nil {
		t.Fatal("generated config cannot construct app", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = root.Run(ctx); err != nil {
		t.Fatal("generated application shutdown", err)
	}
}

func TestCapabilitiesFetchUsesTokenAndRedactedProjection(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/capabilities" || r.Header.Get("Authorization") != "Bearer local-token" {
			t.Error("wrong management request")
		}
		_ = json.NewEncoder(w).Encode(catalog.Snapshot{Version: 7, Entries: []catalog.Entry{{ID: "answer", Kind: "model", Health: "not_probed"}}})
	}))
	defer api.Close()
	var output bytes.Buffer
	if err := remoteCapabilities(api.URL, "local-token", &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "local-token") || !strings.Contains(output.String(), `"version": 7`) {
		t.Fatal(output.String())
	}
}

func TestInitRefusesExistingCredentialsEvenWithoutConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secrets.local.json"), []byte("existing encrypted data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := initializeProject(dir, "model", "", llm.OpenAIResponses, false, false, io.Discard); err == nil {
		t.Fatal("init replaced credential key")
	}
	if _, err := os.Stat(filepath.Join(dir, "forgebot.local.yaml")); !os.IsNotExist(err) {
		t.Fatal("new key created over existing credentials")
	}
}
