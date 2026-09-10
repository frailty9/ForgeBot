package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/soleapi/forgebot/internal/bot"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/platform/memory"
	"github.com/soleapi/forgebot/internal/server"
)

type App struct {
	cfg    config.Config
	logger *slog.Logger
	engine *bot.Engine
	server *server.Server
}

func New(cfg config.Config, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	registry := platform.NewRegistry()
	if cfg.Platforms.Echo.Enabled {
		if err := registry.RegisterSender("echo", memory.NewSender(logger)); err != nil {
			return nil, err
		}
	}
	engine := bot.NewEngine(context.Background(), cfg, logger, registry)
	return &App{
		cfg:    cfg,
		logger: logger,
		engine: engine,
		server: server.New(cfg, logger, engine),
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	errs := make(chan error, 1)
	go func() {
		errs <- a.server.Start()
	}()

	select {
	case <-ctx.Done():
		return a.shutdown()
	case err := <-errs:
		return err
	}
}

func (a *App) shutdown() error {
	a.logger.Info("shutting down")
	a.engine.Shutdown(a.cfg.Server.ShutdownTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Server.ShutdownTimeout)
	defer cancel()
	if err := a.server.Shutdown(ctx); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	a.logger.Info("shutdown complete")
	return nil
}
