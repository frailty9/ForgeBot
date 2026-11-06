package logging

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
)

func TestLevelFromConfigAppliesToDerivedLoggers(t *testing.T) {
	logger, closeLog, err := New(config.LogConfig{Level: "warn", Format: "text"})
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	derived := logger.With("component", "test")
	if derived.Enabled(context.Background(), slog.LevelInfo) || !derived.Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("derived logger ignored configured level")
	}
}

// 写文件时以追加方式打开，重启不会截断已有日志。
func TestFileOutputAppendsAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "forgebot.log")
	for _, message := range []string{"first run", "second run"} {
		logger, closeLog, err := New(config.LogConfig{Level: "info", Format: "json", File: path})
		if err != nil {
			t.Fatal(err)
		}
		logger.Info(message)
		if err = closeLog(); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "first run") || !strings.Contains(text, "second run") || strings.Count(text, "\n") != 2 {
		t.Fatalf("log file was not appended: %q", text)
	}
}
