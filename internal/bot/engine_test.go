package bot

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/platform/memory"
)

func TestEngineRepliesToAddressedMessage(t *testing.T) {
	cfg := config.Default()
	cfg.Gate.Enabled = true
	registry := platform.NewRegistry()
	sender := memory.NewSender(slog.Default())
	if err := registry.RegisterSender("echo", sender); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(context.Background(), cfg, slog.Default(), registry)
	result, err := engine.HandleInbound(context.Background(), messaging.InboundMessage{
		Platform:  "echo",
		ChatID:    "c",
		UserID:    "u",
		MessageID: "m",
		Text:      "hello",
		Group:     true,
		Mentioned: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted || result.Verdict != "pass" {
		t.Fatalf("result = %+v", result)
	}
	if len(sender.Sent()) != 1 {
		t.Fatalf("sent count = %d", len(sender.Sent()))
	}
}

func TestEngineAbsorbsUnaddressedGroupMessage(t *testing.T) {
	cfg := config.Default()
	cfg.Gate.Enabled = true
	cfg.Gate.RequireMention = false
	cfg.Gate.Silence = time.Hour
	registry := platform.NewRegistry()
	sender := memory.NewSender(slog.Default())
	if err := registry.RegisterSender("echo", sender); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(context.Background(), cfg, slog.Default(), registry)
	result, err := engine.HandleInbound(context.Background(), messaging.InboundMessage{
		Platform:  "echo",
		ChatID:    "c",
		UserID:    "u",
		MessageID: "m",
		Text:      "hello",
		Group:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted || result.Verdict != "absorbed" {
		t.Fatalf("result = %+v", result)
	}
	if len(sender.Sent()) != 0 {
		t.Fatalf("unexpected sends: %#v", sender.Sent())
	}
}
