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
	"github.com/soleapi/forgebot/internal/bot"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/gate"
	"github.com/soleapi/forgebot/internal/knowledge/markdown"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/notification"
	"github.com/soleapi/forgebot/internal/operations"
	"github.com/soleapi/forgebot/internal/platform"
	reflectionbuffer "github.com/soleapi/forgebot/internal/reflection/buffer"
	reflectionpipeline "github.com/soleapi/forgebot/internal/reflection/pipeline"
	reflectionreflector "github.com/soleapi/forgebot/internal/reflection/reflector"
	reflectionscanner "github.com/soleapi/forgebot/internal/reflection/scanner"
	searchcontract "github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/secret"
	"github.com/soleapi/forgebot/internal/tooling"
	"github.com/soleapi/forgebot/internal/tooling/searchdocs"
)

var (
	ErrStaticConfigChanged   = errors.New("static configuration requires a process restart")
	ErrReflectionDisabled    = errors.New("reflection is disabled")
	ErrReflectionUnavailable = errors.New("reflection scanner is unavailable")
)

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
	ActionReplaceSearch     Action = "replace_search"
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
	Paused     bool                       `json:"paused"`
	Connectors []platform.ConnectorStatus `json:"connectors,omitempty"`
	Search     searchcontract.Status      `json:"search"`
	Reflection ReflectionStatus           `json:"reflection"`
}

type ReflectionStatus struct {
	Enabled  bool                     `json:"enabled"`
	Active   bool                     `json:"active"`
	Buffer   reflectionbuffer.Stats   `json:"buffer"`
	Scanner  reflectionscanner.Stats  `json:"scanner"`
	Pipeline reflectionpipeline.Stats `json:"pipeline"`
}

type ReflectionScanResult struct {
	Processed int              `json:"processed"`
	Retried   int              `json:"retried"`
	Skipped   bool             `json:"skipped"`
	Status    ReflectionStatus `json:"status"`
}

type ApplyResult struct {
	Action   Action          `json:"action"`
	Status   Status          `json:"status"`
	Snapshot config.Snapshot `json:"snapshot"`
}

type ClientFactory func(llm.ClientConfig) (llm.Client, error)
type ConnectorFactory func(config.PlatformConfig) (platform.ConnectorSet, error)
type SearchFactory func(context.Context, config.SearchConfig) (searchcontract.Retriever, error)
type Persist func() (config.Snapshot, error)

type Builder struct {
	Sender            platform.Sender
	Secrets           secret.Store
	ClientFactory     ClientFactory
	ConnectorFactory  ConnectorFactory
	SearchFactory     SearchFactory
	ApplyLog          func(config.LogConfig)
	Notifications     notification.Publisher
	ReflectionFactory func(context.Context, config.Snapshot, llm.Client, searchcontract.Retriever) (*reflectionscanner.Scanner, error)
}

func (b Builder) Build(ctx context.Context, snapshot config.Snapshot) (*bot.Engine, error) {
	retriever, err := b.buildSearch(ctx, snapshot.Runtime.Search, nil)
	if err != nil {
		return nil, err
	}
	return b.build(ctx, snapshot, b.Sender, retriever, "", "")
}

func (b Builder) build(ctx context.Context, snapshot config.Snapshot, sender platform.Sender, retriever searchcontract.Retriever, secretRef, secretValue string) (*bot.Engine, error) {
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
	var executor tooling.Executor
	if snapshot.Runtime.Search.Enabled && retriever != nil {
		searchConfig := snapshot.Runtime.Search
		executor = tooling.NewRegistry(searchdocs.New(retriever, searchConfig.DefaultResults, searchConfig.MaxResults, searchConfig.MaxContextChars))
	}
	return bot.NewEngine(ctx, bot.Dependencies{Sender: sender, LLM: client, Retriever: retriever, Tools: executor, Policy: policyFrom(snapshot.Runtime)}, nil)
}

func (b Builder) buildSearch(ctx context.Context, cfg config.SearchConfig, current searchcontract.Retriever) (searchcontract.Retriever, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if current != nil {
		return current, nil
	}
	if b.SearchFactory == nil {
		return nil, errors.New("search factory is required when search is enabled")
	}
	retriever, err := b.SearchFactory(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("build search index: %w", err)
	}
	if retriever == nil {
		return nil, errors.New("search factory returned nil retriever")
	}
	return retriever, nil
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
	next, err := m.buildGeneration(ctx, current.snapshot, current.connectors, current.retriever, ref, value)
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
	next, err := m.buildGeneration(ctx, current.snapshot, current.connectors, current.retriever, ref, "")
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
	applyMu         sync.Mutex
	routeMu         sync.RWMutex
	statusMu        sync.RWMutex
	current         atomic.Pointer[generation]
	snapshot        atomic.Pointer[config.Snapshot]
	builder         Builder
	status          Status
	started         bool
	runCtx          context.Context
	errors          chan error
	operator        operations.Service
	paused          atomic.Bool
	reloadingSearch atomic.Bool
	reflection      *reflectionbuffer.Buffer
}

type generation struct {
	snapshot           config.Snapshot
	engine             *bot.Engine
	connectors         platform.ConnectorSet
	handler            *operations.Router
	retriever          searchcontract.Retriever
	admins             administration.Resolver
	reflection         *reflectionbuffer.Buffer
	reflectionEnabled  bool
	reflectionScanner  *reflectionscanner.Scanner
	reflectionPipeline *reflectionpipeline.Processor
	watchCancel        context.CancelFunc
	mu                 sync.RWMutex
}

func NewManager(ctx context.Context, snapshot config.Snapshot, builder Builder) (*Manager, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	manager := &Manager{builder: builder, errors: make(chan error, 8), status: Status{State: StateReady, Action: ActionNone, Version: snapshot.Version, UpdatedAt: time.Now().UTC()}}
	generation, err := manager.buildGeneration(ctx, snapshot, nil, nil, "", "")
	if err != nil {
		return nil, err
	}
	manager.current.Store(generation)
	manager.reflection = generation.reflection
	manager.storeSnapshot(snapshot)
	return manager, nil
}

func (m *Manager) buildGeneration(ctx context.Context, snapshot config.Snapshot, connectors platform.ConnectorSet, retriever searchcontract.Retriever, secretRef, secretValue string) (*generation, error) {
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
	retriever, err := m.builder.buildSearch(ctx, snapshot.Runtime.Search, retriever)
	if err != nil {
		return nil, err
	}
	engine, err := m.builder.build(ctx, snapshot, sender, retriever, secretRef, secretValue)
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
	reflection, err := m.ensureReflectionBuffer(snapshot.Runtime.Reflection)
	if err != nil {
		engine.Shutdown(snapshot.Static.Server.ShutdownTimeout)
		return nil, fmt.Errorf("build reflection buffer: %w", err)
	}
	var reflectionScanner *reflectionscanner.Scanner
	var reflectionPipeline *reflectionpipeline.Processor
	if snapshot.Runtime.Reflection.Enabled && reflection != nil {
		client, clientErr := m.buildReflectionClient(ctx, snapshot, secretRef, secretValue)
		if clientErr != nil {
			engine.Shutdown(snapshot.Static.Server.ShutdownTimeout)
			return nil, clientErr
		}
		if client != nil {
			reflectionScanner, reflectionPipeline, err = m.buildReflectionScanner(ctx, snapshot, reflection, client, retriever)
		}
		if err != nil {
			engine.Shutdown(snapshot.Static.Server.ShutdownTimeout)
			return nil, err
		}
	}
	return &generation{
		snapshot: snapshot, engine: engine, connectors: connectors, handler: router,
		retriever: retriever, admins: administration.New(adminIdentities), reflection: reflection,
		reflectionEnabled:  snapshot.Runtime.Reflection.Enabled,
		reflectionScanner:  reflectionScanner,
		reflectionPipeline: reflectionPipeline,
	}, nil
}

func (m *Manager) buildReflectionScanner(ctx context.Context, snapshot config.Snapshot, b *reflectionbuffer.Buffer, client llm.Client, retriever searchcontract.Retriever) (*reflectionscanner.Scanner, *reflectionpipeline.Processor, error) {
	if m.builder.ReflectionFactory != nil {
		scanner, err := m.builder.ReflectionFactory(ctx, snapshot, client, retriever)
		return scanner, nil, err
	}
	if retriever == nil || strings.TrimSpace(snapshot.Runtime.Search.DocumentRoot) == "" {
		return nil, nil, errors.New("reflection requires an active search retriever")
	}
	r, err := reflectionreflector.New(client, snapshot.Runtime.LLM.Model)
	if err != nil {
		return nil, nil, err
	}
	publisher, err := markdown.New(markdown.Config{Root: snapshot.Runtime.Search.DocumentRoot, ExtraDirectory: snapshot.Runtime.Reflection.ExtraDirectory, MaxBytes: snapshot.Runtime.Search.MaxFileBytes})
	if err != nil {
		return nil, nil, err
	}
	processor, err := reflectionpipeline.New(reflectionpipeline.Config{Reflector: r, Publisher: publisher, MinConfidence: snapshot.Runtime.Reflection.MinConfidence, Reload: func(reloadCtx context.Context) error { _, reloadErr := m.ReloadSearch(reloadCtx, ""); return reloadErr }})
	if err != nil {
		return nil, nil, err
	}
	scanner, err := reflectionscanner.New(reflectionscanner.Config{Interval: snapshot.Runtime.Reflection.ScanInterval, MaxSessions: snapshot.Runtime.Reflection.MaxPairsPerScan, Buffer: b, ProcessBatch: processor.ProcessBatch})
	return scanner, processor, err
}

func (m *Manager) buildReflectionClient(ctx context.Context, snapshot config.Snapshot, secretRef, secretValue string) (llm.Client, error) {
	if strings.TrimSpace(snapshot.Runtime.LLM.Model) == "" {
		return nil, nil
	}
	apiKey := secretValue
	var err error
	if secretRef != snapshot.Runtime.LLM.APIKeyRef && m.builder.Secrets != nil {
		apiKey, err = m.builder.Secrets.Get(ctx, snapshot.Runtime.LLM.APIKeyRef)
	}
	if err != nil && !errors.Is(err, secret.ErrNotFound) {
		return nil, err
	}
	if apiKey == "" {
		return nil, nil
	}
	factory := m.builder.ClientFactory
	if factory == nil {
		factory = func(cfg llm.ClientConfig) (llm.Client, error) { return llm.NewClient(cfg) }
	}
	return factory(snapshot.Runtime.LLM.ClientConfig(apiKey))
}

func (m *Manager) ensureReflectionBuffer(cfg config.ReflectionConfig) (*reflectionbuffer.Buffer, error) {
	if m.reflection != nil {
		return m.reflection, nil
	}
	if !cfg.Enabled {
		return nil, nil
	}
	b, err := reflectionbuffer.New(reflectionbuffer.Config{
		ConversationIDs: cfg.ConversationIDs, MessageTTL: cfg.MessageTTL,
		QuietWindow: cfg.QuietWindow, MaxSessionDuration: cfg.MaxSessionDuration,
		MaxMessagesPerConversation: cfg.MaxMessagesPerConversation,
		MaxMessagesTotal:           cfg.MaxMessagesTotal, MaxMessageChars: cfg.MaxMessageChars,
	})
	if err != nil {
		return nil, err
	}
	return b, nil
}

func reflectionBufferConfig(cfg config.ReflectionConfig) reflectionbuffer.Config {
	return reflectionbuffer.Config{
		ConversationIDs: cfg.ConversationIDs, MessageTTL: cfg.MessageTTL,
		QuietWindow: cfg.QuietWindow, MaxSessionDuration: cfg.MaxSessionDuration,
		MaxMessagesPerConversation: cfg.MaxMessagesPerConversation,
		MaxMessagesTotal:           cfg.MaxMessagesTotal, MaxMessageChars: cfg.MaxMessageChars,
	}
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
	snapshot := *m.snapshot.Load()
	if generation.connectors != nil {
		status.Connectors = generation.connectors.Status()
	}
	if generation.retriever != nil {
		status.Search = generation.retriever.Status()
	} else {
		status.Search = searchcontract.Status{Enabled: false, State: searchcontract.StateDisabled}
	}
	status.Reflection = m.reflectionStatus(generation)
	return snapshot, status
}

func (m *Manager) reflectionStatus(generation *generation) ReflectionStatus {
	status := ReflectionStatus{Enabled: generation.reflectionEnabled, Active: generation.reflectionEnabled && generation.reflectionScanner != nil}
	if generation.reflection != nil {
		status.Buffer = generation.reflection.Stats(time.Now().UTC())
	}
	if generation.reflectionScanner != nil {
		status.Scanner = generation.reflectionScanner.Stats()
	}
	if generation.reflectionPipeline != nil {
		status.Pipeline = generation.reflectionPipeline.Stats()
	}
	return status
}

func (m *Manager) ScanReflection(ctx context.Context) (ReflectionScanResult, error) {
	generation := m.acquire()
	if generation == nil {
		return ReflectionScanResult{}, errors.New("runtime is unavailable")
	}
	if !generation.reflectionEnabled {
		generation.mu.RUnlock()
		return ReflectionScanResult{}, ErrReflectionDisabled
	}
	scanner := generation.reflectionScanner
	generation.mu.RUnlock()
	if scanner == nil {
		return ReflectionScanResult{}, ErrReflectionUnavailable
	}
	processed, retried, skipped := scanner.ScanOnce(ctx)
	current := m.acquire()
	if current == nil {
		return ReflectionScanResult{Processed: processed, Retried: retried, Skipped: skipped}, nil
	}
	status := m.reflectionStatus(current)
	current.mu.RUnlock()
	return ReflectionScanResult{Processed: processed, Retried: retried, Skipped: skipped, Status: status}, nil
}

func (m *Manager) storeSnapshot(snapshot config.Snapshot) {
	copy := snapshot
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
	if current.reflectionScanner != nil {
		if err := current.reflectionScanner.Start(ctx); err != nil {
			if current.connectors != nil {
				_ = current.connectors.Stop(ctx)
			}
			return err
		}
	}
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
	if !reflect.DeepEqual(current.snapshot.Runtime.Platforms, snapshot.Runtime.Platforms) {
		connectors = nil
	}
	retriever := current.retriever
	if !reflect.DeepEqual(current.snapshot.Runtime.Search, snapshot.Runtime.Search) {
		retriever = nil
	}
	next, err := m.buildGeneration(ctx, snapshot, connectors, retriever, "", "")
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
	retriever := current.retriever
	if !reflect.DeepEqual(current.snapshot.Runtime.Search, runtimeConfig.Search) {
		retriever = nil
	}
	next, err := m.buildGeneration(ctx, candidate, connectors, retriever, "", "")
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
	next, err := m.buildGeneration(ctx, current.snapshot, nil, current.retriever, "", "")
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
	if old.reflectionScanner != nil {
		old.reflectionScanner.Cancel()
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

func (m *Manager) ReloadSearch(ctx context.Context, source string) (searchcontract.ReloadResult, error) {
	if !m.reloadingSearch.CompareAndSwap(false, true) {
		return searchcontract.ReloadResult{}, searchcontract.ErrReloadInProgress
	}
	defer m.reloadingSearch.Store(false)
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	current := m.current.Load()
	if current == nil {
		return searchcontract.ReloadResult{}, errors.New("runtime is stopped")
	}
	if !current.snapshot.Runtime.Search.Enabled || current.retriever == nil {
		return searchcontract.ReloadResult{}, searchcontract.ErrDisabled
	}
	result, err := current.retriever.Reload(ctx)
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
	if generation.reflectionEnabled && generation.reflection != nil {
		generation.reflection.Observe(message)
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
	previousReflection := m.reflection
	connectorsChanged := current.connectors != next.connectors
	if connectorsChanged && m.started {
		m.routeMu.Lock()
		defer m.routeMu.Unlock()
		stopCtx, cancel := context.WithTimeout(context.Background(), current.snapshot.Static.Server.ShutdownTimeout)
		stopErr := current.connectors.Stop(stopCtx)
		cancel()
		if stopErr != nil {
			m.reflection = previousReflection
			next.engine.Shutdown(current.snapshot.Static.Server.ShutdownTimeout)
			return config.Snapshot{}, stopErr
		}
		if err := next.connectors.Start(m.runCtx, m); err != nil {
			restoreErr := current.connectors.Start(m.runCtx, m)
			next.engine.Shutdown(current.snapshot.Static.Server.ShutdownTimeout)
			if restoreErr != nil {
				m.reflection = previousReflection
				return config.Snapshot{}, errors.Join(err, fmt.Errorf("restore previous connectors: %w", restoreErr))
			}
			m.reflection = previousReflection
			return config.Snapshot{}, err
		}
	}
	published := next.snapshot
	if persist != nil {
		var err error
		published, err = persist()
		if err != nil {
			m.reflection = previousReflection
			m.rollbackConnectors(current, next, connectorsChanged)
			next.engine.Shutdown(current.snapshot.Static.Server.ShutdownTimeout)
			return config.Snapshot{}, err
		}
		next.snapshot = published
	}
	if next.reflectionEnabled && m.reflection != nil {
		if err := next.reflection.UpdateConfig(reflectionBufferConfig(next.snapshot.Runtime.Reflection)); err != nil {
			m.reflection = previousReflection
			m.rollbackConnectors(current, next, connectorsChanged)
			next.engine.Shutdown(current.snapshot.Static.Server.ShutdownTimeout)
			return config.Snapshot{}, err
		}
	}
	if next.reflection != nil {
		m.reflection = next.reflection
	}
	old := m.current.Swap(next)
	m.storeSnapshot(next.snapshot)
	if m.started {
		if old.reflectionScanner != nil {
			old.reflectionScanner.Cancel()
		}
		if next.reflectionScanner != nil {
			if err := next.reflectionScanner.Start(m.runCtx); err != nil {
				m.notify(notification.Event{Level: notification.LevelError, Component: "reflection", Code: "scanner_start_failed", Message: err.Error()})
			}
		}
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
	if !reflect.DeepEqual(current.Search, next.Search) {
		return ActionReplaceSearch
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
	s := runtime.Search
	return bot.Policy{Name: runtime.Bot.Name, SystemPrompt: runtime.Bot.SystemPrompt, Model: runtime.LLM.Model, Search: bot.SearchPolicy{
		Enabled: s.Enabled, DefaultResults: s.DefaultResults, MaxResults: s.MaxResults,
		MaxQueriesPerTurn: s.MaxQueriesPerTurn, MaxContextChars: s.MaxContextChars,
	}, Gate: bot.GatePolicy{
		Enabled: g.Enabled, RequireMention: g.RequireMention, Silence: g.Silence,
		MaxCount: g.MaxCount, MaxWindow: g.MaxWindow, DedupTTL: g.DedupTTL,
		DedupMax: g.DedupMax, MaxBuffered: g.MaxBuffered, MaxTextLen: g.MaxTextLen,
		MaxConcurrent: g.MaxConcurrent,
	}}
}
