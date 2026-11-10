// Package runtime builds and swaps complete application generations. A new
// generation is validated and started before it becomes active, so failed
// hot updates leave the previous generation serving traffic.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/soleapi/forgebot/internal/agent"
	"github.com/soleapi/forgebot/internal/bot"
	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/conversation"
	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/history"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/notification"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/operations"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/proactive"
	reflectioncontract "github.com/soleapi/forgebot/internal/reflection"
	searchcontract "github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/secret"
	"github.com/soleapi/forgebot/internal/skills"
	"github.com/soleapi/forgebot/internal/tooling"
	toolingmcp "github.com/soleapi/forgebot/internal/tooling/mcp"
)

var (
	ErrStaticConfigChanged   = operation.New(operation.Conflict, "static configuration requires a process restart")
	ErrReflectionDisabled    = operation.New(operation.Disabled, "reflection is disabled")
	ErrReflectionUnavailable = operation.New(operation.Unavailable, "reflection worker is unavailable")
	ErrRuntimeBusy           = operation.New(operation.Busy, "runtime_busy: previous generation is still retiring")
)

var generationSequence atomic.Uint64

type State string

const (
	StateReady      State = "ready"
	StateApplying   State = "applying"
	StateRestarting State = "restarting"
	StateDegraded   State = "degraded"
	StateStopped    State = "stopped"
)

type Action string

const (
	ActionNone              Action = "none"
	ActionReplaceLLM        Action = "replace_llm"
	ActionReplaceSecret     Action = "replace_secret"
	ActionReplaceModules    Action = "replace_modules"
	ActionRestartEngine     Action = "restart_engine"
	ActionRestartConnectors Action = "restart_connectors"
	ActionRestartBot        Action = "restart_bot"
)

type Status struct {
	Modules    []modules.Status           `json:"modules"`
	State      State                      `json:"state"`
	Action     Action                     `json:"action"`
	Version    uint64                     `json:"version"`
	UpdatedAt  time.Time                  `json:"updated_at"`
	Error      string                     `json:"error,omitempty"`
	Paused     bool                       `json:"paused"`
	Connectors []platform.ConnectorStatus `json:"connectors,omitempty"`
}

type ReflectionScanResult = reflectioncontract.ScanResult

type ApplyResult struct {
	Action   Action          `json:"action"`
	Status   Status          `json:"status"`
	Snapshot config.Snapshot `json:"snapshot"`
}

type ClientFactory func(llm.ClientConfig) (llm.Client, error)
type Persist func() (config.Snapshot, error)

// Builder contains factories for all replaceable runtime components.
type Builder struct {
	// recalls 是宿主准备的受限操作能力，不参与模块提供的连接器构建。
	recalls   group.RecallProvider
	Yield     *proactive.YieldState
	Decisions decision.Decider
	Memory    *conversation.Memory
	// History 是核心近期消息容器，跨代保留；为空时由 NewManager 或 Build 创建。
	History       *history.Window
	Modules       *modules.Registry
	Sender        platform.Sender
	Secrets       secret.Reader
	ClientFactory ClientFactory
	Logger        *slog.Logger
	Notifications notification.Publisher
	// 正式应用必须配置回答模型；诊断与边界测试可显式构建无模型实例，
	// 但 Engine 不会将该实例伪装为成功问答或本地回显。
	RequireLLM bool
}

// Build constructs a complete bot engine and its configured dependencies.
func (b Builder) Build(ctx context.Context, snapshot config.Snapshot) (*bot.Engine, error) {
	snapshot = snapshot.Clone()
	if err := b.Modules.ValidateRuntime(snapshot.Runtime.Modules, snapshot.Runtime.Platforms); err != nil {
		return nil, err
	}
	client, err := b.buildClient(ctx, snapshot, "", "")
	if err != nil {
		return nil, err
	}
	b, err = b.withDecision(ctx, snapshot, client, "", "")
	if err != nil {
		return nil, err
	}
	if b.History == nil {
		b.History = newHistory(snapshot, b)
	}
	initial := b.initialCapabilities(snapshot, client)
	moduleSet, err := b.Modules.BuildWithCapabilities(ctx, snapshot.Runtime.Modules, nil, initial)
	if err != nil {
		return nil, err
	}
	b.History.SetSubscribers(moduleSet.HistorySubscribers())
	engine, err := b.build(ctx, snapshot, withHistory(b.Sender, b.History), client, moduleSet)
	if err == nil {
		moduleSet.Start(ctx)
	}
	return engine, err
}

func (b Builder) build(ctx context.Context, snapshot config.Snapshot, sender platform.Sender, client llm.Client, moduleSet *modules.Set) (built *bot.Engine, buildErr error) {
	var executors []tooling.Executor
	defer func() {
		if built == nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = moduleSet.Close(closeCtx)
			for _, executor := range executors {
				if closer, ok := executor.(interface{ Close() error }); ok {
					_ = closer.Close()
				}
			}
		}
	}()
	if sender == nil {
		return nil, errors.New("runtime sender is required")
	}
	if len(moduleSet.Tools()) > 0 {
		executors = append(executors, moduleSet.ToolExecutor())
	}
	var extensionInstructions string
	if snapshot.Runtime.Extensions.Skills.MaxFiles > 0 {
		loader, loadErr := skills.New(skills.Config{Directories: snapshot.Runtime.Extensions.Skills.Directories, MaxFileBytes: snapshot.Runtime.Extensions.Skills.MaxFileBytes, MaxTotalBytes: snapshot.Runtime.Extensions.Skills.MaxTotalBytes, MaxFiles: snapshot.Runtime.Extensions.Skills.MaxFiles})
		if loadErr != nil {
			return nil, fmt.Errorf("create skill loader: %w", loadErr)
		}
		extensionInstructions, loadErr = loader.Load(ctx)
		if loadErr != nil {
			return nil, fmt.Errorf("load skills: %w", loadErr)
		}
	}
	for _, mcpConfig := range snapshot.Runtime.Extensions.MCP {
		if !mcpConfig.Enabled {
			continue
		}
		apiKey := ""
		if ref := strings.TrimSpace(mcpConfig.APIKeyRef); ref != "" && b.Secrets != nil {
			value, secretErr := b.Secrets.Get(ctx, ref)
			if secretErr != nil && !errors.Is(secretErr, secret.ErrNotFound) {
				return nil, fmt.Errorf("resolve MCP API key %q: %w", mcpConfig.ID, secretErr)
			}
			apiKey = value
		}
		mcpConfigValue := toolingmcp.Config{ID: mcpConfig.ID, Endpoint: mcpConfig.Endpoint, APIKey: apiKey, AllowedTools: mcpConfig.AllowedTools, Timeout: mcpConfig.Timeout, MaxResponseBytes: mcpConfig.MaxResponseBytes}
		var mcpClient *toolingmcp.Client
		var clientErr error
		switch mcpConfig.Transport {
		case "streamable_http":
			mcpClient, clientErr = toolingmcp.New(ctx, mcpConfigValue)
		case "stdio":
			mcpClient, clientErr = toolingmcp.NewWithStdio(ctx, mcpConfigValue, toolingmcp.StdioConfig{Command: mcpConfig.Command, Args: mcpConfig.Args, WorkingDirectory: mcpConfig.WorkingDirectory, Environment: mcpConfig.Environment, Timeout: mcpConfig.Timeout, MaxResponseBytes: mcpConfig.MaxResponseBytes})
		default:
			clientErr = fmt.Errorf("unsupported MCP transport %q", mcpConfig.Transport)
		}
		if clientErr != nil {
			return nil, fmt.Errorf("connect MCP server %q: %w", mcpConfig.ID, clientErr)
		}
		executors = append(executors, mcpClient)
	}
	var executor tooling.Executor
	if len(executors) > 0 {
		executor, buildErr = tooling.NewStrictMultiExecutor(executors...)
		if buildErr != nil {
			return nil, buildErr
		}
	}
	policy := policyFrom(snapshot.Runtime)
	policy.Loop = snapshot.Runtime.Agent.Defaults()
	policy.ImageUnderstanding = snapshot.Runtime.LLM.ImageUnderstanding
	policy.ExtensionInstructions = extensionInstructions
	return bot.NewEngine(ctx, bot.Dependencies{Memory: b.Memory, Sender: sender, LLM: client, Tools: executor, ToolBudgets: moduleSet.ToolBudgets(), Commands: moduleSet.Commands(), Policy: policy, Hooks: moduleSet.Chain(), Participants: moduleSet.Participants(), CloseHooks: moduleSet.Close, Generation: generationSequence.Add(1)}, nil)
}

func (b Builder) buildClient(ctx context.Context, snapshot config.Snapshot, secretRef, secretValue string) (llm.Client, error) {
	if strings.TrimSpace(snapshot.Runtime.LLM.Model) == "" {
		if b.RequireLLM {
			return nil, operation.New(operation.NotConfigured, "llm.model is required for normal runtime")
		}
		return nil, nil
	}
	apiKey := ""
	var err error
	if snapshot.Runtime.LLM.Authentication == config.AuthenticationNone {
		// 无认证必须显式声明；不能将丢失 API Key 自动解释为匿名服务。
	} else if secretRef == snapshot.Runtime.LLM.APIKeyRef {
		apiKey = secretValue
	} else if b.Secrets != nil {
		apiKey, err = b.Secrets.Get(ctx, snapshot.Runtime.LLM.APIKeyRef)
	}
	if err != nil && !errors.Is(err, secret.ErrNotFound) {
		return nil, fmt.Errorf("resolve LLM API key: %w", err)
	}
	if apiKey == "" && snapshot.Runtime.LLM.Authentication != config.AuthenticationNone {
		if b.RequireLLM {
			return nil, operation.New(operation.NotConfigured, "llm API key is not configured")
		}
		return nil, nil
	}
	factory := b.ClientFactory
	if factory == nil {
		factory = func(cfg llm.ClientConfig) (llm.Client, error) { return llm.NewClient(cfg) }
	}
	client, err := factory(snapshot.Runtime.LLM.ClientConfig(apiKey))
	if err != nil {
		return nil, fmt.Errorf("create LLM client: %w", err)
	}
	if b.RequireLLM && client == nil {
		return nil, operation.New(operation.NotConfigured, "LLM factory returned no client")
	}
	return client, nil
}

func (b Builder) initialCapabilities(snapshot config.Snapshot, client llm.Client) capability.Values {
	values := capability.NewValues()
	if b.recalls != nil {
		_ = values.Provide(group.RecallKey.ID(), b.recalls)
	}
	if b.Yield != nil {
		_ = values.Provide(proactive.YieldKey.ID(), b.Yield)
	}
	if b.Decisions != nil {
		_ = values.Provide(decision.ServiceKey.ID(), b.Decisions)
	}
	if completer := agent.NewCompleter(client, snapshot.Runtime.LLM.Model); completer != nil {
		_ = values.Provide(agent.CompleterKey.ID(), completer)
	}
	conversations := make(conversation.Set, len(snapshot.Runtime.Platforms.Groups))
	for _, group := range snapshot.Runtime.Platforms.Groups {
		conversations[strings.TrimSpace(group.ID)] = struct{}{}
	}
	_ = values.Provide(conversation.CatalogKey.ID(), conversation.Catalog(conversations))
	if b.History != nil {
		_ = values.Provide(history.ReaderKey.ID(), history.Reader(b.History))
	}
	return values
}

// ReplaceSecret builds a complete candidate runtime before making the secret
// durable, then publishes the already validated runtime without another fallible step.
func (m *Manager) ReplaceSecret(ctx context.Context, ref, value string, activate func() error) error {
	if activate == nil {
		return errors.New("secret activation callback is required")
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	current := m.current.Load()
	if current == nil {
		return errors.New("runtime is stopped")
	}
	if ref != current.snapshot.Runtime.LLM.APIKeyRef && (!current.snapshot.Runtime.Decision.DMEnabled() || ref != current.snapshot.Runtime.Decision.DM.APIKeyRef) {
		return activate()
	}
	m.setStatus(StateApplying, ActionReplaceSecret, current.snapshot.Version, "")
	next, err := m.buildGeneration(ctx, current.snapshot, current.connectors, ref, value)
	if err != nil {
		m.setStatus(StateReady, ActionReplaceSecret, current.snapshot.Version, err.Error())
		return err
	}
	if err := activate(); err != nil {
		next.engine.Shutdown(current.snapshot.Static.Server.ShutdownTimeout)
		m.setStatus(StateReady, ActionReplaceSecret, current.snapshot.Version, err.Error())
		return err
	}
	if _, err := m.publish(ctx, current, next, nil); err != nil {
		m.setStatus(StateReady, ActionReplaceSecret, current.snapshot.Version, err.Error())
		return err
	}
	m.setStatus(StateReady, ActionReplaceSecret, current.snapshot.Version, "")
	return nil
}

func (m *Manager) DeleteSecret(ctx context.Context, ref string, remove func() error) error {
	if remove == nil {
		return errors.New("secret delete callback is required")
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	current := m.current.Load()
	if current == nil {
		return errors.New("runtime is stopped")
	}
	if ref != current.snapshot.Runtime.LLM.APIKeyRef && (!current.snapshot.Runtime.Decision.DMEnabled() || ref != current.snapshot.Runtime.Decision.DM.APIKeyRef) {
		return remove()
	}
	m.setStatus(StateApplying, ActionReplaceSecret, current.snapshot.Version, "")
	next, err := m.buildGeneration(ctx, current.snapshot, current.connectors, ref, "")
	if err != nil {
		m.setStatus(StateReady, ActionReplaceSecret, current.snapshot.Version, err.Error())
		return err
	}
	if err := remove(); err != nil {
		next.engine.Shutdown(current.snapshot.Static.Server.ShutdownTimeout)
		m.setStatus(StateReady, ActionReplaceSecret, current.snapshot.Version, err.Error())
		return err
	}
	if _, err := m.publish(ctx, current, next, nil); err != nil {
		m.setStatus(StateReady, ActionReplaceSecret, current.snapshot.Version, err.Error())
		return err
	}
	m.setStatus(StateReady, ActionReplaceSecret, current.snapshot.Version, "")
	return nil
}

// Manager owns the active generation and coordinates lifecycle, updates,
// persistence, notifications, and administrative actions.
type Manager struct {
	groups   *group.Service
	retiring <-chan struct{}
	applyMu  sync.Mutex
	routeMu  sync.RWMutex
	statusMu sync.RWMutex
	current  atomic.Pointer[generation]
	snapshot atomic.Pointer[config.Snapshot]
	builder  Builder
	status   Status
	started  bool
	runCtx   context.Context
	errors   chan error
	operator operations.Service
	paused   atomic.Bool
}

type generation struct {
	decisions   decision.Decider
	modules     *modules.Set
	snapshot    config.Snapshot
	engine      *bot.Engine
	connectors  platform.ConnectorSet
	handler     *operations.Router
	watchCancel context.CancelFunc
	mu          sync.RWMutex
}

// NewManager validates and publishes the initial runtime generation.
func NewManager(ctx context.Context, snapshot config.Snapshot, builder Builder) (*Manager, error) {
	snapshot = snapshot.Clone()
	builder.Modules = builder.Modules.Clone()
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	if builder.Memory == nil {
		builder.Memory = conversation.NewMemory(snapshot.Runtime.ConversationMemory)
	}
	if builder.Yield == nil {
		builder.Yield = proactive.NewYieldState()
	}
	builder.Yield.Configure(boundConversationIDs(snapshot.Runtime))
	if builder.History == nil {
		builder.History = newHistory(snapshot, builder)
	}
	manager := &Manager{builder: builder, errors: make(chan error, 8), status: Status{State: StateReady, Action: ActionNone, Version: snapshot.Version, UpdatedAt: time.Now().UTC()}}
	manager.builder.recalls = manager
	generation, err := manager.buildGeneration(ctx, snapshot, nil, "", "")
	if err != nil {
		return nil, err
	}
	manager.current.Store(generation)
	builder.History.SetSubscribers(generation.modules.HistorySubscribers())
	manager.groups = group.New(manager, manager.groupPolicy, builder.Logger)
	manager.groups.SetReviewSecrets(builder.Secrets)
	generation.modules.Mark("active")
	manager.storeSnapshot(snapshot)
	return manager, nil
}

func (m *Manager) buildGeneration(ctx context.Context, snapshot config.Snapshot, connectors platform.ConnectorSet, secretRef, secretValue string) (*generation, error) {
	if m.retiring != nil {
		select {
		case <-m.retiring:
			m.retiring = nil
		default:
			return nil, ErrRuntimeBusy
		}
	}
	snapshot = snapshot.Clone()
	if err := m.builder.Modules.ValidateRuntime(snapshot.Runtime.Modules, snapshot.Runtime.Platforms); err != nil {
		return nil, err
	}
	client, err := m.builder.buildClient(ctx, snapshot, secretRef, secretValue)
	if err != nil {
		return nil, err
	}
	generationBuilder, err := m.builder.withDecision(ctx, snapshot, client, secretRef, secretValue)
	if err != nil {
		return nil, err
	}
	initial := generationBuilder.initialCapabilities(snapshot, client)
	moduleSet, err := m.builder.Modules.BuildWithCapabilities(ctx, snapshot.Runtime.Modules, nil, initial)
	if err != nil {
		return nil, err
	}
	if connectors == nil && len(moduleSet.ConnectorProviderTypes()) > 0 {
		connectors, err = m.builder.buildConnectors(ctx, snapshot.Runtime.Platforms, moduleSet)
		if err != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = moduleSet.Close(closeCtx)
			return nil, fmt.Errorf("build connectors: %w", err)
		}
	}
	sender := m.builder.Sender
	if connectors != nil {
		sender = connectors
	}
	sender = withHistory(sender, m.builder.History)
	engine, err := generationBuilder.build(ctx, snapshot, sender, client, moduleSet)
	if err != nil {
		return nil, err
	}
	// 运维群最多一个，路由仍按列表接收以复用既有授权逻辑。
	var policies []operations.GroupPolicy
	if operator := snapshot.Runtime.Operator; operator != nil {
		commands := make([]operations.Command, 0, len(operator.Commands))
		for _, command := range operator.Commands {
			commands = append(commands, operations.Command(strings.ToLower(strings.TrimSpace(command))))
		}
		policies = append(policies, operations.GroupPolicy{ConversationID: operator.ConversationID, Notifications: operator.Notifications, Commands: commands})
	}
	router, err := operations.NewRouter(pausedHandler{manager: m, next: engine}, m.operator, sender, policies)
	if err != nil {
		engine.Shutdown(snapshot.Static.Server.ShutdownTimeout)
		return nil, err
	}
	return &generation{
		decisions: generationBuilder.Decisions,
		modules:   moduleSet,
		snapshot:  snapshot, engine: engine, connectors: connectors, handler: router,
	}, nil
}

func (b Builder) buildConnectors(ctx context.Context, cfg config.PlatformConfig, moduleSet *modules.Set) (platform.ConnectorSet, error) {
	providers := make(map[string]modules.ProviderRegistration)
	if moduleSet != nil {
		for _, typeName := range moduleSet.ConnectorProviderTypes() {
			registration, _ := moduleSet.ConnectorProvider(typeName)
			providers[typeName] = registration
		}
	}
	adapters := make([]platform.Adapter, 0, len(cfg.Connectors))
	bindings := make([]platform.ConversationBinding, 0, len(cfg.Groups))
	for _, binding := range cfg.Groups {
		bindings = append(bindings, platform.ConversationBinding{ConversationID: binding.ID, ConnectorID: binding.ConnectorID, Target: binding.Target})
	}
	for _, connector := range cfg.Connectors {
		if !connector.Enabled {
			continue
		}
		registration, exists := providers[connector.Type]
		if !exists {
			return nil, fmt.Errorf("connector %s requires unavailable provider %q", connector.ID, connector.Type)
		}
		providerBindings := make([]platform.ConnectorBinding, 0)
		for _, binding := range cfg.Groups {
			if binding.ConnectorID == connector.ID {
				providerBindings = append(providerBindings, platform.ConnectorBinding{ConversationID: binding.ID, Target: binding.Target, PromptSuffix: binding.PromptSuffix})
			}
		}
		options := slices.Clone(connector.Options)
		if err := registration.Provider.ValidateOptions(registration.ModuleOptions, options, providerBindings); err != nil {
			return nil, fmt.Errorf("connector %s: %w", connector.ID, err)
		}
		adapter, err := registration.Provider.Build(ctx, platform.ConnectorBuildRequest{
			ConnectorID: connector.ID, PromptSuffix: connector.PromptSuffix,
			ModuleOptions: registration.ModuleOptions, ConnectorOptions: options,
			Bindings: providerBindings, Logger: b.Logger,
		})
		if err != nil {
			return nil, fmt.Errorf("connector %s: %w", connector.ID, err)
		}
		if adapter == nil {
			return nil, fmt.Errorf("connector %s provider returned nil adapter", connector.ID)
		}
		adapters = append(adapters, adapter)
	}
	return platform.NewSet(adapters, bindings)
}

func (b Builder) validateConnectors(ctx context.Context, cfg config.PlatformConfig, moduleSet *modules.Set) error {
	if moduleSet == nil {
		return errors.New("connector provider catalog is required")
	}
	for _, connector := range cfg.Connectors {
		if !connector.Enabled {
			continue
		}
		registration, exists := moduleSet.ConnectorProvider(connector.Type)
		if !exists {
			return fmt.Errorf("platforms.connectors[%s]: provider %q is unavailable", connector.ID, connector.Type)
		}
		bindings := connectorBindings(cfg, connector.ID)
		if err := registration.Provider.ValidateOptions(registration.ModuleOptions, connector.Options, bindings); err != nil {
			return fmt.Errorf("platforms.connectors[%s].options: %w", connector.ID, err)
		}
	}
	return nil
}

func connectorBindings(cfg config.PlatformConfig, connectorID string) []platform.ConnectorBinding {
	bindings := make([]platform.ConnectorBinding, 0)
	for _, binding := range cfg.Groups {
		if binding.ConnectorID == connectorID {
			bindings = append(bindings, platform.ConnectorBinding{ConversationID: binding.ID, Target: binding.Target, PromptSuffix: binding.PromptSuffix})
		}
	}
	return bindings
}

type pausedHandler struct {
	manager *Manager
	next    platform.Handler
}

func (h pausedHandler) HandleInbound(ctx context.Context, message messaging.InboundMessage) (platform.HandleResult, error) {
	if h.manager.Paused() {
		return platform.HandleResult{Accepted: false, Verdict: "paused"}, nil
	}
	return h.next.HandleInbound(ctx, message)
}

func (m *Manager) Snapshot() (config.Snapshot, Status) {
	generation := m.current.Load()
	m.statusMu.RLock()
	status := m.status
	status.Paused = m.paused.Load()
	m.statusMu.RUnlock()
	if generation == nil {
		return config.Snapshot{}, status
	}
	snapshot := m.snapshot.Load().Clone()
	status.Modules = generation.modules.Status()
	if generation.connectors != nil {
		status.Connectors = generation.connectors.Status()
	}
	return snapshot, status
}

func (m *Manager) ScanReflection(ctx context.Context) (ReflectionScanResult, error) {
	generation := m.acquire()
	if generation == nil {
		return ReflectionScanResult{}, errors.New("runtime is unavailable")
	}
	control, ok := capability.Resolve(generation.modules.Capabilities(), reflectioncontract.ControlKey)
	generation.mu.RUnlock()
	if !ok || control == nil {
		return ReflectionScanResult{}, ErrReflectionDisabled
	}
	return control.Scan(ctx)
}

func (m *Manager) storeSnapshot(snapshot config.Snapshot) {
	copy := snapshot.Clone()
	m.snapshot.Store(&copy)
}

func (m *Manager) Start(ctx context.Context) error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	if m.started {
		return errors.New("runtime is already started")
	}
	current := m.current.Load()
	if current == nil {
		return errors.New("runtime is stopped")
	}
	m.runCtx = ctx
	if current.connectors != nil {
		if err := current.connectors.Start(ctx, m); err != nil {
			m.setStatus(StateDegraded, ActionRestartConnectors, current.snapshot.Version, err.Error())
			return err
		}
		m.watchConnectorErrors(current)
	}
	current.modules.Start(ctx)
	m.started = true
	m.setStatus(StateReady, ActionNone, current.snapshot.Version, "")
	return nil
}

func (m *Manager) Errors() <-chan error { return m.errors }

func (m *Manager) SetNotificationPublisher(publisher notification.Publisher) {
	m.applyMu.Lock()
	m.builder.Notifications = publisher
	m.applyMu.Unlock()
}

func (m *Manager) NotificationTargets() []string {
	snapshot, _ := m.Snapshot()
	if operator := snapshot.Runtime.Operator; operator != nil && operator.Notifications {
		return []string{operator.ConversationID}
	}
	return nil
}

func (m *Manager) SendNotification(ctx context.Context, conversationID, text string) error {
	generation := m.acquire()
	if generation == nil {
		return errors.New("runtime is unavailable")
	}
	defer generation.mu.RUnlock()
	if generation.connectors == nil {
		return errors.New("runtime connectors are unavailable")
	}
	return withHistory(generation.connectors, m.builder.History).Send(ctx, messaging.OutboundMessage{ConversationID: conversationID, Text: text})
}

func (m *Manager) Paused() bool { return m.paused.Load() }

func (m *Manager) SetPaused(paused bool) { m.paused.Store(paused) }

func (m *Manager) SetOperatorService(service operations.Service) {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	m.operator = service
	if current := m.current.Load(); current != nil {
		current.handler.SetService(service)
	}
}

func (m *Manager) watchConnectorErrors(generation *generation) {
	watchCtx, cancel := context.WithCancel(m.runCtx)
	generation.watchCancel = cancel
	go func() {
		for {
			select {
			case <-watchCtx.Done():
				return
			case err, ok := <-generation.connectors.Errors():
				if !ok {
					return
				}
				if err == nil {
					continue
				}
				snapshot, _ := m.Snapshot()
				m.setStatus(StateDegraded, ActionRestartConnectors, snapshot.Version, err.Error())
				select {
				case m.errors <- err:
				default:
				}
			}
		}
	}()
}

// Apply builds a candidate generation and swaps it in atomically after success.
func (m *Manager) Apply(ctx context.Context, snapshot config.Snapshot) (ApplyResult, error) {
	snapshot = snapshot.Clone()
	if err := m.builder.Modules.ValidateRuntime(snapshot.Runtime.Modules, snapshot.Runtime.Platforms); err != nil {
		return ApplyResult{}, err
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	current := m.current.Load()
	if current == nil {
		return ApplyResult{}, errors.New("runtime is stopped")
	}
	if !reflect.DeepEqual(current.snapshot.Static, snapshot.Static) {
		return ApplyResult{}, ErrStaticConfigChanged
	}
	if err := snapshot.Validate(); err != nil {
		return ApplyResult{}, err
	}
	action := m.classify(current.snapshot.Runtime, snapshot.Runtime)
	if action == ActionNone {
		return ApplyResult{Action: action, Status: m.readStatus(), Snapshot: current.snapshot.Clone()}, nil
	}
	m.setStatus(StateApplying, action, current.snapshot.Version, "")
	connectors := current.connectors
	if action == ActionRestartConnectors {
		connectors = nil
	}
	next, err := m.buildGeneration(ctx, snapshot, connectors, "", "")
	if err != nil {
		m.setStatus(StateReady, action, current.snapshot.Version, err.Error())
		return ApplyResult{}, err
	}
	if _, err := m.publish(ctx, current, next, nil); err != nil {
		m.setStatus(StateReady, action, current.snapshot.Version, err.Error())
		return ApplyResult{}, err
	}
	m.setStatus(StateReady, action, snapshot.Version, "")
	return ApplyResult{Action: action, Status: m.readStatus(), Snapshot: snapshot}, nil
}

// CommitRuntime builds a candidate before persistence and only publishes it
// after persistence returns the matching, monotonically newer snapshot.
func (m *Manager) CommitRuntime(ctx context.Context, runtimeConfig config.RuntimeConfig, persist Persist) (ApplyResult, error) {
	runtimeConfig = runtimeConfig.Clone()
	if err := m.builder.Modules.ValidateRuntime(runtimeConfig.Modules, runtimeConfig.Platforms); err != nil {
		return ApplyResult{}, operation.Wrap("config.validate", operation.Invalid, operation.NotStarted, err)
	}
	if persist == nil {
		return ApplyResult{}, errors.New("runtime persistence callback is required")
	}
	if err := runtimeConfig.Validate(); err != nil {
		return ApplyResult{}, operation.Wrap("config.validate", operation.Invalid, operation.NotStarted, err)
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	current := m.current.Load()
	if current == nil {
		return ApplyResult{}, errors.New("runtime is stopped")
	}
	action := m.classify(current.snapshot.Runtime, runtimeConfig)
	if action == ActionNone {
		persisted, err := persist()
		if err != nil {
			return ApplyResult{}, err
		}
		if !reflect.DeepEqual(persisted.Static, current.snapshot.Static) || !reflect.DeepEqual(persisted.Runtime, runtimeConfig) || persisted.Version <= current.snapshot.Version {
			return ApplyResult{}, errors.New("persisted runtime snapshot does not match candidate")
		}
		current.mu.Lock()
		current.snapshot = persisted.Clone()
		current.mu.Unlock()
		m.storeSnapshot(persisted)
		m.setStatus(StateReady, action, persisted.Version, "")
		return ApplyResult{Action: action, Status: m.readStatus(), Snapshot: persisted}, nil
	}
	candidate := current.snapshot
	candidate.Runtime = runtimeConfig
	m.setStatus(StateApplying, action, current.snapshot.Version, "")
	connectors := current.connectors
	if action == ActionRestartConnectors {
		connectors = nil
	}
	next, err := m.buildGeneration(ctx, candidate, connectors, "", "")
	if err != nil {
		m.setStatus(StateReady, action, current.snapshot.Version, err.Error())
		return ApplyResult{}, err
	}
	persisted, err := m.publish(ctx, current, next, func() (config.Snapshot, error) {
		persisted, persistErr := persist()
		if persistErr != nil {
			return config.Snapshot{}, persistErr
		}
		if !reflect.DeepEqual(persisted.Static, current.snapshot.Static) || !reflect.DeepEqual(persisted.Runtime, runtimeConfig) || persisted.Version <= current.snapshot.Version {
			return config.Snapshot{}, errors.New("persisted runtime snapshot does not match candidate")
		}
		return persisted, nil
	})
	if err != nil {
		m.setStatus(StateReady, action, current.snapshot.Version, err.Error())
		return ApplyResult{}, err
	}
	m.setStatus(StateReady, action, persisted.Version, "")
	return ApplyResult{Action: action, Status: m.readStatus(), Snapshot: persisted}, nil
}

// Restart rebuilds the active generation from its current snapshot.
func (m *Manager) Restart(ctx context.Context, reason string) error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	current := m.current.Load()
	if current == nil {
		return errors.New("runtime is stopped")
	}
	m.setStatus(StateRestarting, ActionRestartBot, current.snapshot.Version, "")
	next, err := m.buildGeneration(ctx, current.snapshot, nil, "", "")
	if err != nil {
		m.setStatus(StateReady, ActionRestartBot, current.snapshot.Version, err.Error())
		return fmt.Errorf("restart runtime (%s): %w", reason, err)
	}
	if _, err := m.publish(ctx, current, next, nil); err != nil {
		m.setStatus(StateReady, ActionRestartBot, current.snapshot.Version, err.Error())
		return fmt.Errorf("restart runtime (%s): %w", reason, err)
	}
	m.setStatus(StateReady, ActionRestartBot, current.snapshot.Version, "")
	return nil
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	old := m.current.Load()
	if old == nil {
		return nil
	}
	if old.connectors != nil {
		if err := old.connectors.Stop(ctx); err != nil {
			m.setStatus(StateDegraded, ActionNone, old.snapshot.Version, err.Error())
			return err
		}
	}
	m.current.Store(nil)
	m.builder.History.SetSubscribers(nil)
	if old.watchCancel != nil {
		old.watchCancel()
	}
	m.drain(old, old.snapshot.Static.Server.ShutdownTimeout)
	m.started = false
	m.setStatus(StateStopped, ActionNone, old.snapshot.Version, "")
	return nil
}

// ReloadSearch rebuilds the active structured index without replacing other components.
func (m *Manager) ReloadSearch(ctx context.Context, source string) (searchcontract.ReloadResult, error) {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	current := m.current.Load()
	if current == nil {
		return searchcontract.ReloadResult{}, errors.New("runtime is stopped")
	}
	control, ok := capability.Resolve(current.modules.Capabilities(), searchcontract.ControlKey)
	if !ok || control == nil {
		return searchcontract.ReloadResult{}, searchcontract.ErrDisabled
	}
	result, err := control.Reload(ctx)
	if err != nil {
		m.notify(notification.Event{Level: notification.LevelError, Component: "search", Code: "reload_failed", Message: err.Error(), ExcludeConversationID: strings.TrimSpace(source)})
		return searchcontract.ReloadResult{}, err
	}
	m.notify(notification.Event{Level: notification.LevelInfo, Component: "search", Code: "reloaded", Message: fmt.Sprintf("文档索引重载完成：documents=%d sections=%d warnings=%d", result.Documents, result.Sections, result.Warnings), ExcludeConversationID: strings.TrimSpace(source)})
	return result, nil
}

func (m *Manager) HandleInbound(ctx context.Context, message messaging.InboundMessage) (platform.HandleResult, error) {
	m.routeMu.RLock()
	defer m.routeMu.RUnlock()
	generation := m.acquire()
	if generation == nil {
		return platform.HandleResult{}, errors.New("runtime is unavailable")
	}
	defer generation.mu.RUnlock()
	m.builder.History.Append(history.FromInbound(message))
	for _, observer := range generation.modules.Observers() {
		observer.Observe(message)
	}
	result, err := generation.handler.HandleInbound(ctx, message)
	if err != nil {
		component := "bot"
		if result.Verdict == "llm_failed" {
			component = "llm"
		} else if result.Verdict == "send_failed" || result.Verdict == "operator_reply_failed" {
			component = "connector"
		}
		m.notify(notification.Event{Level: notification.LevelError, Component: component, Code: result.Verdict, Message: err.Error()})
	}
	return result, err
}

func (m *Manager) ProactiveStats() proactive.Stats {
	generation := m.acquire()
	if generation == nil {
		return proactive.Stats{}
	}
	defer generation.mu.RUnlock()
	control, ok := capability.Resolve(generation.modules.Capabilities(), proactive.ControlKey)
	if !ok || control == nil {
		return proactive.Stats{}
	}
	return control.Stats()
}

func (m *Manager) acquire() *generation {
	// 先读取指针再持读锁，并重新核对当前代。调用者必须释放读锁，
	// 以保证旧代在整个请求结束前不会关闭；不能只用一次原子读取替代租约。
	for {
		generation := m.current.Load()
		if generation == nil {
			return nil
		}
		generation.mu.RLock()
		if m.current.Load() == generation {
			return generation
		}
		generation.mu.RUnlock()
	}
}

func (m *Manager) drain(generation *generation, grace time.Duration) {
	// 先停止旧代接受后台待答，再等待已取得读锁的请求完成。宽限期到期
	// 只让更新调用返回，不提前销毁仍在执行的资源；下一次换代受 retiring 限制。
	if generation == nil {
		return
	}
	generation.modules.Retire()
	done := make(chan struct{})
	m.retiring = done
	go func() {
		generation.mu.Lock()
		defer generation.mu.Unlock()
		defer close(done)
		generation.engine.Shutdown(0)
	}()
	if grace <= 0 {
		<-done
		m.retiring = nil
		return
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
		m.retiring = nil
	case <-timer.C:
	}
}

func (m *Manager) publish(_ context.Context, current, next *generation, persist func() (config.Snapshot, error)) (config.Snapshot, error) {
	// 可失败的连接器切换和持久化必须位于发布前。进程级记忆/让位策略
	// 只在候选成功后调整，不能因候选构建失败破坏当前代的有效状态。
	connectorsChanged := current.connectors != next.connectors
	if connectorsChanged && m.started {
		m.routeMu.Lock()
		defer m.routeMu.Unlock()
		stopCtx, cancel := context.WithTimeout(context.Background(), current.snapshot.Static.Server.ShutdownTimeout)
		stopErr := current.connectors.Stop(stopCtx)
		cancel()
		if stopErr != nil {
			next.engine.Shutdown(current.snapshot.Static.Server.ShutdownTimeout)
			return config.Snapshot{}, stopErr
		}
		if err := next.connectors.Start(m.runCtx, m); err != nil {
			restoreErr := current.connectors.Start(m.runCtx, m)
			next.engine.Shutdown(current.snapshot.Static.Server.ShutdownTimeout)
			if restoreErr != nil {
				return config.Snapshot{}, errors.Join(err, fmt.Errorf("restore previous connectors: %w", restoreErr))
			}
			return config.Snapshot{}, err
		}
	}
	published := next.snapshot
	if persist != nil {
		var err error
		published, err = persist()
		if err != nil {
			m.rollbackConnectors(current, next, connectorsChanged)
			next.engine.Shutdown(current.snapshot.Static.Server.ShutdownTimeout)
			return config.Snapshot{}, err
		}
		next.snapshot = published.Clone()
	}
	if m.started {
		next.modules.Start(m.runCtx)
	} else {
		next.modules.Mark("active")
	}
	m.builder.Memory.Configure(next.snapshot.Runtime.ConversationMemory)
	m.builder.Yield.Configure(boundConversationIDs(next.snapshot.Runtime))
	m.builder.History.Configure(next.snapshot.Runtime.MessageHistory, boundConversationIDs(next.snapshot.Runtime))
	old := m.current.Swap(next)
	m.builder.History.SetSubscribers(next.modules.HistorySubscribers())
	m.storeSnapshot(next.snapshot)
	if m.started {
		if connectorsChanged {
			if old.watchCancel != nil {
				old.watchCancel()
			}
			m.watchConnectorErrors(next)
		} else {
			next.watchCancel = old.watchCancel
			old.watchCancel = nil
		}
	}
	m.drain(old, next.snapshot.Static.Server.ShutdownTimeout)
	return published.Clone(), nil
}

func (m *Manager) ValidateConfiguration(cfg config.RuntimeConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	providers, err := m.builder.Modules.BuildConnectorProviders(context.Background(), cfg.Modules, m.builder.Logger)
	if err != nil {
		return err
	}
	defer providers.Close(context.Background())
	return m.builder.validateConnectors(context.Background(), cfg.Platforms, providers)
}

func (m *Manager) PublicModules(cfg config.RuntimeConfig) (map[string]any, error) {
	return m.builder.Modules.Public(cfg.Modules)
}

// PublicPlatforms renders protocol options through the provider that owns
// them. Unknown disabled connectors never expose their raw opaque options.
func (m *Manager) PublicPlatforms(ctx context.Context, cfg config.RuntimeConfig) (map[string]any, error) {
	providers, err := m.builder.Modules.BuildConnectorProviders(ctx, cfg.Modules, m.builder.Logger)
	if err != nil {
		return nil, err
	}
	defer providers.Close(context.Background())
	connectors := make([]any, 0, len(cfg.Platforms.Connectors))
	for _, connector := range cfg.Platforms.Connectors {
		item := map[string]any{"id": connector.ID, "type": connector.Type, "enabled": connector.Enabled, "prompt_suffix": connector.PromptSuffix}
		registration, available := providers.ConnectorProvider(connector.Type)
		if !available {
			item["provider_available"] = false
			connectors = append(connectors, item)
			continue
		}
		options, publicErr := registration.Provider.PublicOptions(registration.ModuleOptions, connector.Options)
		if publicErr != nil {
			return nil, fmt.Errorf("platforms.connectors[%s].options: %w", connector.ID, publicErr)
		}
		item["options"] = options
		item["provider_available"] = true
		connectors = append(connectors, item)
	}
	return map[string]any{"connectors": connectors, "groups": cfg.Platforms.Groups}, nil
}

func (m *Manager) rollbackConnectors(current, next *generation, changed bool) {
	if !changed || !m.started {
		return
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), current.snapshot.Static.Server.ShutdownTimeout)
	_ = next.connectors.Stop(stopCtx)
	cancel()
	_ = current.connectors.Start(m.runCtx, m)
}

func (m *Manager) setStatus(state State, action Action, version uint64, message string) {
	m.statusMu.Lock()
	m.status = Status{State: state, Action: action, Version: version, UpdatedAt: time.Now().UTC(), Error: message}
	m.statusMu.Unlock()
	if message != "" {
		m.notify(notification.Event{Level: notification.LevelError, Component: "runtime", Code: string(action), Message: message})
	}
}

func (m *Manager) notify(event notification.Event) {
	if m.builder.Notifications != nil {
		m.builder.Notifications.Publish(event)
	}
}

func (m *Manager) readStatus() Status {
	m.statusMu.RLock()
	defer m.statusMu.RUnlock()
	return m.status
}

func (m *Manager) classify(current, next config.RuntimeConfig) Action {
	if reflect.DeepEqual(current, next) {
		return ActionNone
	}
	if !reflect.DeepEqual(current.Platforms, next.Platforms) {
		return ActionRestartConnectors
	}
	if m.builder.Modules.ConnectorProvidersChanged(current.Modules, next.Modules) {
		return ActionRestartConnectors
	}
	if !reflect.DeepEqual(current.Modules, next.Modules) {
		return ActionReplaceModules
	}
	withoutLLMCurrent, withoutLLMNext := current, next
	withoutLLMCurrent.LLM, withoutLLMNext.LLM = config.LLMConfig{}, config.LLMConfig{}
	if reflect.DeepEqual(withoutLLMCurrent, withoutLLMNext) {
		return ActionReplaceLLM
	}
	return ActionRestartEngine
}

func policyFrom(runtime config.RuntimeConfig) bot.Policy {
	return bot.Policy{Name: runtime.Bot.Name, SystemPrompt: runtime.Bot.SystemPrompt, Model: runtime.LLM.Model, Admission: bot.AdmissionPolicy{
		DedupTTL: runtime.Admission.DedupTTL, DedupMax: runtime.Admission.DedupMax,
	}}
}
