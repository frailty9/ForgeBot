// Package logging builds the process logger from the startup YAML. The level,
// format and output are fixed for the life of the process; changing them
// requires a restart.
package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/soleapi/forgebot/internal/config"
)

// New returns the process logger and a close function for its output. With
// cfg.File empty the logger writes to stdout and close is a no-op. Otherwise
// the file is opened for append so restarts never truncate earlier logs;
// rotation is left to external tools.
func New(cfg config.LogConfig) (*slog.Logger, func() error, error) {
	var out io.Writer = os.Stdout
	closeOutput := func() error { return nil }
	if path := strings.TrimSpace(cfg.File); path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, nil, err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			return nil, nil, err
		}
		out, closeOutput = file, file.Close
	}
	return slog.New(buildHandler(cfg, out)), closeOutput, nil
}

func buildHandler(cfg config.LogConfig, out io.Writer) slog.Handler {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	options := &slog.HandlerOptions{Level: level}
	if strings.ToLower(cfg.Format) == "json" {
		return slog.NewJSONHandler(out, options)
	}
	return slog.NewTextHandler(out, options)
}
