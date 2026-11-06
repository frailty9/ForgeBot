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
	initAPIKey := flag.String("init-api-key", "", "initialize <key_ref> [api_key] and exit; omitted api_key uses FORGEBOT_LLM_API_KEY")
	flag.Parse()
	initialize := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "init-api-key" {
			initialize = true
		}
	})
	value, err := apiKeyArgument(initialize, flag.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	cfg, report, err := config.Load(config.LoadOptions{
		Path:        *configPath,
		RuntimePath: *runtimeConfigPath,
		LookupEnv:   os.Getenv,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if initialize {
		ref, err := initialKeyRef(*initAPIKey)
		if err == nil {
			err = initializeAPIKey(cfg, ref, value)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "initialize API key: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("API key initialized for reference %q.\n", ref)
		return
	}

	logger, closeLog, err := logging.New(cfg.Static.Log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init logger: %v\n", err)
		os.Exit(1)
	}
	defer closeLog()
	slog.SetDefault(logger)
	logger.Info("configuration loaded",
		"static_source", report.StaticSource,
		"static_path", report.StaticPath,
		"runtime_source", report.RuntimeSource,
		"runtime_path", report.RuntimePath,
	)

	// 日志写文件时控制台看不到 logger 输出，致命错误另写一行到 stderr。
	fatal := func(message string, err error) {
		logger.Error(message, "err", err)
		if cfg.Static.Log.File != "" {
			fmt.Fprintf(os.Stderr, "%s: %v (details in %s)\n", message, err, cfg.Static.Log.File)
		}
		_ = closeLog()
		os.Exit(1)
	}
	root, err := app.New(cfg, logger)
	if err != nil {
		fatal("initialize app", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := root.Run(ctx); err != nil {
		fatal("run app", err)
	}
}

// 引用由 -init-api-key 的值明确指定，只选择写入目标，不读取或修改模型引用。
func initialKeyRef(requested string) (string, error) {
	ref := strings.TrimSpace(requested)
	if ref == "" {
		return "", fmt.Errorf("credential reference must not be blank")
	}
	return ref, nil
}

// 原值可直接跟在引用后，或省略以使用环境变量。额外参数报错不回显原值。
// Go flag 在首个位置参数处停止解析，因此配置开关须放在 API Key 原值之前。
func apiKeyArgument(initialize bool, args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	if !initialize || len(args) != 1 {
		return "", fmt.Errorf("usage: -config <path> -init-api-key <key_ref> [api_key]; put all flags before api_key")
	}
	value := strings.TrimSpace(args[0])
	if value == "" {
		return "", fmt.Errorf("API key argument must not be blank")
	}
	return value, nil
}

func initializeAPIKey(cfg config.Snapshot, requested, value string) error {
	ref, err := initialKeyRef(requested)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Static.SecretStorePath) == "" {
		return fmt.Errorf("secret_store_path must be configured in YAML or FORGEBOT_SECRET_STORE")
	}
	encodedKey := cfg.Static.SecretKey
	if encodedKey == "" {
		return fmt.Errorf("secret_key must be configured in YAML or FORGEBOT_SECRET_KEY")
	}
	if value == "" {
		value = os.Getenv("FORGEBOT_LLM_API_KEY")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("provide an API key after key_ref or set FORGEBOT_LLM_API_KEY")
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
	status, err := store.Status(context.Background(), ref)
	if err != nil {
		return err
	}
	if status.Configured {
		return fmt.Errorf("secret reference %q is already configured; initialization never overwrites existing credentials", ref)
	}
	candidate, err := store.Stage(context.Background(), ref, value)
	if err != nil {
		return err
	}
	if _, err := store.Activate(context.Background(), candidate); err != nil {
		_ = store.Discard(context.Background(), candidate)
		return err
	}
	return nil
}
