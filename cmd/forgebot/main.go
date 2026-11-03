package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/soleapi/forgebot/internal/app"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/logging"
	"github.com/soleapi/forgebot/internal/persistence"
	"github.com/soleapi/forgebot/internal/secret"
)

func main() {
	if handled, err := runCommand(os.Args[1:], os.Stdout); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	configPath := flag.String("config", "", "path to ForgeBot YAML startup configuration file")
	runtimeConfigPath := flag.String("runtime-config", "", "path to ForgeBot JSON runtime configuration file")
	initAPIKey := flag.Bool("init-api-key", false, "write FORGEBOT_LLM_API_KEY to the encrypted SecretStore and exit")
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
	if *initAPIKey {
		if err := initializeAPIKey(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "initialize API key: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("API key initialized for reference %q.\n", cfg.Runtime.LLM.APIKeyRef)
		return
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

func initializeAPIKey(cfg config.Snapshot) error {
	if strings.TrimSpace(cfg.Static.SecretStorePath) == "" {
		return fmt.Errorf("secret_store_path must be configured in YAML or FORGEBOT_SECRET_STORE")
	}
	encodedKey := strings.TrimSpace(os.Getenv("FORGEBOT_SECRET_KEY"))
	if encodedKey == "" {
		return fmt.Errorf("FORGEBOT_SECRET_KEY is required")
	}
	value := strings.TrimSpace(os.Getenv("FORGEBOT_LLM_API_KEY"))
	if value == "" {
		return fmt.Errorf("FORGEBOT_LLM_API_KEY is required for -init-api-key")
	}
	key, err := secret.ParseKey(encodedKey)
	if err != nil {
		return err
	}
	backend, err := persistence.Open(cfg.Static, key)
	if err != nil {
		return err
	}
	store := backend.Credentials()
	if store == nil {
		return fmt.Errorf("credential repository is not configured")
	}
	status, err := store.Status(context.Background(), cfg.Runtime.LLM.APIKeyRef)
	if err != nil {
		return err
	}
	if status.Configured {
		return fmt.Errorf("secret reference %q is already configured; use the authenticated management API to replace it", cfg.Runtime.LLM.APIKeyRef)
	}
	candidate, err := store.Stage(context.Background(), cfg.Runtime.LLM.APIKeyRef, value)
	if err != nil {
		return err
	}
	if _, err := store.Activate(context.Background(), candidate); err != nil {
		_ = store.Discard(context.Background(), candidate)
		return err
	}
	return nil
}
