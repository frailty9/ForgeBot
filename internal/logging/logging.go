// Package logging builds the process logger from the startup YAML. Console
// and file output are configured separately; settings are fixed for the life
// of the process and changing them requires a restart.
package logging

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/config"
)

// New returns the process logger and a close function for its outputs.
func New(cfg config.LogConfig) (*slog.Logger, func() error, error) {
	return newWithConsole(cfg, os.Stdout)
}

// newWithConsole 允许测试替换 stdout。每路按自己的等级与格式建 handler；
// 只开一路时直接使用，避免 MultiHandler 为每条记录克隆。文件一路在进程内
// 轮转，Windows 上同样可用。
func newWithConsole(cfg config.LogConfig, console io.Writer) (*slog.Logger, func() error, error) {
	var handlers []slog.Handler
	closeOutput := func() error { return nil }
	if cfg.Console.Enabled {
		handlers = append(handlers, buildHandler(cfg.Console.Level, cfg.Console.Format, console))
	}
	if f := cfg.File; f.Enabled {
		path := strings.TrimSpace(f.Path)
		if path == "" {
			return nil, nil, errors.New("log.file.path is required when log.file is enabled")
		}
		file, err := newRotatingFile(path, f.MaxSizeMB, f.RotateAfterDays, f.MaxAgeDays, f.Compress, time.Now())
		if err != nil {
			return nil, nil, err
		}
		file.run()
		handlers = append(handlers, buildHandler(f.Level, f.Format, file))
		closeOutput = file.Close
	}
	switch len(handlers) {
	case 0:
		return nil, nil, errors.New("log.console and log.file cannot both be disabled")
	case 1:
		return slog.New(handlers[0]), closeOutput, nil
	}
	return slog.New(slog.NewMultiHandler(handlers...)), closeOutput, nil
}

func buildHandler(levelName, format string, out io.Writer) slog.Handler {
	level := slog.LevelInfo
	switch strings.ToLower(levelName) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	options := &slog.HandlerOptions{Level: level}
	if strings.ToLower(format) == "json" {
		return slog.NewJSONHandler(out, options)
	}
	return slog.NewTextHandler(out, options)
}
