package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/llm"
)

func TestLoadDefaults(t *testing.T) {
	cfg, report, err := Load(LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Source != "defaults" {
		t.Fatalf("source = %q, want defaults", report.Source)
	}
	if cfg.Server.Addr != "127.0.0.1:8080" {
		t.Fatalf("addr = %q", cfg.Server.Addr)
	}
	if cfg.Gate.DedupTTL != time.Minute {
		t.Fatalf("dedup ttl = %s", cfg.Gate.DedupTTL)
	}
	if cfg.LLM.Protocol != llm.OpenAIResponses {
		t.Fatalf("default LLM protocol = %q", cfg.LLM.Protocol)
	}
}

func TestLoadFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := []byte(`{
		"server": {"addr": "127.0.0.1:9000", "read_timeout": "2s", "write_timeout": 3, "shutdown_timeout": "4s"},
		"log": {"level": "info", "format": "json"},
		"bot": {"name": "TestBot"},
		"gate": {"enabled": true, "require_mention": false, "silence": "500ms", "max_count": 2, "max_window": "3s", "dedup_ttl": "5s", "dedup_max": 7, "max_buffered": 8, "max_text_len": 9, "max_concurrent": 2},
		"llm": {"protocol": "anthropic_messages", "base_url": "https://api.example.test", "model": "claude", "timeout": "9s"},
		"platforms": {"echo": {"enabled": true}}
	}`)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, report, err := Load(LoadOptions{
		Path: path,
		LookupEnv: func(key string) string {
			if key == envHTTPAddr {
				return "127.0.0.1:9100"
			}
			if key == envLogLevel {
				return "debug"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Source != "file" || report.Path != path {
		t.Fatalf("report = %+v", report)
	}
	if cfg.Server.Addr != "127.0.0.1:9100" {
		t.Fatalf("env addr override failed: %q", cfg.Server.Addr)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("env log override failed: %q", cfg.Log.Level)
	}
	if cfg.Server.WriteTimeout != 3*time.Second {
		t.Fatalf("numeric duration = %s", cfg.Server.WriteTimeout)
	}
	if cfg.Gate.Silence != 500*time.Millisecond {
		t.Fatalf("gate silence = %s", cfg.Gate.Silence)
	}
	if cfg.LLM.Protocol != llm.AnthropicMessages || cfg.LLM.Timeout != 9*time.Second {
		t.Fatalf("llm config = %+v", cfg.LLM)
	}
	if got := cfg.LLM.ClientConfig().AnthropicVersion; got != "" {
		t.Fatalf("unexpected anthropic version = %q", got)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"unknown": true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(LoadOptions{Path: path}); err == nil {
		t.Fatal("unknown field accepted")
	}
}
