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
	if snapshot.Runtime.Admission.DedupTTL != time.Minute {
		t.Fatalf("dedup ttl = %s", snapshot.Runtime.Admission.DedupTTL)
	}
	if snapshot.Runtime.LLM.Protocol != llm.OpenAIResponses {
		t.Fatalf("protocol = %q", snapshot.Runtime.LLM.Protocol)
	}
	if snapshot.Runtime.LLM.APIKeyRef != defaultAPIKeyRef {
		t.Fatalf("api key ref = %q", snapshot.Runtime.LLM.APIKeyRef)
	}
}

func TestLegacyRuntimeWithoutSearchCanBeWrittenAndReloaded(t *testing.T) {
	legacy := `{
		"bot":{"name":"ForgeBot"},
		"admission":{"dedup_ttl":"60s","dedup_max":10000},
		"llm":{"protocol":"openai_responses","api_key_ref":"llm/default","model":"","timeout":"60s"},
		"platforms":{"connectors":[{"id":"echo","type":"echo","enabled":true}],"groups":[{"id":"local","connector_id":"echo","target":"local"}]},
		"operations":{"groups":[]}
	}`
	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := Load(LoadOptions{RuntimePath: path})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	reloaded, _, err := Load(LoadOptions{RuntimePath: path})
	if err != nil {
		t.Fatalf("reload rewritten legacy runtime: %v", err)
	}
	if err := reloaded.Runtime.Validate(); err != nil {
		t.Fatalf("rewritten legacy runtime is invalid: %v", err)
	}
}

func TestLoadYAMLRuntimeJSONAndStaticEnv(t *testing.T) {
	dir := t.TempDir()
	staticPath := filepath.Join(dir, "forgebot.yaml")
	runtimePath := filepath.Join(dir, "runtime.json")
	staticBody := []byte("server:\n  addr: 127.0.0.1:9000\n  read_timeout: 2s\n  write_timeout: 3s\n  shutdown_timeout: 4s\nruntime_config_path: runtime.json\nsecret_store_path: secrets.json\n")
	runtimeBody := []byte(`{
		"bot": {"name": "TestBot"},
		"admission": {"dedup_ttl": "5s", "dedup_max": 7},
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
	if snapshot.Static.Log.Level != "info" || snapshot.Static.Log.Format != "text" || snapshot.Static.Log.File != "" {
		t.Fatalf("log defaults changed: %+v", snapshot.Static.Log)
	}
	if snapshot.Runtime.Admission.DedupTTL != 5*time.Second {
		t.Fatalf("admission dedup ttl = %s", snapshot.Runtime.Admission.DedupTTL)
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

func TestDefaultHTTPDeadlineAllowsFullAgentRun(t *testing.T) {
	if DefaultStatic().Server.WriteTimeout <= DefaultRuntime().Agent.Defaults().Timeout {
		t.Fatal("default HTTP deadline truncates default agent budget")
	}
}

func TestCheckedInExampleLoadsWithCurrentConfigSchema(t *testing.T) {
	cfg, _, err := Load(LoadOptions{Path: filepath.Join("..", "..", "configs", "forgebot.example.yaml"), LookupEnv: func(string) string { return "" }})
	if err != nil {
		t.Fatal("checked-in example no longer loads", err)
	}
	if cfg.Runtime.LLM.Model == "" || cfg.Runtime.LLM.ImageUnderstanding || cfg.Runtime.Agent.Defaults().MaxTurns != 6 {
		t.Fatal("unexpected example behavior", cfg.Runtime.LLM, cfg.Runtime.Agent)
	}
}

func TestRuntimeConfigRejectsRemovedFeatureFields(t *testing.T) {
	for _, body := range []string{
		"{\"gate\":{\"enabled\":false}}",
		"{\"search\":{\"enabled\":false}}",
		"{\"reflection\":{\"enabled\":false}}",
		"{\"platforms\":{\"groups\":[{\"id\":\"local\",\"connector_id\":\"echo\",\"target\":\"local\",\"auto_reply_unmentioned\":true}]}}",
	} {
		path := filepath.Join(t.TempDir(), "runtime.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, err := Load(LoadOptions{RuntimePath: path})
		if err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("removed field accepted: %s, err=%v", body, err)
		}
	}
}

func TestRuntimeConfigRejectsLegacyOneBotConnectorField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	body := `{
		"platforms":{"connectors":[{"id":"qq","type":"onebot","enabled":true,"onebot":{}}],"groups":[]}
	}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(LoadOptions{RuntimePath: path}); err == nil || !strings.Contains(err.Error(), "unknown field \"onebot\"") {
		t.Fatalf("legacy onebot field accepted: %v", err)
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
	cfg := PlatformConfig{
		Connectors: []ConnectorConfig{
			{ID: "qq-main", Type: "onebot", Enabled: true, Options: json.RawMessage(`{"api_url":"http://127.0.0.1:5700"}`)},
			{ID: "qq-secondary", Type: "onebot", Enabled: true, Options: json.RawMessage(`{"api_url":"http://127.0.0.1:5701"}`)},
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

func TestPlatformConfigAcceptsProtocolSpecificOptionsAndRejectsMalformedOptions(t *testing.T) {
	valid := PlatformConfig{Connectors: []ConnectorConfig{{ID: "custom", Type: "custom.protocol", Enabled: true, Options: json.RawMessage(`{"opaque":true}`)}}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := PlatformConfig{Connectors: []ConnectorConfig{{ID: "bad", Type: "invalid type", Enabled: true, Options: json.RawMessage(`[]`)}}}
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "type is invalid") || !strings.Contains(err.Error(), "options must be a JSON object") {
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
		Commands: []string{"status", "restart", "pause", "resume", "proactive", "reload_docs"},
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

func TestAdministrationConfigScopesUsersToBoundConversations(t *testing.T) {
	platforms := PlatformConfig{
		Connectors: []ConnectorConfig{{ID: "echo", Type: "echo", Enabled: true}},
		Groups:     []GroupBindingConfig{{ID: "support", ConnectorID: "echo", Target: "local"}},
	}
	valid := AdministrationConfig{Administrators: []AdministratorConfig{{ConversationID: "support", UserID: "42"}}}
	if err := valid.Validate(platforms); err != nil {
		t.Fatal(err)
	}
	invalid := AdministrationConfig{Administrators: []AdministratorConfig{
		{ConversationID: "missing", UserID: ""},
		{ConversationID: "support", UserID: "42"},
		{ConversationID: "support", UserID: "42"},
	}}
	err := invalid.Validate(platforms)
	if err == nil || !strings.Contains(err.Error(), "must reference a bound conversation") ||
		!strings.Contains(err.Error(), "user_id is required") || !strings.Contains(err.Error(), "is duplicated") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

// secret_key 与 admin_token 一样可写在 YAML，环境变量优先，且任何序列化都不输出。
func TestSecretKeyFromYAMLWithEnvPrecedenceAndRedaction(t *testing.T) {
	dir := t.TempDir()
	staticPath := filepath.Join(dir, "forgebot.yaml")
	if err := os.WriteFile(staticPath, []byte("secret_key: '  yaml-master-key  '\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := Load(LoadOptions{Path: staticPath, LookupEnv: func(string) string { return "" }})
	if err != nil || snapshot.Static.SecretKey != "yaml-master-key" {
		t.Fatalf("yaml secret_key = %q, %v", snapshot.Static.SecretKey, err)
	}
	snapshot, _, err = Load(LoadOptions{Path: staticPath, LookupEnv: func(key string) string {
		if key == envSecretKey {
			return "env-master-key"
		}
		return ""
	}})
	if err != nil || snapshot.Static.SecretKey != "env-master-key" {
		t.Fatalf("env did not override secret_key: %q, %v", snapshot.Static.SecretKey, err)
	}
	for _, value := range []any{snapshot, snapshot.Public(true), snapshot.Public(false)} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "master-key") || strings.Contains(string(data), "secret_key") {
			t.Fatalf("secret key exposed: %s", data)
		}
	}
}

// log 属于启动 YAML：可只填部分字段，FORGEBOT_LOG_LEVEL 覆盖等级，相对文件路径以 YAML 目录为基准；
// 运行时 JSON 不再接受 log。
func TestLogConfigLivesInStaticYAML(t *testing.T) {
	dir := t.TempDir()
	staticPath := filepath.Join(dir, "forgebot.yaml")
	if err := os.WriteFile(staticPath, []byte("log:\n  format: json\n  file: logs/forgebot.log\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := Load(LoadOptions{Path: staticPath, LookupEnv: func(key string) string {
		if key == envLogLevel {
			return "debug"
		}
		return ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := LogConfig{Level: "debug", Format: "json", File: filepath.Join(dir, "logs", "forgebot.log")}
	if snapshot.Static.Log != want {
		t.Fatalf("log = %+v, want %+v", snapshot.Static.Log, want)
	}
	if err := os.WriteFile(staticPath, []byte("log:\n  level: verbose\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = Load(LoadOptions{Path: staticPath, LookupEnv: func(string) string { return "" }}); err == nil {
		t.Fatal("invalid log level accepted")
	}
	var runtime RuntimeConfig
	if err := decodeStrictJSON([]byte(`{"log":{"level":"debug","format":"text"}}`), &runtime); err == nil {
		t.Fatal("runtime JSON still accepts log")
	}
}
