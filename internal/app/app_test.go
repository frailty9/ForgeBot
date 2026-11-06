package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/structuredsearch"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/platform/memory"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
	"github.com/soleapi/forgebot/internal/secret"
)

func TestNewWithoutSecretStore(t *testing.T) {
	snapshot := config.DefaultSnapshot()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, err := New(snapshot, logger)
	if err == nil || operation.Describe(err).Code != operation.NotConfigured {
		t.Fatalf("missing LLM accepted: %v", err)
	}
}

// 使用本地受控模型与临时凭据验证正式应用入口，不再依靠无模型回显。
func configuredAppSnapshot(t *testing.T) config.Snapshot {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"test answer"}]}]}`)
	}))
	t.Cleanup(api.Close)
	snapshot := config.DefaultSnapshot()
	snapshot.Runtime.LLM.Model = "test-model"
	snapshot.Runtime.LLM.BaseURL = api.URL
	snapshot.Static.SecretStorePath = filepath.Join(t.TempDir(), "secrets.json")
	key := make([]byte, 32)
	snapshot.Static.SecretKey = base64.StdEncoding.EncodeToString(key)
	store, err := secret.NewFileStore(snapshot.Static.SecretStorePath, key)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.Stage(context.Background(), snapshot.Runtime.LLM.APIKeyRef, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Activate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestAppComposesStructuredSearchAsModelTool(t *testing.T) {
	documentRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(documentRoot, "auth.mdx"), []byte("# Authentication\n\nUse the ForgeBot API Key header.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot := config.DefaultSnapshot()
	snapshot.Runtime.LLM.Model = "test-model"
	snapshot.Runtime.LLM.Authentication = "none"
	enabled := true
	options, _ := json.Marshal(map[string]any{"document_root": documentRoot})
	snapshot.Runtime.Modules = map[string]config.ModuleConfig{structuredsearch.ID: {Enabled: &enabled, Options: options}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := platform.NewRegistry()
	sender := memory.NewSender(logger)
	if err := registry.RegisterConnector("echo", sender); err != nil {
		t.Fatal(err)
	}
	if err := registry.BindConversation(platform.ConversationBinding{ConversationID: "local", ConnectorID: "echo", Target: "local"}); err != nil {
		t.Fatal(err)
	}
	moduleRegistry := modules.NewRegistry()
	if err := moduleRegistry.Register(structuredsearch.Factory{}, hooks.Native, 300, hooks.Continue); err != nil {
		t.Fatal(err)
	}
	client := &searchToolClient{}
	runtimeManager, err := runtimecfg.NewManager(context.Background(), snapshot, runtimecfg.Builder{Sender: registry, Modules: moduleRegistry, RequireLLM: true, ClientFactory: func(llm.ClientConfig) (llm.Client, error) { return client, nil }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtimeManager.HandleInbound(context.Background(), messaging.InboundMessage{
		ConversationID: "local", UserID: "u", MessageID: "m", Text: "API Key怎么用", Mentioned: true,
	})
	if err != nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	sent := sender.Sent()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "auth.mdx") || client.calls != 2 {
		t.Fatalf("sent = %+v", sent)
	}
	if err := runtimeManager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type searchToolClient struct{ calls int }

func (*searchToolClient) Protocol() llm.Protocol { return llm.OpenAIResponses }
func (c *searchToolClient) Complete(_ context.Context, r llm.Request) (llm.Response, error) {
	c.calls++
	if c.calls == 1 {
		return llm.Response{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "search-1", Name: "search_docs", Arguments: `{"query":"API Key"}`}}}}, nil
	}
	return llm.Response{Text: r.Messages[len(r.Messages)-1].Text()}, nil
}
func (*searchToolClient) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}
