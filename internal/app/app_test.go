package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/structuredsearch"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/platform/memory"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
)

func TestNewWithoutSecretStore(t *testing.T) {
	snapshot := config.DefaultSnapshot()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root, err := New(snapshot, logger, nil)
	if err != nil {
		t.Fatal(err)
	}
	if root.runtime == nil || root.server == nil {
		t.Fatal("app dependencies were not initialized")
	}
}

func TestAppComposesStructuredSearchForExplicitQuery(t *testing.T) {
	documentRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(documentRoot, "auth.mdx"), []byte("# Authentication\n\nUse the ForgeBot API Key header.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot := config.DefaultSnapshot()
	snapshot.Runtime.LLM.Model = ""
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
	runtimeManager, err := runtimecfg.NewManager(context.Background(), snapshot, runtimecfg.Builder{Sender: registry, Modules: moduleRegistry})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtimeManager.HandleInbound(context.Background(), messaging.InboundMessage{
		ConversationID: "local", UserID: "u", MessageID: "m", Text: "/search API Key", Command: true,
	})
	if err != nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	sent := sender.Sent()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "auth.mdx") || !strings.Contains(sent[0].Text, "ForgeBot API Key") {
		t.Fatalf("sent = %+v", sent)
	}
}
