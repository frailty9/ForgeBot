// Package app wires configuration, runtime generation management, connectors,
// and the HTTP server into the ForgeBot process lifecycle.
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
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/logging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/replyexperience"
	"github.com/soleapi/forgebot/internal/notification"
	"github.com/soleapi/forgebot/internal/operator"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/platform/memory"
	"github.com/soleapi/forgebot/internal/platform/onebot"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
	searchcontract "github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/search/structured"
	"github.com/soleapi/forgebot/internal/secret"
	"github.com/soleapi/forgebot/internal/server"
	"github.com/soleapi/forgebot/internal/settings"
)

// App is the top-level process composition and shutdown coordinator.
type App struct {
	cfg      config.Snapshot
	logger   *slog.Logger
	runtime  *runtimecfg.Manager
	server   *server.Server
	notifier *notification.Dispatcher
}

// New constructs all process-level services from the validated snapshot.
func New(cfg config.Snapshot, logger *slog.Logger, logController *logging.Controller) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	secretStore, err := buildSecretStore(cfg.Static)
	if err != nil {
		return nil, err
	}
	moduleRegistry := modules.NewRegistry()
	if err := moduleRegistry.Register(replyexperience.Factory{}, hooks.Native, 100, hooks.Continue); err != nil {
		return nil, err
	}
	builder := runtimecfg.Builder{Modules: moduleRegistry, ConnectorFactory: connectorFactory(logger), SearchFactory: searchFactory, ApplyLog: func(logConfig config.LogConfig) {
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
	operatorService, err := operator.New(runtimeManager, settingsStore)
	if err != nil {
		return nil, err
	}
	runtimeManager.SetOperatorService(operatorService)
	notifier, err := notification.NewDispatcher(runtimeManager, runtimeManager, logger, 64)
	if err != nil {
		return nil, err
	}
	runtimeManager.SetNotificationPublisher(notifier)
	return &App{
		cfg: cfg, logger: logger, runtime: runtimeManager,
		server: server.New(cfg, logger, runtimeManager, adminService), notifier: notifier,
	}, nil
}

func searchFactory(ctx context.Context, cfg config.SearchConfig) (searchcontract.Retriever, error) {
	return structured.New(ctx, structured.Options{
		Root: cfg.DocumentRoot, Extensions: cfg.Extensions, DefaultResults: cfg.DefaultResults,
		MaxResults: cfg.MaxResults, MinMatchRatio: cfg.MinMatchRatio,
		QueryTimeout: cfg.QueryTimeout, BuildTimeout: cfg.BuildTimeout,
		MaxSectionChars: cfg.MaxSectionChars, MaxFileBytes: cfg.MaxFileBytes,
		MaxTotalBytes: cfg.MaxTotalBytes, MaxFiles: cfg.MaxFiles,
	})
}

func connectorFactory(logger *slog.Logger) runtimecfg.ConnectorFactory {
	return func(cfg config.PlatformConfig) (platform.ConnectorSet, error) {
		adapters := make([]platform.Adapter, 0, len(cfg.Connectors))
		for _, connector := range cfg.Connectors {
			if !connector.Enabled {
				continue
			}
			switch connector.Type {
			case "echo":
				adapters = append(adapters, memory.NewAdapter(connector.ID, logger))
			case "onebot":
				groups := make(map[string]onebot.Group)
				for _, binding := range cfg.Groups {
					if binding.ConnectorID == connector.ID {
						groups[binding.Target] = onebot.Group{ConversationID: binding.ID, PromptSuffix: binding.PromptSuffix}
					}
				}
				onebotConfig := connector.OneBot
				adapter, err := onebot.New(onebot.Config{
					ID: connector.ID, APIURL: onebotConfig.APIURL, SendToken: onebotConfig.SendToken,
					ReceiveSecret: onebotConfig.ReceiveSecret,
					PromptSuffix:  connector.PromptSuffix,
					WebhookAddr:   onebotConfig.WebhookAddr, WebhookPath: onebotConfig.WebhookPath,
					RequestLimit: onebotConfig.RequestLimit, Timeout: onebotConfig.Timeout,
					ReplayTTL: onebotConfig.ReplayTTL, ReplayMax: onebotConfig.ReplayMax, Groups: groups,
				}, logger)
				if err != nil {
					return nil, err
				}
				adapters = append(adapters, adapter)
			}
		}
		bindings := make([]platform.ConversationBinding, 0, len(cfg.Groups))
		for _, binding := range cfg.Groups {
			bindings = append(bindings, platform.ConversationBinding{
				ConversationID: binding.ID, ConnectorID: binding.ConnectorID, Target: binding.Target,
			})
		}
		return platform.NewSet(adapters, bindings)
	}
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

// Run starts the HTTP server and runtime generation until ctx is canceled.
func (a *App) Run(ctx context.Context) error {
	notificationCtx, stopNotifications := context.WithCancel(ctx)
	defer stopNotifications()
	go a.notifier.Run(notificationCtx)
	if err := a.runtime.Start(ctx); err != nil {
		return err
	}
	a.notifier.Publish(notification.Event{Level: notification.LevelInfo, Component: "runtime", Code: "started", Message: "ForgeBot 已启动。"})
	errs := make(chan error, 1)
	go func() { errs <- a.server.Start() }()
	select {
	case <-ctx.Done():
		return a.shutdown()
	case err := <-errs:
		return errors.Join(err, a.shutdown())
	}
}

func (a *App) shutdown() error {
	a.logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Static.Server.ShutdownTimeout)
	defer cancel()
	if err := a.runtime.Shutdown(ctx); err != nil {
		return err
	}
	if err := a.server.Shutdown(ctx); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	a.logger.Info("shutdown complete")
	return nil
}
