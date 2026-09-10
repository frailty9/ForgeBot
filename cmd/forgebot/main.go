package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/soleapi/forgebot/internal/app"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/logging"
)

func main() {
	configPath := flag.String("config", "", "path to ForgeBot JSON configuration file")
	flag.Parse()

	cfg, report, err := config.Load(config.LoadOptions{
		Path:      *configPath,
		LookupEnv: os.Getenv,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	logger, err := logging.New(cfg.Log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init logger: %v\n", err)
		os.Exit(1)
	}
	slog.SetDefault(logger)
	logger.Info("configuration loaded", "source", report.Source, "path", report.Path)

	root, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("initialize app", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := root.Run(ctx); err != nil {
		logger.Error("run app", "err", err)
		os.Exit(1)
	}
}
