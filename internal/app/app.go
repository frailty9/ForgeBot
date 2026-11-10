// Package app wires configuration, runtime generation management, connectors,
// and the HTTP server into the ForgeBot process lifecycle.
package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/soleapi/forgebot/internal/admin"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/modules/echo"
	"github.com/soleapi/forgebot/internal/modules/joinreview"
	"github.com/soleapi/forgebot/internal/modules/knowledgereflection"
	"github.com/soleapi/forgebot/internal/modules/onebot"
	"github.com/soleapi/forgebot/internal/modules/proactivereply"
	"github.com/soleapi/forgebot/internal/modules/qqbot"
	"github.com/soleapi/forgebot/internal/modules/replyexperience"
	"github.com/soleapi/forgebot/internal/modules/structuredsearch"
	"github.com/soleapi/forgebot/internal/modules/welcome"
	"github.com/soleapi/forgebot/internal/notification"
	"github.com/soleapi/forgebot/internal/operator"
	"github.com/soleapi/forgebot/internal/persistence"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
	"github.com/soleapi/forgebot/internal/secret"
	"github.com/soleapi/forgebot/internal/server"
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
func New(cfg config.Snapshot, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	var key []byte
	if encodedKey := cfg.Static.SecretKey; encodedKey != "" {
		var err error
		key, err = secret.ParseKey(encodedKey)
		if err != nil {
			return nil, err
		}
	}
	backend, err := persistence.Open(cfg.Static, key)
	if err != nil {
		return nil, err
	}
	settingsStore, secretStore := backend.Configuration(), backend.Credentials()
	if settingsStore != nil {
		// 加载器提供启动快照；构建运行时前以选中后端的当前版本为准。
		cfg, err = settingsStore.Current(context.Background())
		if err != nil {
			return nil, err
		}
	}
	moduleRegistry, err := NativeModules()
	if err != nil {
		return nil, err
	}
	builder := runtimecfg.Builder{Modules: moduleRegistry, Logger: logger, RequireLLM: true}
	if secretStore != nil {
		builder.Secrets = secretStore
	}
	runtimeManager, err := runtimecfg.NewManager(context.Background(), cfg, builder)
	if err != nil {
		return nil, err
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

// NativeModules 仅注册工厂，不构建索引/连接器/模型，供 App 和 doctor 共用校验。
func NativeModules() (*modules.Registry, error) {
	registry := modules.NewRegistry()
	for _, item := range []struct {
		factory modules.Factory
		order   int
	}{{echo.Factory{}, 50}, {onebot.Factory{}, 60}, {qqbot.Factory{}, 70}, {replyexperience.Factory{}, 100}, {proactivereply.Factory{}, 200}, {structuredsearch.Factory{}, 300}, {knowledgereflection.Factory{}, 400}, {joinreview.Factory{}, 450}, {welcome.Factory{}, 500}} {
		if err := registry.Register(item.factory, hooks.Native, item.order, hooks.Continue); err != nil {
			return nil, err
		}
	}
	return registry, nil
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
