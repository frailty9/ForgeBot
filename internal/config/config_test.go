package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/llm"
)

func TestLoadDefaults(t *testing.T) {
	fixed := time.Date(2026, 9, 10, 1, 2, 3, 0, time.FixedZone("test", 8*60*60))
	snapshot, report, err := Load(LoadOptions{Now: func() time.Time { return fixed }})
	if err != nil {
		t.Fatal(err)
	}
	if report.StaticSource != "defaults" || report.RuntimeSource != "defaults" {
		t.Fatalf("report = %+v", report)
	}
	if snapshot.Version != 1 || !snapshot.UpdatedAt.Equal(fixed.UTC()) {
		t.Fatalf("snapshot metadata = %+v", snapshot)
	}
	if snapshot.Static.Server.Addr != "127.0.0.1:8080" {
		t.Fatalf("addr = %q", snapshot.Static.Server.Addr)
	}
	if snapshot.Runtime.Gate.DedupTTL != time.Minute {
		t.Fatalf("dedup ttl = %s", snapshot.Runtime.Gate.DedupTTL)
	}
	if snapshot.Runtime.LLM.Protocol != llm.OpenAIResponses {
		t.Fatalf("protocol = %q", snapshot.Runtime.LLM.Protocol)
	}
	if snapshot.Runtime.LLM.APIKeyRef != defaultAPIKeyRef {
		t.Fatalf("api key ref = %q", snapshot.Runtime.LLM.APIKeyRef)
	}
}

func TestLoadYAMLRuntimeJSONAndStaticEnv(t *testing.T) {
	dir := t.TempDir()
	staticPath := filepath.Join(dir, "forgebot.yaml")
	runtimePath := filepath.Join(dir, "runtime.json")
	staticBody := []byte("server:\n  addr: 127.0.0.1:9000\n  read_timeout: 2s\n  write_timeout: 3s\n  shutdown_timeout: 4s\nruntime_config_path: runtime.json\nsecret_store_path: secrets.json\n")
	runtimeBody := []byte(`{
		"log": {"level": "info", "format": "json"},
		"bot": {"name": "TestBot"},
		"gate": {"enabled": true, "require_mention": false, "silence": "500ms", "max_count": 2, "max_window": "3s", "dedup_ttl": "5s", "dedup_max": 7, "max_buffered": 8, "max_text_len": 9, "max_concurrent": 2},
		"llm": {"protocol": "anthropic_messages", "base_url": "https://api.example.test", "api_key_ref": "llm/test", "model": "claude", "timeout": "9s"},
		"platforms": {
			"connectors": [{"id": "echo", "type": "echo", "enabled": true}],
			"groups": [{"id": "local", "connector_id": "echo", "target": "local"}]
		}
	}`)
	if err := os.WriteFile(staticPath, staticBody, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, runtimeBody, 0600); err != nil {
		t.Fatal(err)
	}

	snapshot, report, err := Load(LoadOptions{Path: staticPath, LookupEnv: func(key string) string {
		if key == envHTTPAddr {
			return "127.0.0.1:9100"
		}
		return ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.StaticSource != "yaml" || report.RuntimeSource != "json" || report.RuntimePath != runtimePath {
		t.Fatalf("report = %+v", report)
	}
	if snapshot.Static.Server.Addr != "127.0.0.1:9100" {
		t.Fatalf("env addr override failed: %q", snapshot.Static.Server.Addr)
	}
	if snapshot.Static.Server.WriteTimeout != 3*time.Second {
		t.Fatalf("write timeout = %s", snapshot.Static.Server.WriteTimeout)
	}
	if snapshot.Runtime.Log.Level != "info" {
		t.Fatalf("runtime log unexpectedly overridden: %q", snapshot.Runtime.Log.Level)
	}
	if snapshot.Runtime.Gate.Silence != 500*time.Millisecond {
		t.Fatalf("gate silence = %s", snapshot.Runtime.Gate.Silence)
	}
	if snapshot.Runtime.LLM.Protocol != llm.AnthropicMessages || snapshot.Runtime.LLM.Timeout != 9*time.Second {
		t.Fatalf("llm config = %+v", snapshot.Runtime.LLM)
	}
	if snapshot.Runtime.LLM.APIKeyRef != "llm/test" {
		t.Fatalf("api key ref = %q", snapshot.Runtime.LLM.APIKeyRef)
	}
}

func TestRuntimeConfigRejectsAPIKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.json")
	if err := os.WriteFile(path, []byte(`{"llm":{"api_key":"must-not-be-here"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(LoadOptions{RuntimePath: path})
	if err == nil || !strings.Contains(err.Error(), "unknown field \"api_key\"") {
		t.Fatalf("expected api_key rejection, got %v", err)
	}
}

func TestPublicSnapshotNeverIncludesSecretOrReference(t *testing.T) {
	snapshot := DefaultSnapshot()
	snapshot.Runtime.LLM.APIKeyRef = "llm/private-reference"
	data, err := json.Marshal(snapshot.Public(true))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "private-reference") || strings.Contains(text, "api_key_ref") {
		t.Fatalf("public snapshot exposed key reference: %s", text)
	}
	if !strings.Contains(text, `"api_key_configured":true`) {
		t.Fatalf("configured state missing: %s", text)
	}
}

func TestLoadRejectsUnknownYAMLField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "forgebot.yaml")
	if err := os.WriteFile(path, []byte("unknown: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(LoadOptions{Path: path}); err == nil {
		t.Fatal("unknown YAML field accepted")
	}
}

func TestLoadRejectsUnknownNestedYAMLField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "forgebot.yaml")
	if err := os.WriteFile(path, []byte("server:\n  addr: 127.0.0.1:8080\n  unknown: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(LoadOptions{Path: path}); err == nil {
		t.Fatal("unknown nested YAML field accepted")
	}
}

func TestLoadRecoversRuntimeFromBackup(t *testing.T) {
	dir := t.TempDir()
	staticPath := filepath.Join(dir, "forgebot.yaml")
	runtimePath := filepath.Join(dir, "runtime.json")
	if err := os.WriteFile(staticPath, []byte("runtime_config_path: runtime.json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	backup, err := json.Marshal(DefaultRuntime())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath+".bak", backup, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, report, err := Load(LoadOptions{Path: staticPath})
	if err != nil {
		t.Fatal(err)
	}
	if report.RuntimeSource != "json_backup" || snapshot.Runtime.Bot.Name != "ForgeBot" {
		t.Fatalf("report=%+v snapshot=%+v", report, snapshot)
	}
}

func TestEnvironmentPathsAreNotRelativeToYAML(t *testing.T) {
	dir := t.TempDir()
	staticPath := filepath.Join(dir, "forgebot.yaml")
	if err := os.WriteFile(staticPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(t.TempDir(), "runtime.json")
	data, err := json.Marshal(DefaultRuntime())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := Load(LoadOptions{Path: staticPath, LookupEnv: func(key string) string {
		switch key {
		case envRuntimeConfig:
			return runtimePath
		case envSecretStorePath:
			return "configs/secrets.json"
		default:
			return ""
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Static.RuntimeConfigPath != runtimePath {
		t.Fatalf("runtime path = %q", snapshot.Static.RuntimeConfigPath)
	}
	if snapshot.Static.SecretStorePath != "configs/secrets.json" {
		t.Fatalf("secret path = %q", snapshot.Static.SecretStorePath)
	}
}

func TestPlatformConfigSupportsMultipleConnectorInstancesAndGroups(t *testing.T) {
	onebotConfig := func(port string) *OneBotConnectorConfig {
		return &OneBotConnectorConfig{APIURL: "http://127.0.0.1:" + port, WebhookAddr: "127.0.0.1:8" + port, WebhookPath: "/onebot/" + port, RequestLimit: 1024, Timeout: time.Second}
	}
	cfg := PlatformConfig{
		Connectors: []ConnectorConfig{
			{ID: "qq-main", Type: "onebot", Enabled: true, OneBot: onebotConfig("5700")},
			{ID: "qq-secondary", Type: "onebot", Enabled: true, OneBot: onebotConfig("5701")},
		},
		Groups: []GroupBindingConfig{
			{ID: "support", ConnectorID: "qq-main", Target: "10001"},
			{ID: "engineering", ConnectorID: "qq-main", Target: "10002"},
			{ID: "project", ConnectorID: "qq-secondary", Target: "20001"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformConfigRejectsAmbiguousOrUnavailableBindings(t *testing.T) {
	cfg := PlatformConfig{
		Connectors: []ConnectorConfig{{ID: "echo", Type: "echo", Enabled: false}},
		Groups: []GroupBindingConfig{
			{ID: "support", ConnectorID: "echo", Target: "a"},
			{ID: "support", ConnectorID: "echo", Target: "b"},
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "reference an enabled connector") || !strings.Contains(err.Error(), "id is duplicated") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPlatformConfigRejectsOneBotWithoutGroups(t *testing.T) {
	cfg := PlatformConfig{Connectors: []ConnectorConfig{{
		ID: "qq-main", Type: "onebot", Enabled: true,
		OneBot: &OneBotConnectorConfig{APIURL: "http://127.0.0.1:5700", WebhookAddr: "127.0.0.1:8090", WebhookPath: "/onebot", RequestLimit: 1024, Timeout: time.Second},
	}}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "must bind at least one group") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOperationsConfigRequiresBoundConversationAndKnownCommands(t *testing.T) {
	platforms := PlatformConfig{
		Connectors: []ConnectorConfig{{ID: "echo", Type: "echo", Enabled: true}},
		Groups:     []GroupBindingConfig{{ID: "ops", ConnectorID: "echo", Target: "local"}},
	}
	valid := OperationsConfig{Groups: []OperatorGroupConfig{{
		ConversationID: "ops", Notifications: true,
		Commands: []string{"status", "restart", "pause", "resume", "gate", "reload_docs"},
	}}}
	if err := valid.Validate(platforms); err != nil {
		t.Fatal(err)
	}
	invalid := OperationsConfig{Groups: []OperatorGroupConfig{
		{ConversationID: "missing", Commands: []string{"status"}},
		{ConversationID: "ops", Commands: []string{"shell"}},
		{ConversationID: "ops", Commands: []string{"status"}},
	}}
	err := invalid.Validate(platforms)
	if err == nil || !strings.Contains(err.Error(), "must reference a bound conversation") ||
		!strings.Contains(err.Error(), "must be one of") || !strings.Contains(err.Error(), "is duplicated") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}
