package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/admin"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/logging"
	"github.com/soleapi/forgebot/internal/operations"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/platform/memory"
	"github.com/soleapi/forgebot/internal/platform/onebot"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
	"github.com/soleapi/forgebot/internal/secret"
	"github.com/soleapi/forgebot/internal/server"
	"github.com/soleapi/forgebot/internal/settings"
)

type App struct {
	cfg      config.Snapshot
	logger   *slog.Logger
	runtime  *runtimecfg.Manager
	server   *server.Server
	adapters []platform.Adapter
	handler  platform.Handler
}

func New(cfg config.Snapshot, logger *slog.Logger, logController *logging.Controller) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	registry := platform.NewRegistry()
	var adapters []platform.Adapter
	for _, connector := range cfg.Runtime.Platforms.Connectors {
		if !connector.Enabled {
			continue
		}
		switch connector.Type {
		case "echo":
			if err := registry.RegisterConnector(connector.ID, memory.NewSender(logger)); err != nil {
				return nil, err
			}
		case "onebot":
			groups := make(map[string]string)
			for _, binding := range cfg.Runtime.Platforms.Groups {
				if binding.ConnectorID == connector.ID {
					groups[binding.Target] = binding.ID
				}
			}
			onebotConfig := connector.OneBot
			onebotAdapter, err := onebot.New(onebot.Config{
				ID: connector.ID, APIURL: onebotConfig.APIURL, SendToken: onebotConfig.SendToken,
				ReceiveSecret: onebotConfig.ReceiveSecret,
				WebhookAddr:   onebotConfig.WebhookAddr, WebhookPath: onebotConfig.WebhookPath,
				RequestLimit: onebotConfig.RequestLimit, Timeout: onebotConfig.Timeout, Groups: groups,
			}, logger)
			if err != nil {
				return nil, err
			}
			if err := registry.RegisterConnector(connector.ID, onebotAdapter.Sender()); err != nil {
				return nil, err
			}
			adapters = append(adapters, onebotAdapter)
		}
	}
	for _, binding := range cfg.Runtime.Platforms.Groups {
		if err := registry.BindConversation(platform.ConversationBinding{ConversationID: binding.ID, ConnectorID: binding.ConnectorID, Target: binding.Target}); err != nil {
			return nil, err
		}
	}

	secretStore, err := buildSecretStore(cfg.Static)
	if err != nil {
		return nil, err
	}
	builder := runtimecfg.Builder{Sender: registry, ApplyLog: func(logConfig config.LogConfig) {
		if logController != nil {
			logController.Apply(logConfig)
		}
	}}
	if secretStore != nil {
		builder.Secrets = secretStore
	}
	runtimeManager, err := runtimecfg.NewManager(context.Background(), cfg, builder)
	if err != nil {
		return nil, err
	}

	var settingsStore settings.Store
	if cfg.Static.RuntimeConfigPath != "" {
		settingsStore, err = settings.NewJSONFileStore(cfg.Static.RuntimeConfigPath, cfg.Static)
		if err != nil {
			return nil, err
		}
	}
	var secretManager *secret.Manager
	if secretStore != nil {
		secretManager, err = secret.NewManagedWriter(secretStore, runtimeManager.ReplaceSecret, runtimeManager.DeleteSecret)
		if err != nil {
			return nil, err
		}
	}
	adminService, err := admin.New(runtimeManager, settingsStore, secretManager)
	if err != nil {
		return nil, err
	}
	operatorPolicies := make([]operations.GroupPolicy, 0, len(cfg.Runtime.Operations.Groups))
	for _, group := range cfg.Runtime.Operations.Groups {
		commands := make([]operations.Command, 0, len(group.Commands))
		for _, command := range group.Commands {
			commands = append(commands, operations.Command(strings.ToLower(strings.TrimSpace(command))))
		}
		operatorPolicies = append(operatorPolicies, operations.GroupPolicy{
			ConversationID: group.ConversationID, Notifications: group.Notifications, Commands: commands,
		})
	}
	operatorRouter, err := operations.NewRouter(runtimeManager, nil, operatorPolicies)
	if err != nil {
		return nil, err
	}

	return &App{
		cfg: cfg, logger: logger, runtime: runtimeManager,
		adapters: adapters,
		handler:  operatorRouter,
		server:   server.New(cfg, logger, runtimeManager, adminService),
	}, nil
}

func buildSecretStore(static config.StaticConfig) (*secret.FileStore, error) {
	encodedKey := strings.TrimSpace(os.Getenv("FORGEBOT_SECRET_KEY"))
	if static.SecretStorePath == "" && encodedKey == "" {
		return nil, nil
	}
	if static.SecretStorePath == "" {
		return nil, errors.New("secret_store_path is required when FORGEBOT_SECRET_KEY is set")
	}
	if encodedKey == "" {
		return nil, errors.New("FORGEBOT_SECRET_KEY is required when secret_store_path is set")
	}
	key, err := secret.ParseKey(encodedKey)
	if err != nil {
		return nil, err
	}
	return secret.NewFileStore(static.SecretStorePath, key)
}

func (a *App) Run(ctx context.Context) error {
	errs := make(chan error, 1)
	go func() { errs <- a.server.Start() }()
	for _, adapter := range a.adapters {
		adapter := adapter
		go func() {
			if err := adapter.Start(ctx, a.handler); err != nil {
				errs <- err
			}
		}()
	}
	select {
	case <-ctx.Done():
		return a.shutdown()
	case err := <-errs:
		return err
	}
}

func (a *App) shutdown() error {
	a.logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Static.Server.ShutdownTimeout)
	defer cancel()
	for _, adapter := range a.adapters {
		if err := adapter.Stop(ctx); err != nil {
			return err
		}
	}
	if err := a.runtime.Shutdown(context.Background()); err != nil {
		return err
	}
	if err := a.server.Shutdown(ctx); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	a.logger.Info("shutdown complete")
	return nil
}
