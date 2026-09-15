package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/admin"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/platform/memory"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
	"github.com/soleapi/forgebot/internal/secret"
	"github.com/soleapi/forgebot/internal/settings"
)

func TestAdminRequiresBearerToken(t *testing.T) {
	fixture := newAdminFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/config", nil)
	response := httptest.NewRecorder()
	fixture.server.server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("unstable unauthorized response: %s", response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/admin/config", nil)
	request.Header.Set("Authorization", "Bearer test-admin-token")
	response = httptest.NewRecorder()
	fixture.server.server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "test-admin-token") {
		t.Fatal("admin token exposed")
	}
}

func TestAPIKeyEndpointIsWriteOnly(t *testing.T) {
	fixture := newAdminFixture(t)
	response := fixture.request(t, http.MethodPut, "/v1/admin/llm/api-key", `{"api_key":"top-secret-value"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "top-secret-value") {
		t.Fatal("write response exposed API key")
	}

	response = fixture.request(t, http.MethodGet, "/v1/admin/llm/api-key", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "top-secret-value") || !strings.Contains(response.Body.String(), `"configured":true`) {
		t.Fatalf("unsafe status response: %s", response.Body.String())
	}

	data, err := os.ReadFile(fixture.secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "top-secret-value") {
		t.Fatal("secret persisted as plaintext")
	}
}

func TestOrdinaryConfigRejectsAPIKeyAndStaleVersion(t *testing.T) {
	fixture := newAdminFixture(t)
	response := fixture.request(t, http.MethodPut, "/v1/admin/config", `{"expected_version":1,"runtime":{"llm":{"api_key":"forbidden"}}}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_request"`) || !strings.Contains(response.Body.String(), "unknown field") {
		t.Fatalf("api_key response = %d %s", response.Code, response.Body.String())
	}

	get := fixture.request(t, http.MethodGet, "/v1/admin/config", "")
	var current admin.ConfigResponse
	if err := json.Unmarshal(get.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	current.Runtime.Bot.Name = "UpdatedBot"
	body, err := json.Marshal(admin.UpdateRequest{ExpectedVersion: 1, Runtime: current.Runtime, Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	response = fixture.requestBytes(t, http.MethodPut, "/v1/admin/config", body)
	if response.Code != http.StatusOK {
		t.Fatalf("update = %d %s", response.Code, response.Body.String())
	}
	response = fixture.requestBytes(t, http.MethodPut, "/v1/admin/config", body)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale update = %d %s", response.Code, response.Body.String())
	}
}

func TestAdminConfigRoundTripPreservesAndMutatesSecretsExplicitly(t *testing.T) {
	fixture := newAdminFixture(t)
	response := fixture.request(t, http.MethodGet, "/v1/admin/config", "")
	if response.Code != http.StatusOK {
		t.Fatalf("get = %d %s", response.Code, response.Body.String())
	}
	bodyText := response.Body.String()
	for _, plaintext := range []string{"send-secret", "receive-secret", "mcp-secret-ref", "private-value"} {
		if strings.Contains(bodyText, plaintext) {
			t.Fatalf("GET exposed %q: %s", plaintext, bodyText)
		}
	}
	var current admin.ConfigResponse
	if err := json.Unmarshal(response.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	if !current.Runtime.LLM.APIKeyRef.Configured || !current.Runtime.Platforms.Connectors[1].OneBot.SendToken.Configured || !current.Runtime.Extensions.MCP[0].Environment["PRIVATE"].Configured {
		t.Fatalf("configured flags missing: %+v", current.Runtime)
	}
	current.Runtime.Bot.Name = "SafeRoundTrip"
	body, err := json.Marshal(admin.UpdateRequest{ExpectedVersion: current.Version, Runtime: current.Runtime, Source: "test:roundtrip"})
	if err != nil {
		t.Fatal(err)
	}
	response = fixture.requestBytes(t, http.MethodPut, "/v1/admin/config", body)
	if response.Code != http.StatusOK {
		t.Fatalf("round trip = %d %s", response.Code, response.Body.String())
	}
	persisted, err := config.ReadRuntimeDocument(fixture.runtimePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Runtime.Platforms.Connectors[1].OneBot.SendToken != "send-secret" || persisted.Runtime.Platforms.Connectors[1].OneBot.ReceiveSecret != "receive-secret" || persisted.Runtime.Extensions.MCP[0].APIKeyRef != "mcp-secret-ref" || persisted.Runtime.Extensions.MCP[0].Environment["PRIVATE"] != "private-value" {
		t.Fatalf("round trip lost secrets: %+v", persisted.Runtime)
	}

	current.Version++
	current.Runtime.Platforms.Connectors[1].OneBot.SendToken = admin.SecretField{Configured: true, Action: admin.SecretReplace, Value: "new-send-secret"}
	current.Runtime.Platforms.Connectors[1].OneBot.ReceiveSecret = admin.SecretField{Configured: true, Action: admin.SecretDelete}
	current.Runtime.Extensions.MCP[0].Environment["PRIVATE"] = admin.SecretField{Configured: true, Action: admin.SecretDelete}
	body, err = json.Marshal(admin.UpdateRequest{ExpectedVersion: current.Version, Runtime: current.Runtime, Source: "test:secrets"})
	if err != nil {
		t.Fatal(err)
	}
	response = fixture.requestBytes(t, http.MethodPut, "/v1/admin/config", body)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "new-send-secret") {
		t.Fatalf("secret update = %d %s", response.Code, response.Body.String())
	}
	persisted, err = config.ReadRuntimeDocument(fixture.runtimePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Runtime.Platforms.Connectors[1].OneBot.SendToken; got != "new-send-secret" {
		t.Fatalf("send token = %q", got)
	}
	if persisted.Runtime.Platforms.Connectors[1].OneBot.ReceiveSecret != "" {
		t.Fatal("receive secret was not deleted")
	}
	if _, exists := persisted.Runtime.Extensions.MCP[0].Environment["PRIVATE"]; exists {
		t.Fatal("MCP environment secret was not deleted")
	}
}

func TestAdminConfigRejectsInvalidSecretActionWithField(t *testing.T) {
	fixture := newAdminFixture(t)
	get := fixture.request(t, http.MethodGet, "/v1/admin/config", "")
	var current admin.ConfigResponse
	if err := json.Unmarshal(get.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	current.Runtime.Platforms.Connectors[1].OneBot.SendToken.Action = "expose"
	body, err := json.Marshal(admin.UpdateRequest{ExpectedVersion: current.Version, Runtime: current.Runtime})
	if err != nil {
		t.Fatal(err)
	}
	response := fixture.requestBytes(t, http.MethodPut, "/v1/admin/config", body)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"validation_failed"`) || !strings.Contains(response.Body.String(), `"field":"runtime.platforms.connectors[1].onebot.send_token"`) {
		t.Fatalf("invalid action = %d %s", response.Code, response.Body.String())
	}
}

func TestAdminSearchReloadRequiresAuthenticationAndReportsDisabled(t *testing.T) {
	fixture := newAdminFixture(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/search/reload", nil)
	response := httptest.NewRecorder()
	fixture.server.server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
	response = fixture.request(t, http.MethodPost, "/v1/admin/search/reload", "")
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "search is disabled") {
		t.Fatalf("disabled reload = %d %s", response.Code, response.Body.String())
	}
}

func TestAdminReflectionScanRequiresAuthenticationAndReportsDisabled(t *testing.T) {
	fixture := newAdminFixture(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/reflection/scan", nil)
	response := httptest.NewRecorder()
	fixture.server.server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
	response = fixture.request(t, http.MethodPost, "/v1/admin/reflection/scan", "")
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "reflection is disabled") {
		t.Fatalf("disabled scan = %d %s", response.Code, response.Body.String())
	}
}

type adminFixture struct {
	server      *Server
	secretPath  string
	runtimePath string
}

func newAdminFixture(t *testing.T) adminFixture {
	t.Helper()
	dir := t.TempDir()
	runtimePath := filepath.Join(dir, "runtime.json")
	runtimeConfig := config.DefaultRuntime()
	runtimeConfig.Platforms.Connectors = append(runtimeConfig.Platforms.Connectors, config.ConnectorConfig{
		ID: "qq-disabled", Type: "onebot", OneBot: &config.OneBotConnectorConfig{
			SendToken: "send-secret", ReceiveSecret: "receive-secret",
		},
	})
	runtimeConfig.Extensions.MCP = []config.MCPServerConfig{{
		ID: "private-disabled", Transport: "stdio", Environment: map[string]string{"PRIVATE": "private-value"}, APIKeyRef: "mcp-secret-ref",
	}}
	data, err := json.Marshal(runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	static := config.DefaultStatic()
	static.AdminToken = "test-admin-token"
	static.RuntimeConfigPath = runtimePath
	static.Server.ShutdownTimeout = 10
	snapshot := config.Snapshot{Version: 1, UpdatedAt: config.DefaultSnapshot().UpdatedAt, Static: static, Runtime: runtimeConfig}
	registry := platform.NewRegistry()
	if err := registry.RegisterConnector("echo", memory.NewSender(slog.New(slog.NewTextHandler(io.Discard, nil)))); err != nil {
		t.Fatal(err)
	}
	if err := registry.BindConversation(platform.ConversationBinding{ConversationID: "local", ConnectorID: "echo", Target: "local"}); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(dir, "secrets.json")
	key := make([]byte, 32)
	secretStore, err := secret.NewFileStore(secretPath, key)
	if err != nil {
		t.Fatal(err)
	}
	runtimeManager, err := runtimecfg.NewManager(context.Background(), snapshot, runtimecfg.Builder{
		Sender: registry, Secrets: secretStore,
		ClientFactory: func(cfg llm.ClientConfig) (llm.Client, error) { return &serverFakeLLM{protocol: cfg.Protocol}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	settingsStore, err := settings.NewJSONFileStore(runtimePath, static)
	if err != nil {
		t.Fatal(err)
	}
	secretManager, err := secret.NewManagedWriter(secretStore, runtimeManager.ReplaceSecret, runtimeManager.DeleteSecret)
	if err != nil {
		t.Fatal(err)
	}
	adminService, err := admin.New(runtimeManager, settingsStore, secretManager)
	if err != nil {
		t.Fatal(err)
	}
	return adminFixture{server: New(snapshot, slog.New(slog.NewTextHandler(io.Discard, nil)), runtimeManager, adminService), secretPath: secretPath, runtimePath: runtimePath}
}

func (f adminFixture) request(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.requestBytes(t, method, path, []byte(body))
}

func (f adminFixture) requestBytes(t *testing.T, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer test-admin-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	f.server.server.Handler.ServeHTTP(response, request)
	return response
}

type serverFakeLLM struct{ protocol llm.Protocol }

func (f *serverFakeLLM) Protocol() llm.Protocol { return f.protocol }
func (f *serverFakeLLM) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{Text: "ok"}, nil
}
func (f *serverFakeLLM) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}
