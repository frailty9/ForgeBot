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
	configPath := flag.String("config", "", "path to ForgeBot YAML startup configuration file")
	runtimeConfigPath := flag.String("runtime-config", "", "path to ForgeBot JSON runtime configuration file")
	flag.Parse()

	cfg, report, err := config.Load(config.LoadOptions{
		Path:        *configPath,
		RuntimePath: *runtimeConfigPath,
		LookupEnv:   os.Getenv,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	logger, logController, err := logging.New(cfg.Runtime.Log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init logger: %v\n", err)
		os.Exit(1)
	}
	slog.SetDefault(logger)
	logger.Info("configuration loaded",
		"static_source", report.StaticSource,
		"static_path", report.StaticPath,
		"runtime_source", report.RuntimeSource,
		"runtime_path", report.RuntimePath,
	)

	root, err := app.New(cfg, logger, logController)
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
