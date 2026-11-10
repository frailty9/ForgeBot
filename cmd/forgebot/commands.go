package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/app"
	"github.com/soleapi/forgebot/internal/catalog"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/modules/qqbot"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/secret"
	toolingmcp "github.com/soleapi/forgebot/internal/tooling/mcp"
)

// runCommand 不启动 Bot；初始化和诊断不能因为模型未配置而无法运行。
func runCommand(args []string, out io.Writer) (bool, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return false, nil
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(out)
	switch command {
	case "init":
		dir := flags.String("dir", "configs", "directory for new local configuration")
		model := flags.String("model", "", "answer model name")
		protocol := flags.String("protocol", "openai_responses", "LLM protocol")
		endpoint := flags.String("base-url", "", "LLM API root")
		anonymous := flags.Bool("anonymous", false, "explicitly use an unauthenticated endpoint")
		images := flags.Bool("images", false, "declare answer model image understanding")
		if err := flags.Parse(args[1:]); err != nil {
			return true, err
		}
		p, err := llm.ParseProtocol(*protocol)
		if err != nil {
			return true, err
		}
		return true, initializeProject(*dir, *model, *endpoint, p, *anonymous, *images, out)
	case "doctor", "capabilities":
		path := flags.String("config", "", "static YAML configuration")
		runtimePath := flags.String("runtime-config", "", "runtime configuration override")
		probe := flags.Bool("probe", false, "explicitly call answer model to verify remote completion")
		probeTools := flags.Bool("probe-tools", false, "explicitly initialize MCP HTTP connections or start configured stdio processes")
		endpoint := flags.String("url", "", "running admin API root (capabilities)")
		if err := flags.Parse(args[1:]); err != nil {
			return true, err
		}
		cfg, _, err := config.Load(config.LoadOptions{Path: *path, RuntimePath: *runtimePath})
		if err != nil {
			return true, err
		}
		if command == "doctor" {
			return true, doctor(cfg, *probe, *probeTools, out)
		}
		if *probe || *probeTools {
			return true, fmt.Errorf("capabilities does not perform probes")
		}
		root := *endpoint
		if root == "" {
			root = "http://" + cfg.Static.Server.Addr
		}
		return true, remoteCapabilities(root, cfg.Static.AdminToken, out)
	default:
		return true, fmt.Errorf("unknown command %q; use init, doctor or capabilities", command)
	}
}

func initializeProject(dir, model, endpoint string, protocol llm.Protocol, anonymous, images bool, out io.Writer) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	paths := []string{"forgebot.local.yaml", "runtime.local.json"}
	for _, name := range paths {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			return fmt.Errorf("refusing to overwrite %s", name)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	key := make([]byte, 32)
	if _, err := os.Lstat(filepath.Join(root, "secrets.local.json")); err == nil {
		return fmt.Errorf("existing credentials require explicit reuse; refusing to generate a different master key")
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err = rand.Read(key); err != nil {
		return err
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return err
	}
	keyText := base64.StdEncoding.EncodeToString(key)
	cfg := config.DefaultRuntime()
	cfg.LLM.Protocol = protocol
	cfg.LLM.Model = strings.TrimSpace(model)
	cfg.LLM.BaseURL = strings.TrimSpace(endpoint)
	cfg.LLM.ImageUnderstanding = images
	cfg.LLM.Authentication = config.AuthenticationAPIKey
	if anonymous {
		cfg.LLM.Authentication = config.AuthenticationNone
	}
	// 初始化文件列出关闭的审批配置，便于按需填写；不改变运行时缺省行为。
	reviewEnabled := false
	cfg.Modules = map[string]config.ModuleConfig{
		qqbot.ID: {Enabled: &reviewEnabled, Options: json.RawMessage(`{}`)},
		"native.join_review": {
			Enabled: &reviewEnabled,
			Options: json.RawMessage(`{
  "regex": "\\S",
  "model_enabled": false,
  "rule": "申请内容须说明入群目的，并符合本群主题。",
  "combine": "and",
  "reject_reason": "请填写符合本群要求的入群申请说明。",
  "groups": {"local": {"enabled": false}},
  "external_review": {
    "url": "https://review.example.com/join",
    "method": "POST",
    "headers": {"Authorization": "Bearer {review/token}"},
    "body": "{\"application\":\"{application}\",\"user_id\":\"{user_id}\",\"conversation_id\":\"{conversation_id}\"}",
    "result_expression": ".data.approved == true"
  }
}`),
		},
	}
	cfg.Platforms.Connectors = append(cfg.Platforms.Connectors, config.ConnectorConfig{
		ID: "qqbot-main", Type: "qqbot", Enabled: false,
		Options: json.RawMessage(`{"app_id":"YOUR_APP_ID","secret_ref":"qqbot/main","webhook_addr":"127.0.0.1:18091","webhook_path":"/qqbot","timeout":"15s","request_limit":1048576,"replay_ttl":"5m","replay_max":10000}`),
	})
	runtimeData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	yaml := fmt.Sprintf("storage:\n  backend: file\nserver:\n  addr: 127.0.0.1:8080\n  read_timeout: 10s\n  write_timeout: 100s\n  shutdown_timeout: 15s\nlog:\n  console:\n    enabled: true\n    level: info\n    format: text\n  file:\n    enabled: false\n    level: info\n    format: json\n    path: logs/forgebot.log\n    max_size_mb: 10\n    rotate_after_days: 7\n    max_age_days: 60\n    compress: false\nruntime_config_path: runtime.local.json\nsecret_store_path: secrets.local.json\nadmin_token: %s\n# SecretStore 主密钥，丢失后无法解密 secrets.local.json；FORGEBOT_SECRET_KEY 可覆盖此值。\nsecret_key: %s\n", base64.RawURLEncoding.EncodeToString(token), keyText)
	if err = os.MkdirAll(root, 0700); err != nil {
		return err
	}
	// O_EXCL 防止检查与写入之间覆盖已有文件。失败仅清理本次创建的具体文件。
	created := []string{}
	success := false
	defer func() {
		if !success {
			for _, path := range created {
				_ = os.Remove(path)
			}
		}
	}()
	for index, data := range [][]byte{[]byte(yaml), runtimeData} {
		path := filepath.Join(root, paths[index])
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		created = append(created, path)
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	success = true
	// 私人初始化目录单独带忽略规则；已有 .gitignore 不擅自改写。
	if file, err := os.OpenFile(filepath.Join(root, ".gitignore"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600); err == nil {
		_, _ = file.WriteString("/forgebot.local.yaml\n/runtime.local.json*\n/secrets.local.json*\n/data/\n/logs/\n")
		_ = file.Close()
	}
	fmt.Fprintf(out, "已初始化 %s\n主密钥与管理令牌已写入 forgebot.local.yaml，请单独备份该文件且不要提交。\n", root)
	fmt.Fprintf(out, "随后设置 FORGEBOT_LLM_API_KEY，执行 -config '%s' -init-api-key %s；doctor 可在尚未配置模型时运行并列出缺项。\n", filepath.Join(root, "forgebot.local.yaml"), cfg.LLM.APIKeyRef)
	return nil
}

type check struct {
	Name   string         `json:"name"`
	Status string         `json:"status"`
	Code   operation.Code `json:"code,omitempty"`
	Detail string         `json:"detail,omitempty"`
}

func doctor(cfg config.Snapshot, probe, probeTools bool, out io.Writer) error {
	probeCtx, stopProbes := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopProbes()
	checks := []check{}
	failed := false
	add := func(name string, err error, detail string) {
		item := check{Name: name, Status: "ok", Detail: detail}
		if err != nil {
			item.Status = "failed"
			item.Code = operation.Describe(err).Code
			failed = true
		}
		checks = append(checks, item)
	}
	if cfg.Runtime.LLM.Model == "" {
		add("answer_model", operation.New(operation.NotConfigured, "model missing"), "设置 llm.model")
	} else {
		add("answer_model", nil, cfg.Runtime.LLM.Model)
	}
	add("runtime_config", nil, "配置结构有效；未启动连接器、MCP 或 Bot")
	registry, registryErr := app.NativeModules()
	if registryErr == nil {
		registryErr = registry.ValidateRuntime(cfg.Runtime.Modules, cfg.Runtime.Platforms)
	}
	add("native_module_options", registryErr, "使用正式模块工厂校验配置，未构建索引或后台任务")
	connectorCredentialRefs := map[string][]string{}
	if registryErr == nil {
		providers, err := registry.BuildConnectorProviders(context.Background(), cfg.Runtime.Modules, nil)
		if err == nil {
			defer providers.Close(context.Background())
			for _, connector := range cfg.Runtime.Platforms.Connectors {
				if !connector.Enabled {
					continue
				}
				registration, exists := providers.ConnectorProvider(connector.Type)
				if !exists {
					add("connector:"+connector.ID, operation.New(operation.NotConfigured, "connector provider not enabled"), "启用对应协议模块")
					continue
				}
				bindings := []platform.ConnectorBinding{}
				for _, binding := range cfg.Runtime.Platforms.Groups {
					if binding.ConnectorID == connector.ID {
						bindings = append(bindings, platform.ConnectorBinding{ConversationID: binding.ID, Target: binding.Target, PromptSuffix: binding.PromptSuffix})
					}
				}
				add("connector:"+connector.ID, registration.Provider.ValidateOptions(registration.ModuleOptions, connector.Options, bindings), "验证协议参数，未启动连接器")
				if provider, ok := registration.Provider.(platform.CredentialProvider); ok {
					refs, err := provider.CredentialRefs(registration.ModuleOptions, connector.Options)
					add("connector_credentials_config:"+connector.ID, err, "检查连接器凭据引用声明")
					connectorCredentialRefs[connector.ID] = refs
				}
			}
		} else {
			add("connector_providers", err, "无法构建协议提供者")
		}
	}
	add("storage_backend", cfg.Static.Validate(), cfg.Static.Storage.Kind())
	if cfg.Static.Server.WriteTimeout <= cfg.Runtime.Agent.Defaults().Timeout {
		add("http_answer_deadline", operation.New(operation.Invalid, "HTTP write timeout is shorter than the agent run budget"), "server.write_timeout应大于agent.timeout，避免测试入站在回答完成前断开")
	}
	paths := map[string]bool{}
	for _, path := range []string{cfg.Static.RuntimeConfigPath, cfg.Static.SecretStorePath} {
		if path != "" {
			absolute, err := filepath.Abs(path)
			if err == nil {
				if paths[strings.ToLower(absolute)] {
					err = operation.New(operation.Invalid, "store files overlap")
				}
				paths[strings.ToLower(absolute)] = true
			}
			add("store_file:"+filepath.Base(path), err, "仓储路径彼此独立")
		}
	}
	apiKey := ""
	var credentials interface {
		secret.Reader
		secret.StatusReader
	}
	if cfg.Static.SecretStorePath == "" && cfg.Static.SecretKey != "" {
		add("credential_store", operation.New(operation.NotConfigured, "master key set without store path"), "设置secret_store_path，或移除 secret_key 与 FORGEBOT_SECRET_KEY")
	}
	if cfg.Runtime.LLM.Authentication != config.AuthenticationNone || cfg.Static.SecretStorePath != "" {
		key, err := secret.ParseKey(cfg.Static.SecretKey)
		add("master_key", err, "读取 YAML secret_key 或 FORGEBOT_SECRET_KEY（后者优先），仅验证格式，不显示值")
		if err == nil {
			credentials, err = secret.InspectFile(cfg.Static.SecretStorePath, key)
			add("credential_store", err, "只读验证加密仓储，不执行恢复或格式升级")
			if err == nil && cfg.Runtime.LLM.Authentication != config.AuthenticationNone {
				apiKey, err = credentials.Get(context.Background(), cfg.Runtime.LLM.APIKeyRef)
				add("answer_credential", err, "先执行 -init-api-key <llm.api_key_ref> 配置回答凭据")
			}
		}
	}
	for id, refs := range connectorCredentialRefs {
		for _, ref := range refs {
			var err error
			if credentials == nil {
				err = operation.New(operation.NotConfigured, "connector credential unavailable")
			} else {
				var value string
				value, err = credentials.Get(context.Background(), ref)
				if err == nil && value == "" {
					err = operation.New(operation.NotConfigured, "connector credential empty")
				}
			}
			add("connector_credential:"+id, err, "先通过 -init-api-key 写入连接器 secret_ref 对应凭据；不请求平台 Token")
		}
	}
	// 只绑定本地监听地址检查占用，立即关闭；不连接平台也不发送群消息。
	listener, err := net.Listen("tcp", cfg.Static.Server.Addr)
	if err == nil {
		_ = listener.Close()
	}
	add("listen_address", err, "检查本地地址能否监听；已运行实例占用属预期")
	for _, server := range cfg.Runtime.Extensions.MCP {
		if server.Enabled {
			if !probeTools {
				checks = append(checks, check{Name: "mcp:" + server.ID, Status: "not_probed", Detail: "仅验证配置，未启动HTTP连接或stdio子进程"})
				continue
			}
			ctx, cancel := context.WithTimeout(probeCtx, 15*time.Second)
			key := ""
			var err error
			if server.APIKeyRef != "" {
				if credentials == nil {
					err = operation.New(operation.NotConfigured, "MCP credential unavailable")
				} else {
					key, err = credentials.Get(ctx, server.APIKeyRef)
				}
			}
			if err == nil {
				value := toolingmcp.Config{ID: server.ID, Endpoint: server.Endpoint, APIKey: key, AllowedTools: server.AllowedTools, Timeout: server.Timeout, MaxResponseBytes: server.MaxResponseBytes}
				var client *toolingmcp.Client
				if server.Transport == "stdio" {
					client, err = toolingmcp.NewWithStdio(ctx, value, toolingmcp.StdioConfig{Command: server.Command, Args: server.Args, WorkingDirectory: server.WorkingDirectory, Environment: server.Environment, Timeout: server.Timeout, MaxResponseBytes: server.MaxResponseBytes})
				} else {
					client, err = toolingmcp.New(ctx, value)
				}
				if client != nil {
					_ = client.Close()
				}
			}
			cancel()
			add("mcp:"+server.ID, err, "显式初始化并列出工具，不执行tools/call")
		}
	}
	for index, dir := range cfg.Runtime.Extensions.Skills.Directories {
		info, err := os.Stat(dir)
		if err == nil && !info.IsDir() {
			err = operation.New(operation.Invalid, "skill directory is not a directory")
		}
		add(fmt.Sprintf("skill_directory:%d", index+1), err, "仅验证目录可访问")
	}
	if probe && !failed {
		client, err := llm.NewClient(cfg.Runtime.LLM.ClientConfig(apiKey))
		if err == nil {
			ctx, cancel := context.WithTimeout(probeCtx, 15*time.Second)
			_, err = client.Complete(ctx, llm.Request{Model: cfg.Runtime.LLM.Model, Messages: []llm.Message{llm.TextMessage(llm.RoleUser, "Reply OK")}, MaxTokens: 16})
			cancel()
		}
		add("answer_model_probe", err, "显式远程完成请求，可能消耗模型额度")
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(map[string]any{"ready": !failed, "checks": checks}); err != nil {
		return err
	}
	if failed {
		return operation.New(operation.NotConfigured, "doctor found configuration or local readiness issues")
	}
	return nil
}

func remoteCapabilities(root, token string, out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(root, "/")+"/v1/admin/capabilities", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return operation.Wrap("capabilities.fetch", operation.HTTPCode(response.StatusCode), operation.NotStarted, fmt.Errorf("management API HTTP status %d", response.StatusCode))
	}
	var snapshot catalog.Snapshot
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&snapshot); err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snapshot)
}
