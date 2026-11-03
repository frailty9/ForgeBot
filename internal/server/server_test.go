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
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid"`) {
		t.Fatalf("api_key response = %d %s", response.Code, response.Body.String())
	}

	runtimeConfig := config.DefaultRuntime()
	runtimeConfig.Bot.Name = "UpdatedBot"
	body, err := json.Marshal(admin.UpdateRequest{ExpectedVersion: 1, Runtime: runtimeConfig, Source: "test"})
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

func TestAdminSearchReloadRequiresAuthenticationAndReportsDisabled(t *testing.T) {
	fixture := newAdminFixture(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/search/reload", nil)
	response := httptest.NewRecorder()
	fixture.server.server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
	response = fixture.request(t, http.MethodPost, "/v1/admin/search/reload", "")
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"disabled"`) {
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
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"disabled"`) {
		t.Fatalf("disabled scan = %d %s", response.Code, response.Body.String())
	}
}

type adminFixture struct {
	server     *Server
	secretPath string
}

func newAdminFixture(t *testing.T) adminFixture {
	t.Helper()
	dir := t.TempDir()
	runtimePath := filepath.Join(dir, "runtime.json")
	runtimeConfig := config.DefaultRuntime()
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
	return adminFixture{server: New(snapshot, slog.New(slog.NewTextHandler(io.Discard, nil)), runtimeManager, adminService), secretPath: secretPath}
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
