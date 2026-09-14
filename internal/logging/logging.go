// Package logging provides a dynamically configurable slog handler used by
// the runtime generation and management API.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/soleapi/forgebot/internal/config"
)

type Controller struct {
	mu      sync.RWMutex
	handler slog.Handler
}

func New(cfg config.LogConfig) (*slog.Logger, *Controller, error) {
	controller := &Controller{handler: buildHandler(cfg)}
	return slog.New(controller), controller, nil
}

func (c *Controller) Apply(cfg config.LogConfig) {
	c.mu.Lock()
	c.handler = buildHandler(cfg)
	c.mu.Unlock()
}

func (c *Controller) Enabled(ctx context.Context, level slog.Level) bool {
	c.mu.RLock()
	handler := c.handler
	c.mu.RUnlock()
	return handler.Enabled(ctx, level)
}

func (c *Controller) Handle(ctx context.Context, record slog.Record) error {
	c.mu.RLock()
	handler := c.handler
	c.mu.RUnlock()
	return handler.Handle(ctx, record)
}

func (c *Controller) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &dynamicHandler{controller: c, attrs: append([]slog.Attr(nil), attrs...)}
}

func (c *Controller) WithGroup(name string) slog.Handler {
	return &dynamicHandler{controller: c, groups: []string{name}}
}

type dynamicHandler struct {
	controller *Controller
	attrs      []slog.Attr
	groups     []string
}

func (h *dynamicHandler) current() slog.Handler {
	h.controller.mu.RLock()
	handler := h.controller.handler
	h.controller.mu.RUnlock()
	for _, group := range h.groups {
		handler = handler.WithGroup(group)
	}
	if len(h.attrs) > 0 {
		handler = handler.WithAttrs(h.attrs)
	}
	return handler
}
func (h *dynamicHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.current().Enabled(ctx, level)
}
func (h *dynamicHandler) Handle(ctx context.Context, record slog.Record) error {
	return h.current().Handle(ctx, record)
}
func (h *dynamicHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	copy := *h
	copy.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &copy
}
func (h *dynamicHandler) WithGroup(name string) slog.Handler {
	copy := *h
	copy.groups = append(append([]string(nil), h.groups...), name)
	return &copy
}

func buildHandler(cfg config.LogConfig) slog.Handler {
	level := new(slog.LevelVar)
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level.Set(slog.LevelDebug)
	case "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		level.Set(slog.LevelInfo)
	}
	options := &slog.HandlerOptions{Level: level}
	if strings.ToLower(cfg.Format) == "json" {
		return slog.NewJSONHandler(os.Stdout, options)
	}
	return slog.NewTextHandler(os.Stdout, options)
}
