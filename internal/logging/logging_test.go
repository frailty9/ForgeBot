package logging

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
)

func fileSink(path, level string) config.LogFileConfig {
	return config.LogFileConfig{Enabled: true, Level: level, Format: "json", Path: path, MaxSizeMB: 10, RotateAfterDays: 7, MaxAgeDays: 60}
}

func TestConsoleLevelAppliesToDerivedLoggers(t *testing.T) {
	var console bytes.Buffer
	logger, closeLog, err := newWithConsole(config.LogConfig{Console: config.LogConsoleConfig{Enabled: true, Level: "warn", Format: "text"}}, &console)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	derived := logger.With("component", "test")
	if derived.Enabled(context.Background(), slog.LevelInfo) || !derived.Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("derived logger ignored configured level")
	}
	derived.Warn("shown")
	if !strings.Contains(console.String(), "component=test") {
		t.Fatalf("console output missing derived attrs: %q", console.String())
	}
}

// 两路按各自等级过滤：debug 只进控制台，info 两路都有，各自使用自己的格式。
func TestConsoleAndFileFilterByOwnLevel(t *testing.T) {
	var console bytes.Buffer
	path := filepath.Join(t.TempDir(), "logs", "forgebot.log")
	cfg := config.LogConfig{Console: config.LogConsoleConfig{Enabled: true, Level: "debug", Format: "text"}, File: fileSink(path, "info")}
	logger, closeLog, err := newWithConsole(cfg, &console)
	if err != nil {
		t.Fatal(err)
	}
	logger.With("component", "split").Debug("debug-only")
	logger.Info("both")
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(console.String(), "msg=debug-only") || !strings.Contains(console.String(), "msg=both") {
		t.Fatalf("console missing records: %q", console.String())
	}
	if strings.Contains(text, "debug-only") || !strings.Contains(text, `"msg":"both"`) {
		t.Fatalf("file did not apply its own level or format: %q", text)
	}
}

// 只开文件时控制台没有输出；文件以追加方式打开，重启不会截断已有日志。
func TestFileOnlyAppendsAcrossRestarts(t *testing.T) {
	var console bytes.Buffer
	path := filepath.Join(t.TempDir(), "logs", "forgebot.log")
	for _, message := range []string{"first run", "second run"} {
		logger, closeLog, err := newWithConsole(config.LogConfig{File: fileSink(path, "info")}, &console)
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
	if console.Len() != 0 {
		t.Fatalf("disabled console still written: %q", console.String())
	}
}

func TestRejectsNoSinkOrFileWithoutPath(t *testing.T) {
	if _, _, err := newWithConsole(config.LogConfig{}, &bytes.Buffer{}); err == nil {
		t.Fatal("logger without any sink accepted")
	}
	if _, _, err := newWithConsole(config.LogConfig{File: fileSink(" ", "info")}, &bytes.Buffer{}); err == nil {
		t.Fatal("file sink without path accepted")
	}
}
