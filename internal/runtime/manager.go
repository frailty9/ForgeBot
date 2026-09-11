package runtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/soleapi/forgebot/internal/bot"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/gate"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
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
	ActionNone          Action = "none"
	ActionUpdateLog     Action = "update_log"
	ActionReplaceLLM    Action = "replace_llm"
	ActionReplaceSecret Action = "replace_secret"
	ActionRestartEngine Action = "restart_engine"
	ActionRestartBot    Action = "restart_bot"
)

type Status struct {
	State     State     `json:"state"`
	Action    Action    `json:"action"`
	Version   uint64    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	Error     string    `json:"error,omitempty"`
}

type ApplyResult struct {
	Action   Action          `json:"action"`
	Status   Status          `json:"status"`
	Snapshot config.Snapshot `json:"snapshot"`
}

type ClientFactory func(llm.ClientConfig) (llm.Client, error)
type Persist func() (config.Snapshot, error)

type Builder struct {
	Sender        platform.Sender
	Secrets       secret.Store
	ClientFactory ClientFactory
	ApplyLog      func(config.LogConfig)
}

func (b Builder) Build(ctx context.Context, snapshot config.Snapshot) (*bot.Engine, error) {
	return b.build(ctx, snapshot, "", "")
}

func (b Builder) build(ctx context.Context, snapshot config.Snapshot, secretRef, secretValue string) (*bot.Engine, error) {
	if b.Sender == nil {
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
	return bot.NewEngine(ctx, bot.Dependencies{Sender: b.Sender, LLM: client, Policy: policyFrom(snapshot.Runtime)}, nil)
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
	nextEngine, err := m.builder.build(ctx, current.snapshot, ref, value)
	if err != nil {
		m.setStatus(StateReady, ActionReplaceSecret, current.snapshot.Version, err.Error())
		return err
	}
	if err := activate(); err != nil {
		nextEngine.Shutdown(current.snapshot.Static.Server.ShutdownTimeout)
		m.setStatus(StateReady, ActionReplaceSecret, current.snapshot.Version, err.Error())
		return err
	}
	old := m.current.Swap(&generation{snapshot: current.snapshot, engine: nextEngine})
	m.setStatus(StateReady, ActionReplaceSecret, current.snapshot.Version, "")
	m.drain(old, current.snapshot.Static.Server.ShutdownTimeout)
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
	nextEngine, err := m.builder.build(ctx, current.snapshot, ref, "")
	if err != nil {
		m.setStatus(StateReady, ActionReplaceSecret, current.snapshot.Version, err.Error())
		return err
	}
	if err := remove(); err != nil {
		nextEngine.Shutdown(current.snapshot.Static.Server.ShutdownTimeout)
		m.setStatus(StateReady, ActionReplaceSecret, current.snapshot.Version, err.Error())
		return err
	}
	old := m.current.Swap(&generation{snapshot: current.snapshot, engine: nextEngine})
	m.setStatus(StateReady, ActionReplaceSecret, current.snapshot.Version, "")
	m.drain(old, current.snapshot.Static.Server.ShutdownTimeout)
	return nil
}

type Manager struct {
	applyMu  sync.Mutex
	statusMu sync.RWMutex
	current  atomic.Pointer[generation]
	builder  Builder
	status   Status
}

type generation struct {
	snapshot config.Snapshot
	engine   *bot.Engine
	mu       sync.RWMutex
}

func NewManager(ctx context.Context, snapshot config.Snapshot, builder Builder) (*Manager, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	engine, err := builder.Build(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	manager := &Manager{builder: builder, status: Status{State: StateReady, Action: ActionNone, Version: snapshot.Version, UpdatedAt: time.Now().UTC()}}
	manager.current.Store(&generation{snapshot: snapshot, engine: engine})
	return manager, nil
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
	generation.mu.RUnlock()
	return snapshot, status
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
	nextEngine, err := m.builder.Build(ctx, snapshot)
	if err != nil {
		m.setStatus(StateReady, action, current.snapshot.Version, err.Error())
		return ApplyResult{}, err
	}
	next := &generation{snapshot: snapshot, engine: nextEngine}
	old := m.current.Swap(next)
	m.setStatus(StateReady, action, snapshot.Version, "")
	m.drain(old, snapshot.Static.Server.ShutdownTimeout)
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
	nextEngine, err := m.builder.Build(ctx, candidate)
	if err != nil {
		m.setStatus(StateReady, action, current.snapshot.Version, err.Error())
		return ApplyResult{}, err
	}
	persisted, err := persist()
	if err != nil {
		nextEngine.Shutdown(current.snapshot.Static.Server.ShutdownTimeout)
		m.setStatus(StateReady, action, current.snapshot.Version, err.Error())
		return ApplyResult{}, err
	}
	if !reflect.DeepEqual(persisted.Static, current.snapshot.Static) || !reflect.DeepEqual(persisted.Runtime, runtimeConfig) || persisted.Version <= current.snapshot.Version {
		nextEngine.Shutdown(current.snapshot.Static.Server.ShutdownTimeout)
		err = errors.New("persisted runtime snapshot does not match candidate")
		m.setStatus(StateDegraded, action, current.snapshot.Version, err.Error())
		return ApplyResult{}, err
	}
	old := m.current.Swap(&generation{snapshot: persisted, engine: nextEngine})
	if m.builder.ApplyLog != nil {
		m.builder.ApplyLog(persisted.Runtime.Log)
	}
	m.setStatus(StateReady, action, persisted.Version, "")
	m.drain(old, persisted.Static.Server.ShutdownTimeout)
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
	nextEngine, err := m.builder.Build(ctx, current.snapshot)
	if err != nil {
		m.setStatus(StateReady, ActionRestartBot, current.snapshot.Version, err.Error())
		return fmt.Errorf("restart runtime (%s): %w", reason, err)
	}
	old := m.current.Swap(&generation{snapshot: current.snapshot, engine: nextEngine})
	m.setStatus(StateReady, ActionRestartBot, current.snapshot.Version, "")
	m.drain(old, current.snapshot.Static.Server.ShutdownTimeout)
	return nil
}

func (m *Manager) Shutdown(_ context.Context) error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	old := m.current.Swap(nil)
	if old == nil {
		return nil
	}
	m.drain(old, old.snapshot.Static.Server.ShutdownTimeout)
	m.setStatus(StateStopped, ActionNone, old.snapshot.Version, "")
	return nil
}

func (m *Manager) HandleInbound(ctx context.Context, message messaging.InboundMessage) (platform.HandleResult, error) {
	generation := m.acquire()
	if generation == nil {
		return platform.HandleResult{}, errors.New("runtime is unavailable")
	}
	defer generation.mu.RUnlock()
	return generation.engine.HandleInbound(ctx, message)
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
