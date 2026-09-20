// Package runtime builds and swaps complete application generations. A new
// generation is validated and started before it becomes active, so failed
// hot updates leave the previous generation serving traffic.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/soleapi/forgebot/internal/administration"
	"github.com/soleapi/forgebot/internal/agent"
	"github.com/soleapi/forgebot/internal/bot"
	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/conversation"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/notification"
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
	ErrStaticConfigChanged   = errors.New("static configuration requires a process restart")
	ErrReflectionDisabled    = errors.New("reflection is disabled")
	ErrReflectionUnavailable = errors.New("reflection scanner is unavailable")
	ErrRuntimeBusy           = errors.New("runtime_busy: previous generation is still retiring")
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
	ActionUpdateLog         Action = "update_log"
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
	Search     searchcontract.Status      `json:"search"`
	Reflection reflectioncontract.Status  `json:"reflection"`
}

type ReflectionStatus = reflectioncontract.Status
type ReflectionScanResult = reflectioncontract.ScanResult

type ApplyResult struct {
	Action   Action          `json:"action"`
	Status   Status          `json:"status"`
	Snapshot config.Snapshot `json:"snapshot"`
}

type ClientFactory func(llm.ClientConfig) (llm.Client, error)
type ConnectorFactory func(config.PlatformConfig) (platform.ConnectorSet, error)
type Persist func() (config.Snapshot, error)

// Builder contains factories for all replaceable runtime components.
type Builder struct {
	Modules          *modules.Registry
	Sender           platform.Sender
	Secrets          secret.Store
	ClientFactory    ClientFactory
	ConnectorFactory ConnectorFactory
	ApplyLog         func(config.LogConfig)
	Notifications    notification.Publisher
}

// Build constructs a complete bot engine and its configured dependencies.
func (b Builder) Build(ctx context.Context, snapshot config.Snapshot) (*bot.Engine, error) {
	snapshot = snapshot.Clone()
	if err := b.Modules.Validate(snapshot.Runtime.Modules); err != nil {
		return nil, err
	}
	client, err := b.buildClient(ctx, snapshot, "", "")
	if err != nil {
		return nil, err
	}
	initial := b.initialCapabilities(snapshot, client)
	moduleSet, err := b.Modules.BuildWithCapabilities(ctx, snapshot.Runtime.Modules, nil, initial)
	if err != nil {
		return nil, err
	}
	engine, err := b.build(ctx, snapshot, b.Sender, client, moduleSet)
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
	policy.ExtensionInstructions = extensionInstructions
	return bot.NewEngine(ctx, bot.Dependencies{Sender: sender, LLM: client, Tools: executor, ToolBudgets: moduleSet.ToolBudgets(), Commands: moduleSet.Commands(), Policy: policy, Hooks: moduleSet.Chain(), Participants: moduleSet.Participants(), CloseHooks: moduleSet.Close, Generation: generationSequence.Add(1)}, nil)
}

func (b Builder) buildClient(ctx context.Context, snapshot config.Snapshot, secretRef, secretValue string) (llm.Client, error) {
	if strings.TrimSpace(snapshot.Runtime.LLM.Model) == "" {
		return nil, nil
	}
	apiKey := ""
	var err error
	if secretRef == snapshot.Runtime.LLM.APIKeyRef {
		apiKey = secretValue
	} else if b.Secrets != nil {
		apiKey, err = b.Secrets.Get(ctx, snapshot.Runtime.LLM.APIKeyRef)
	}
	if err != nil && !errors.Is(err, secret.ErrNotFound) {
		return nil, fmt.Errorf("resolve LLM API key: %w", err)
	}
	if apiKey == "" {
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
	return client, nil
}

func (b Builder) initialCapabilities(snapshot config.Snapshot, client llm.Client) capability.Values {
	values := capability.NewValues()
	if completer := agent.NewCompleter(client, snapshot.Runtime.LLM.Model); completer != nil {
		_ = values.Provide(agent.CompleterKey.ID(), completer)
	}
	conversations := make(conversation.Set, len(snapshot.Runtime.Platforms.Groups))
	for _, group := range snapshot.Runtime.Platforms.Groups {
		conversations[strings.TrimSpace(group.ID)] = struct{}{}
	}
	_ = values.Provide(conversation.CatalogKey.ID(), conversation.Catalog(conversations))
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
	if ref != current.snapshot.Runtime.LLM.APIKeyRef {
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
	if ref != current.snapshot.Runtime.LLM.APIKeyRef {
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
	modules     *modules.Set
	snapshot    config.Snapshot
	engine      *bot.Engine
	connectors  platform.ConnectorSet
	handler     *operations.Router
	admins      administration.Resolver
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
	manager := &Manager{builder: builder, errors: make(chan error, 8), status: Status{State: StateReady, Action: ActionNone, Version: snapshot.Version, UpdatedAt: time.Now().UTC()}}
	generation, err := manager.buildGeneration(ctx, snapshot, nil, "", "")
	if err != nil {
		return nil, err
	}
	manager.current.Store(generation)
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
	if err := m.builder.Modules.Validate(snapshot.Runtime.Modules); err != nil {
		return nil, err
	}
	if connectors == nil && m.builder.ConnectorFactory != nil {
		var err error
		connectors, err = m.builder.ConnectorFactory(snapshot.Runtime.Platforms)
		if err != nil {
			return nil, fmt.Errorf("build connectors: %w", err)
		}
	}
	sender := m.builder.Sender
	if connectors != nil {
		sender = connectors
	}
	client, err := m.builder.buildClient(ctx, snapshot, secretRef, secretValue)
	if err != nil {
		return nil, err
	}
	initial := m.builder.initialCapabilities(snapshot, client)
	moduleSet, err := m.builder.Modules.BuildWithCapabilities(ctx, snapshot.Runtime.Modules, nil, initial)
	if err != nil {
		return nil, err
	}
	engine, err := m.builder.build(ctx, snapshot, sender, client, moduleSet)
	if err != nil {
		return nil, err
	}
	policies := make([]operations.GroupPolicy, 0, len(snapshot.Runtime.Operations.Groups))
	for _, group := range snapshot.Runtime.Operations.Groups {
		commands := make([]operations.Command, 0, len(group.Commands))
		for _, command := range group.Commands {
			commands = append(commands, operations.Command(strings.ToLower(strings.TrimSpace(command))))
		}
		policies = append(policies, operations.GroupPolicy{ConversationID: group.ConversationID, Notifications: group.Notifications, Commands: commands})
	}
	router, err := operations.NewRouter(pausedHandler{manager: m, next: engine}, m.operator, sender, policies)
	if err != nil {
		engine.Shutdown(snapshot.Static.Server.ShutdownTimeout)
		return nil, err
	}
	adminIdentities := make([]administration.Identity, 0, len(snapshot.Runtime.Administration.Administrators))
	for _, administrator := range snapshot.Runtime.Administration.Administrators {
		adminIdentities = append(adminIdentities, administration.Identity{
			ConversationID: administrator.ConversationID, UserID: administrator.UserID,
		})
	}
	return &generation{
		modules:  moduleSet,
		snapshot: snapshot, engine: engine, connectors: connectors, handler: router,
		admins: administration.New(adminIdentities),
	}, nil
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
	if control, ok := capability.Resolve(generation.modules.Capabilities(), searchcontract.ControlKey); ok && control != nil {
		status.Search = control.Status()
	} else {
		status.Search = searchcontract.Status{Enabled: false, State: searchcontract.StateDisabled}
	}
	status.Reflection = m.reflectionStatus(generation)
	return snapshot, status
}

func (m *Manager) reflectionStatus(generation *generation) ReflectionStatus {
	control, ok := capability.Resolve(generation.modules.Capabilities(), reflectioncontract.ControlKey)
	if !ok || control == nil {
		return ReflectionStatus{}
	}
	return control.Status()
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
	targets := make([]string, 0, len(snapshot.Runtime.Operations.Groups))
	for _, group := range snapshot.Runtime.Operations.Groups {
		if group.Notifications {
			targets = append(targets, group.ConversationID)
		}
	}
	return targets
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
	return generation.connectors.Send(ctx, messaging.OutboundMessage{ConversationID: conversationID, Text: text})
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
	if err := m.builder.Modules.Validate(snapshot.Runtime.Modules); err != nil {
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
	action := classify(current.snapshot.Runtime, snapshot.Runtime)
	if action == ActionNone {
		return ApplyResult{Action: action, Status: m.readStatus(), Snapshot: current.snapshot.Clone()}, nil
	}
	m.setStatus(StateApplying, action, current.snapshot.Version, "")
	connectors := current.connectors
	if !reflect.DeepEqual(current.snapshot.Runtime.Platforms, snapshot.Runtime.Platforms) {
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
	if err := m.builder.Modules.Validate(runtimeConfig.Modules); err != nil {
		return ApplyResult{}, err
	}
	if persist == nil {
		return ApplyResult{}, errors.New("runtime persistence callback is required")
	}
	if err := runtimeConfig.Validate(); err != nil {
		return ApplyResult{}, err
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	current := m.current.Load()
	if current == nil {
		return ApplyResult{}, errors.New("runtime is stopped")
	}
	action := classify(current.snapshot.Runtime, runtimeConfig)
	if action == ActionNone || action == ActionUpdateLog {
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
		if action == ActionUpdateLog && m.builder.ApplyLog != nil {
			m.builder.ApplyLog(persisted.Runtime.Log)
		}
		m.setStatus(StateReady, action, persisted.Version, "")
		return ApplyResult{Action: action, Status: m.readStatus(), Snapshot: persisted}, nil
	}
	candidate := current.snapshot
	candidate.Runtime = runtimeConfig
	m.setStatus(StateApplying, action, current.snapshot.Version, "")
	connectors := current.connectors
	if !reflect.DeepEqual(current.snapshot.Runtime.Platforms, runtimeConfig.Platforms) {
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
	if m.builder.ApplyLog != nil {
		m.builder.ApplyLog(persisted.Runtime.Log)
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
	message = generation.admins.Mark(message)
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
	old := m.current.Swap(next)
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
	return m.builder.Modules.Validate(cfg.Modules)
}

func (m *Manager) PublicModules(cfg config.RuntimeConfig) (map[string]any, error) {
	return m.builder.Modules.Public(cfg.Modules)
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

func classify(current, next config.RuntimeConfig) Action {
	if reflect.DeepEqual(current, next) {
		return ActionNone
	}
	withoutLogCurrent, withoutLogNext := current, next
	withoutLogCurrent.Log, withoutLogNext.Log = config.LogConfig{}, config.LogConfig{}
	if reflect.DeepEqual(withoutLogCurrent, withoutLogNext) {
		return ActionUpdateLog
	}
	if !reflect.DeepEqual(current.Platforms, next.Platforms) {
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
