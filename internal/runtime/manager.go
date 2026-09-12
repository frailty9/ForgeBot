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

	"github.com/soleapi/forgebot/internal/bot"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/gate"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/operations"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/secret"
)

var ErrStaticConfigChanged = errors.New("static configuration requires a process restart")

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
	ActionRestartEngine     Action = "restart_engine"
	ActionRestartConnectors Action = "restart_connectors"
	ActionRestartBot        Action = "restart_bot"
)

type Status struct {
	State      State                      `json:"state"`
	Action     Action                     `json:"action"`
	Version    uint64                     `json:"version"`
	UpdatedAt  time.Time                  `json:"updated_at"`
	Error      string                     `json:"error,omitempty"`
	Connectors []platform.ConnectorStatus `json:"connectors,omitempty"`
}

type ApplyResult struct {
	Action   Action          `json:"action"`
	Status   Status          `json:"status"`
	Snapshot config.Snapshot `json:"snapshot"`
}

type ClientFactory func(llm.ClientConfig) (llm.Client, error)
type ConnectorFactory func(config.PlatformConfig) (platform.ConnectorSet, error)
type Persist func() (config.Snapshot, error)

type Builder struct {
	Sender           platform.Sender
	Secrets          secret.Store
	ClientFactory    ClientFactory
	ConnectorFactory ConnectorFactory
	ApplyLog         func(config.LogConfig)
}

func (b Builder) Build(ctx context.Context, snapshot config.Snapshot) (*bot.Engine, error) {
	return b.build(ctx, snapshot, b.Sender, "", "")
}

func (b Builder) build(ctx context.Context, snapshot config.Snapshot, sender platform.Sender, secretRef, secretValue string) (*bot.Engine, error) {
	if sender == nil {
		return nil, errors.New("runtime sender is required")
	}
	clientFactory := b.ClientFactory
	if clientFactory == nil {
		clientFactory = func(cfg llm.ClientConfig) (llm.Client, error) { return llm.NewClient(cfg) }
	}
	var client llm.Client
	if snapshot.Runtime.LLM.Model != "" {
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
		if apiKey != "" {
			client, err = clientFactory(snapshot.Runtime.LLM.ClientConfig(apiKey))
			if err != nil {
				return nil, fmt.Errorf("create LLM client: %w", err)
			}
		}
	}
	return bot.NewEngine(ctx, bot.Dependencies{Sender: sender, LLM: client, Policy: policyFrom(snapshot.Runtime)}, nil)
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

type Manager struct {
	applyMu  sync.Mutex
	routeMu  sync.RWMutex
	statusMu sync.RWMutex
	current  atomic.Pointer[generation]
	builder  Builder
	status   Status
	started  bool
	runCtx   context.Context
	errors   chan error
	operator operations.Service
}

type generation struct {
	snapshot    config.Snapshot
	engine      *bot.Engine
	connectors  platform.ConnectorSet
	handler     *operations.Router
	watchCancel context.CancelFunc
	mu          sync.RWMutex
}

func NewManager(ctx context.Context, snapshot config.Snapshot, builder Builder) (*Manager, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	manager := &Manager{builder: builder, errors: make(chan error, 8), status: Status{State: StateReady, Action: ActionNone, Version: snapshot.Version, UpdatedAt: time.Now().UTC()}}
	generation, err := manager.buildGeneration(ctx, snapshot, nil, "", "")
	if err != nil {
		return nil, err
	}
	manager.current.Store(generation)
	return manager, nil
}

func (m *Manager) buildGeneration(ctx context.Context, snapshot config.Snapshot, connectors platform.ConnectorSet, secretRef, secretValue string) (*generation, error) {
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
	engine, err := m.builder.build(ctx, snapshot, sender, secretRef, secretValue)
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
	router, err := operations.NewRouter(engine, m.operator, policies)
	if err != nil {
		engine.Shutdown(snapshot.Static.Server.ShutdownTimeout)
		return nil, err
	}
	return &generation{snapshot: snapshot, engine: engine, connectors: connectors, handler: router}, nil
}

func (m *Manager) Snapshot() (config.Snapshot, Status) {
	generation := m.current.Load()
	m.statusMu.RLock()
	status := m.status
	m.statusMu.RUnlock()
	if generation == nil {
		return config.Snapshot{}, status
	}
	generation.mu.RLock()
	snapshot := generation.snapshot
	if generation.connectors != nil {
		status.Connectors = generation.connectors.Status()
	}
	generation.mu.RUnlock()
	return snapshot, status
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
	m.started = true
	m.setStatus(StateReady, ActionNone, current.snapshot.Version, "")
	return nil
}

func (m *Manager) Errors() <-chan error { return m.errors }

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

func (m *Manager) Apply(ctx context.Context, snapshot config.Snapshot) (ApplyResult, error) {
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
		return ApplyResult{Action: action, Status: m.readStatus(), Snapshot: current.snapshot}, nil
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
		current.snapshot = persisted
		current.mu.Unlock()
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
	if m.builder.ApplyLog != nil {
		m.builder.ApplyLog(persisted.Runtime.Log)
	}
	m.setStatus(StateReady, action, persisted.Version, "")
	return ApplyResult{Action: action, Status: m.readStatus(), Snapshot: persisted}, nil
}

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

func (m *Manager) HandleInbound(ctx context.Context, message messaging.InboundMessage) (platform.HandleResult, error) {
	m.routeMu.RLock()
	defer m.routeMu.RUnlock()
	generation := m.acquire()
	if generation == nil {
		return platform.HandleResult{}, errors.New("runtime is unavailable")
	}
	defer generation.mu.RUnlock()
	return generation.handler.HandleInbound(ctx, message)
}

func (m *Manager) GateStats() gate.Stats {
	generation := m.acquire()
	if generation == nil {
		return gate.Stats{}
	}
	defer generation.mu.RUnlock()
	return generation.engine.GateStats()
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
	done := make(chan struct{})
	go func() { generation.mu.Lock(); close(done) }()
	if grace > 0 {
		select {
		case <-done:
		case <-time.After(grace):
			// The engine stays alive until in-flight requests release the generation.
			go func() { <-done; generation.engine.Shutdown(grace); generation.mu.Unlock() }()
			return
		}
	} else {
		<-done
	}
	generation.engine.Shutdown(grace)
	generation.mu.Unlock()
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
		next.snapshot = published
	}
	old := m.current.Swap(next)
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
	return published, nil
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
	withoutLLMCurrent, withoutLLMNext := current, next
	withoutLLMCurrent.LLM, withoutLLMNext.LLM = config.LLMConfig{}, config.LLMConfig{}
	if reflect.DeepEqual(withoutLLMCurrent, withoutLLMNext) {
		return ActionReplaceLLM
	}
	return ActionRestartEngine
}

func policyFrom(runtime config.RuntimeConfig) bot.Policy {
	g := runtime.Gate
	return bot.Policy{Name: runtime.Bot.Name, SystemPrompt: runtime.Bot.SystemPrompt, Model: runtime.LLM.Model, Gate: bot.GatePolicy{
		Enabled: g.Enabled, RequireMention: g.RequireMention, Silence: g.Silence,
		MaxCount: g.MaxCount, MaxWindow: g.MaxWindow, DedupTTL: g.DedupTTL,
		DedupMax: g.DedupMax, MaxBuffered: g.MaxBuffered, MaxTextLen: g.MaxTextLen,
		MaxConcurrent: g.MaxConcurrent,
	}}
}
