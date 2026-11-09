// Package modules builds whole feature modules into immutable hook chains.
package modules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/history"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/tooling"
)

type Descriptor struct {
	ID             string
	DefaultEnabled bool
	Hooks          []hooks.Stage
	Provides       []capability.ID
	Requires       []capability.ID
	Registrations  []RegistrationKind
}
type Factory interface {
	Descriptor() Descriptor
	ValidateOptions(json.RawMessage) error
	PublicOptions(json.RawMessage) (any, error)
	Build(context.Context, json.RawMessage) (Instance, error)
}
type Instance interface {
	Registrations() hooks.Registrations
	Close(context.Context) error
}
type entry struct {
	descriptor Descriptor
	factory    Factory
	source     hooks.Source
	order      int
	failure    hooks.FailurePolicy
}
type Registry struct{ entries map[string]entry }

func NewRegistry() *Registry { return &Registry{entries: make(map[string]entry)} }

// Register is a composition-time host operation, not a plugin registration API.
func (r *Registry) Register(factory Factory, source hooks.Source, order int, failure hooks.FailurePolicy) error {
	if factory == nil {
		return errors.New("nil module factory")
	}
	descriptor := factory.Descriptor()
	if !config.ValidModuleID(descriptor.ID) {
		return errors.New("invalid module id")
	}
	if _, exists := r.entries[descriptor.ID]; exists {
		return errors.New("duplicate module id")
	}
	if source != hooks.Native && source != hooks.External {
		return errors.New("invalid module source")
	}
	if failure == "" {
		failure = hooks.Continue
	}
	if failure != hooks.Continue && failure != hooks.Abort {
		return errors.New("invalid module failure policy")
	}
	seen := map[hooks.Stage]bool{}
	for _, stage := range descriptor.Hooks {
		if stage != hooks.AgentInput && stage != hooks.BeforeTool && stage != hooks.BeforeReply {
			return fmt.Errorf("unsupported module registration %q", stage)
		}
		if seen[stage] {
			return errors.New("duplicate declared hook")
		}
		seen[stage] = true
	}
	seenCapabilities := make(map[capability.ID]bool)
	for _, id := range append(slices.Clone(descriptor.Provides), descriptor.Requires...) {
		if !id.Valid() || seenCapabilities[id] {
			return fmt.Errorf("invalid or duplicate capability %q", id)
		}
		seenCapabilities[id] = true
	}
	seenKinds := make(map[RegistrationKind]bool)
	for _, kind := range descriptor.Registrations {
		switch kind {
		case RegistrationTools, RegistrationCommands, RegistrationObservers,
			RegistrationParticipants, RegistrationJobs, RegistrationStatus,
			RegistrationConnectorProviders, RegistrationGroupEvents, RegistrationJoinReview, RegistrationHistory:
		default:
			return fmt.Errorf("unsupported registration kind %q", kind)
		}
		if seenKinds[kind] {
			return fmt.Errorf("duplicate registration kind %q", kind)
		}
		seenKinds[kind] = true
	}
	descriptor.Hooks = slices.Clone(descriptor.Hooks)
	descriptor.Provides = slices.Clone(descriptor.Provides)
	descriptor.Requires = slices.Clone(descriptor.Requires)
	descriptor.Registrations = slices.Clone(descriptor.Registrations)
	r.entries[descriptor.ID] = entry{descriptor, factory, source, order, failure}
	return nil
}

// ConnectorProvidersChanged reports whether a configuration update changes a
// module that contributes protocol implementations. Runtime uses this metadata
// rather than hard-coding native module IDs when choosing a restart action.
func (r *Registry) ConnectorProvidersChanged(current, next map[string]config.ModuleConfig) bool {
	if r == nil {
		return false
	}
	for _, id := range r.ids() {
		entry := r.entries[id]
		if !slices.Contains(entry.descriptor.Registrations, RegistrationConnectorProviders) {
			continue
		}
		before, after := current[id], next[id]
		if enabled(entry, before) != enabled(entry, after) {
			return true
		}
		if enabled(entry, before) && !reflect.DeepEqual(before.Options, after.Options) {
			return true
		}
	}
	return false
}

// Clone freezes a private directory for a runtime. Factories must be immutable.
func (r *Registry) Clone() *Registry {
	copy := NewRegistry()
	if r != nil {
		for id, value := range r.entries {
			value.descriptor.Hooks = slices.Clone(value.descriptor.Hooks)
			value.descriptor.Provides = slices.Clone(value.descriptor.Provides)
			value.descriptor.Requires = slices.Clone(value.descriptor.Requires)
			value.descriptor.Registrations = slices.Clone(value.descriptor.Registrations)
			copy.entries[id] = value
		}
	}
	return copy
}

func (r *Registry) ids() []string {
	var ids []string
	if r != nil {
		for id := range r.entries {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (r *Registry) Validate(entries map[string]config.ModuleConfig) error {
	if err := config.ValidateModules(entries); err != nil {
		return err
	}
	for id := range entries {
		if r == nil {
			return fmt.Errorf("unknown module %q", id)
		}
		if _, exists := r.entries[id]; !exists {
			return fmt.Errorf("unknown module %q", id)
		}
	}
	enabledCount := 0
	for _, id := range r.ids() {
		entry := r.entries[id]
		cfg := entries[id]
		if err := entry.factory.ValidateOptions(slices.Clone(cfg.Options)); err != nil {
			return fmt.Errorf("module %s: %w", id, err)
		}
		if enabled(entry, cfg) {
			enabledCount++
		}
	}
	if enabledCount > config.MaxModules {
		return errors.New("too many enabled modules")
	}
	return nil
}

// ValidateRuntime 在模块自身校验之外，检查按逻辑群配置的模块只引用已绑定的群。
// 关闭的模块同样检查，避免重新开启时才发现写错的群 ID。
func (r *Registry) ValidateRuntime(entries map[string]config.ModuleConfig, platforms config.PlatformConfig) error {
	if err := r.Validate(entries); err != nil {
		return err
	}
	bound := make(map[string]bool, len(platforms.Groups))
	for _, binding := range platforms.Groups {
		bound[binding.ID] = true
	}
	for _, id := range r.ids() {
		referencer, ok := r.entries[id].factory.(GroupReferencer)
		cfg, configured := entries[id]
		if !ok || !configured {
			continue
		}
		groups, err := referencer.ReferencedGroups(slices.Clone(cfg.Options))
		if err != nil {
			return fmt.Errorf("module %s: %w", id, err)
		}
		for _, conversationID := range groups {
			if !bound[conversationID] {
				return fmt.Errorf("module %s references unbound group %q; add it to platforms.groups first", id, conversationID)
			}
		}
	}
	return nil
}

func enabled(entry entry, cfg config.ModuleConfig) bool {
	if cfg.Enabled != nil {
		return *cfg.Enabled
	}
	return entry.descriptor.DefaultEnabled
}

func (r *Registry) Public(entries map[string]config.ModuleConfig) (map[string]any, error) {
	result := make(map[string]any)
	for _, id := range r.ids() {
		entry := r.entries[id]
		cfg := entries[id]
		options, err := entry.factory.PublicOptions(slices.Clone(cfg.Options))
		if err != nil {
			return nil, err
		}
		result[id] = map[string]any{"enabled": enabled(entry, cfg), "options": options}
	}
	return result, nil
}

type Status struct {
	ID      string              `json:"id"`
	Source  hooks.Source        `json:"source"`
	Enabled bool                `json:"enabled"`
	State   string              `json:"state"`
	Hooks   []hooks.Description `json:"hooks"`
	Details any                 `json:"details,omitempty"`
	Error   string              `json:"error,omitempty"`
}
type Set struct {
	groupEvents        []GroupEventRegistration
	joinReview         *JoinReviewRegistration
	chain              *hooks.Chain
	capabilities       capability.Values
	tools              []ToolRegistration
	commands           []CommandRegistration
	observers          []ObserverRegistration
	participants       []ParticipantRegistration
	history            []history.Subscriber
	jobs               []BackgroundJob
	statusProviders    map[string]func() any
	connectorProviders map[string]ProviderRegistration
	instances          []Instance
	instanceIDs        []string
	status             []Status
	mu                 sync.RWMutex
	startOnce          sync.Once
	retireOnce         sync.Once
	jobCancel          context.CancelFunc
	jobWG              sync.WaitGroup
	jobErr             error
	closeOnce          sync.Once
	closeErr           error
}

func (r *Registry) orderedIDs(entries map[string]config.ModuleConfig, initial capability.Values) ([]string, error) {
	ids := r.ids()
	enabledIDs := make(map[string]bool, len(ids))
	providers := make(map[capability.ID]string)
	for _, id := range ids {
		entry := r.entries[id]
		if !enabled(entry, entries[id]) {
			continue
		}
		enabledIDs[id] = true
		for _, provided := range entry.descriptor.Provides {
			if initial.Has(provided) {
				return nil, fmt.Errorf("capability %q is already provided by the host", provided)
			}
			if previous, exists := providers[provided]; exists {
				return nil, fmt.Errorf("capability %q provided by both %s and %s", provided, previous, id)
			}
			providers[provided] = id
		}
	}
	state := make(map[string]uint8, len(ids))
	ordered := make([]string, 0, len(ids))
	var visit func(string) error
	visit = func(id string) error {
		if !enabledIDs[id] {
			return nil
		}
		switch state[id] {
		case 1:
			return fmt.Errorf("capability dependency cycle at module %s", id)
		case 2:
			return nil
		}
		state[id] = 1
		for _, required := range r.entries[id].descriptor.Requires {
			if initial.Has(required) {
				continue
			}
			provider, ok := providers[required]
			if !ok {
				return fmt.Errorf("module %s requires unavailable capability %q", id, required)
			}
			if err := visit(provider); err != nil {
				return err
			}
		}
		state[id] = 2
		ordered = append(ordered, id)
		return nil
	}
	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	for _, id := range ids {
		if !enabledIDs[id] {
			ordered = append(ordered, id)
		}
	}
	return ordered, nil
}

func (r *Registry) Build(ctx context.Context, entries map[string]config.ModuleConfig, logger *slog.Logger) (result *Set, err error) {
	return r.BuildWithCapabilities(ctx, entries, logger, capability.NewValues())
}

// BuildConnectorProviders prepares only modules that declare connector
// providers. It is used by validation and public configuration rendering, so
// those paths share provider rules without building unrelated indexes, jobs,
// or message hooks.
func (r *Registry) BuildConnectorProviders(ctx context.Context, entries map[string]config.ModuleConfig, logger *slog.Logger) (*Set, error) {
	if err := r.Validate(entries); err != nil {
		return nil, err
	}
	onlyProviders := make(map[string]config.ModuleConfig, len(r.entries))
	for _, id := range r.ids() {
		entry := r.entries[id]
		cfg := entries[id]
		cfg.Options = slices.Clone(cfg.Options)
		if !slices.Contains(entry.descriptor.Registrations, RegistrationConnectorProviders) {
			disabled := false
			cfg.Enabled = &disabled
		}
		onlyProviders[id] = cfg
	}
	return r.BuildWithCapabilities(ctx, onlyProviders, logger, capability.NewValues())
}

func (r *Registry) BuildWithCapabilities(ctx context.Context, entries map[string]config.ModuleConfig, logger *slog.Logger, initial capability.Values) (result *Set, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = r.Validate(entries); err != nil {
		return nil, err
	}
	orderedIDs, err := r.orderedIDs(entries, initial)
	if err != nil {
		return nil, err
	}
	set := &Set{
		status: []Status{}, capabilities: initial.Clone(), statusProviders: make(map[string]func() any),
		connectorProviders: make(map[string]ProviderRegistration),
	}
	defer func() {
		if err != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err = errors.Join(err, set.Close(closeCtx))
		}
	}()
	var mounts []hooks.Mount
	for _, id := range orderedIDs {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		entry := r.entries[id]
		cfg := entries[id]
		status := Status{ID: id, Source: entry.source, Enabled: enabled(entry, cfg), State: "disabled", Hooks: []hooks.Description{}}
		if status.Enabled {
			var instance Instance
			var buildErr error
			if contextual, ok := entry.factory.(ContextFactory); ok {
				instance, buildErr = contextual.BuildWithContext(ctx, BuildContext{Capabilities: set.capabilities.View()}, slices.Clone(cfg.Options))
			} else {
				instance, buildErr = entry.factory.Build(ctx, slices.Clone(cfg.Options))
			}
			if instance != nil {
				set.instances = append(set.instances, instance)
				set.instanceIDs = append(set.instanceIDs, id)
			}
			if buildErr != nil {
				return nil, fmt.Errorf("build module %s: %w", id, buildErr)
			}
			if instance == nil {
				return nil, fmt.Errorf("module %s returned nil instance", id)
			}
			registrations := instance.Registrations()
			status.Hooks = hooks.Describe(registrations)
			for _, hook := range status.Hooks {
				if !slices.Contains(entry.descriptor.Hooks, hook.Hook) {
					return nil, fmt.Errorf("module %s registered undeclared hook %s", id, hook.Hook)
				}
			}
			mounts = append(mounts, hooks.Mount{ModuleID: id, Source: entry.source, Order: entry.order, Failure: entry.failure, Registrations: registrations})
			if extended, ok := instance.(ExtendedProvider); ok {
				extra := extended.ExtendedRegistrations()
				if err := validateExtended(entry.descriptor, extra); err != nil {
					return nil, fmt.Errorf("module %s: %w", id, err)
				}
				set.tools = append(set.tools, extra.Tools...)
				set.commands = append(set.commands, extra.Commands...)
				set.observers = append(set.observers, extra.Observers...)
				set.participants = append(set.participants, extra.Participants...)
				for _, subscriber := range extra.History {
					subscriber.ID = id + "/" + subscriber.ID
					set.history = append(set.history, subscriber)
				}
				if extra.JoinReview != nil {
					if set.joinReview != nil {
						return nil, errors.New("only one module may provide join review")
					}
					set.joinReview = extra.JoinReview
				}
				for _, registration := range extra.GroupEvents {
					registration.ID = id + "/" + registration.ID
					set.groupEvents = append(set.groupEvents, registration)
				}
				set.jobs = append(set.jobs, extra.Jobs...)
				for _, provider := range extra.ConnectorProviders {
					typeName := ""
					if provider != nil {
						typeName = provider.Type()
					}
					if provider == nil || !platform.ValidConnectorType(typeName) {
						return nil, errors.New("invalid connector provider registration")
					}
					if previous, exists := set.connectorProviders[typeName]; exists {
						return nil, fmt.Errorf("connector provider type %q registered by both %s and %s", typeName, previous.ModuleID, id)
					}
					set.connectorProviders[typeName] = ProviderRegistration{
						ModuleID: id, Provider: provider, ModuleOptions: slices.Clone(cfg.Options),
					}
				}
				if extra.Status != nil {
					set.statusProviders[id] = extra.Status
				}
				for _, provided := range extra.Capabilities {
					if err := set.capabilities.Provide(provided.ID, provided.Value); err != nil {
						return nil, fmt.Errorf("module %s: %w", id, err)
					}
				}
			}
			status.State = "prepared"
		}
		set.status = append(set.status, status)
	}
	set.chain, err = hooks.Compile(mounts, logger)
	if err != nil {
		return nil, err
	}
	if err := validateToolAndCommandNames(set.tools, set.commands); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return set, nil
}

func validateExtended(descriptor Descriptor, registrations ExtendedRegistrations) error {
	allowed := make(map[RegistrationKind]bool, len(descriptor.Registrations))
	for _, kind := range descriptor.Registrations {
		allowed[kind] = true
	}
	if registrations.JoinReview != nil && (!allowed[RegistrationJoinReview] || registrations.JoinReview.Policy == nil) {
		return errors.New("returned undeclared or invalid join review registration")
	}
	if len(registrations.GroupEvents) > 0 && !allowed[RegistrationGroupEvents] {
		return errors.New("returned undeclared group event registrations")
	}
	seenEvents := map[string]bool{}
	for _, event := range registrations.GroupEvents {
		if strings.TrimSpace(event.ID) == "" || event.Handle == nil || seenEvents[event.ID] {
			return errors.New("invalid or duplicate group event registration")
		}
		seenEvents[event.ID] = true
	}
	if len(registrations.Tools) > 0 && !allowed[RegistrationTools] {
		return errors.New("returned undeclared tool registrations")
	}
	if len(registrations.Commands) > 0 && !allowed[RegistrationCommands] {
		return errors.New("returned undeclared command registrations")
	}
	if len(registrations.Observers) > 0 && !allowed[RegistrationObservers] {
		return errors.New("returned undeclared observer registrations")
	}
	if len(registrations.Participants) > 0 && !allowed[RegistrationParticipants] {
		return errors.New("returned undeclared participant registrations")
	}
	if len(registrations.History) > 0 && !allowed[RegistrationHistory] {
		return errors.New("returned undeclared history registrations")
	}
	for _, subscriber := range registrations.History {
		if strings.TrimSpace(subscriber.ID) == "" || (subscriber.Appended == nil && subscriber.Evicted == nil) {
			return errors.New("invalid history subscriber registration")
		}
	}
	if len(registrations.Jobs) > 0 && !allowed[RegistrationJobs] {
		return errors.New("returned undeclared job registrations")
	}
	for _, job := range registrations.Jobs {
		if strings.TrimSpace(job.ID) == "" || job.Run == nil {
			return errors.New("invalid background job registration")
		}
	}
	if registrations.Status != nil && !allowed[RegistrationStatus] {
		return errors.New("returned undeclared status registration")
	}
	if len(registrations.ConnectorProviders) > 0 && !allowed[RegistrationConnectorProviders] {
		return errors.New("returned undeclared connector provider registrations")
	}
	declared := make(map[capability.ID]bool, len(descriptor.Provides))
	for _, id := range descriptor.Provides {
		declared[id] = true
	}
	for _, provided := range registrations.Capabilities {
		if !declared[provided.ID] {
			return fmt.Errorf("returned undeclared capability %q", provided.ID)
		}
	}
	return nil
}

func validateToolAndCommandNames(tools []ToolRegistration, commands []CommandRegistration) error {
	seenTools := make(map[string]string, len(tools))
	for _, registration := range tools {
		if strings.TrimSpace(registration.ID) == "" || registration.Tool == nil || registration.MaxCalls <= 0 {
			return errors.New("nil tool registration")
		}
		name := strings.TrimSpace(registration.Tool.Definition().Name)
		if name == "" {
			return errors.New("tool registration has empty name")
		}
		if previous, exists := seenTools[name]; exists {
			return fmt.Errorf("tool %q is registered more than once (%s and %s)", name, previous, registration.ID)
		}
		seenTools[name] = registration.ID
	}
	seenCommands := make(map[string]string, len(commands))
	for _, registration := range commands {
		name := strings.TrimSpace(strings.ToLower(registration.Name))
		if strings.TrimSpace(registration.ID) == "" || name == "" || strings.HasPrefix(name, "/") || registration.Handle == nil {
			return errors.New("invalid command registration")
		}
		if previous, exists := seenCommands[name]; exists {
			return fmt.Errorf("command %q is registered more than once (%s and %s)", name, previous, registration.ID)
		}
		seenCommands[name] = registration.ID
	}
	return nil
}

func (s *Set) Chain() *hooks.Chain             { return s.chain }
func (s *Set) Capabilities() capability.Values { return s.capabilities.Clone() }
func (s *Set) Tools() []ToolRegistration       { return slices.Clone(s.tools) }
func (s *Set) ToolExecutor() tooling.Executor {
	tools := make([]tooling.Tool, 0, len(s.tools))
	for _, registration := range s.tools {
		if registration.Tool != nil {
			tools = append(tools, registration.Tool)
		}
	}
	return tooling.NewRegistry(tools...)
}
func (s *Set) ToolBudgets() map[string]int {
	result := make(map[string]int, len(s.tools))
	for _, registration := range s.tools {
		if registration.Tool != nil {
			result[registration.Tool.Definition().Name] = registration.MaxCalls
		}
	}
	return result
}
func (s *Set) Commands() []CommandRegistration       { return slices.Clone(s.commands) }
func (s *Set) GroupEvents() []GroupEventRegistration { return slices.Clone(s.groupEvents) }

// JoinReviewPolicy 返回本运行代审批模块为该群提供的规则；模块未启用或群不在
// 其配置中时返回 false，表示宿主不处理该群的入群申请。
func (s *Set) JoinReviewPolicy(conversationID string) (group.Policy, bool) {
	if s == nil || s.joinReview == nil {
		return group.Policy{}, false
	}
	return s.joinReview.Policy(conversationID)
}
func (s *Set) Observers() []ObserverRegistration       { return slices.Clone(s.observers) }
func (s *Set) Participants() []ParticipantRegistration { return slices.Clone(s.participants) }

// HistorySubscribers 是这一代模块对近期消息容器的订阅，宿主在这一代生效时整体替换。
func (s *Set) HistorySubscribers() []history.Subscriber { return slices.Clone(s.history) }
func (s *Set) Jobs() []BackgroundJob                    { return slices.Clone(s.jobs) }

// ConnectorProvider returns an independently owned registration for one type.
// The provider itself is immutable process code; only its option bytes are
// copied to prevent callers from mutating a generation's configuration.
func (s *Set) ConnectorProvider(typeName string) (ProviderRegistration, bool) {
	if s == nil {
		return ProviderRegistration{}, false
	}
	registration, ok := s.connectorProviders[typeName]
	if !ok {
		return ProviderRegistration{}, false
	}
	registration.ModuleOptions = slices.Clone(registration.ModuleOptions)
	return registration, true
}

// ConnectorProviderTypes lists registered provider types in stable order.
func (s *Set) ConnectorProviderTypes() []string {
	if s == nil {
		return nil
	}
	types := make([]string, 0, len(s.connectorProviders))
	for typeName := range s.connectorProviders {
		types = append(types, typeName)
	}
	sort.Strings(types)
	return types
}

// Start activates optional module jobs after the Set has been published.
func (s *Set) Start(ctx context.Context) {
	s.startOnce.Do(func() {
		jobCtx, cancel := context.WithCancel(ctx)
		s.mu.Lock()
		s.jobCancel = cancel
		s.mu.Unlock()
		for _, instance := range s.instances {
			if starter, ok := instance.(Starter); ok {
				starter.Start(jobCtx)
			}
		}
		for _, job := range s.jobs {
			job := job
			s.jobWG.Add(1)
			go func() {
				defer s.jobWG.Done()
				if err := job.Run(jobCtx); err != nil && !errors.Is(err, context.Canceled) {
					s.mu.Lock()
					s.jobErr = errors.Join(s.jobErr, fmt.Errorf("job %s: %w", job.ID, err))
					s.mu.Unlock()
				}
			}()
		}
	})
	s.Mark("active")
}

// Retire asks optional modules to stop accepting new asynchronous work.
func (s *Set) Retire() {
	s.retireOnce.Do(func() {
		s.mu.Lock()
		cancel := s.jobCancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		for index := len(s.instances) - 1; index >= 0; index-- {
			if retirer, ok := s.instances[index].(Retirer); ok {
				retirer.Retire()
			}
		}
	})
	s.Mark("retiring")
}
func (s *Set) Mark(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.status {
		if s.status[index].Enabled {
			s.status[index].State = state
		}
	}
}
func (s *Set) Status() []Status {
	s.mu.RLock()
	result := slices.Clone(s.status)
	providers := maps.Clone(s.statusProviders)
	s.mu.RUnlock()
	for index := range result {
		result[index].Hooks = slices.Clone(result[index].Hooks)
		if provider := providers[result[index].ID]; provider != nil && result[index].Enabled {
			result[index].Details = provider()
		}
	}
	return result
}
func (s *Set) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.Retire()
		jobsDone := make(chan struct{})
		go func() { s.jobWG.Wait(); close(jobsDone) }()
		select {
		case <-jobsDone:
		case <-ctx.Done():
			s.closeErr = errors.Join(s.closeErr, ctx.Err())
		}
		for index := len(s.instances) - 1; index >= 0; index-- {
			if err := s.instances[index].Close(ctx); err != nil {
				s.closeErr = errors.Join(s.closeErr, fmt.Errorf("close module %s: %w", s.instanceIDs[index], err))
				s.mu.Lock()
				for position := range s.status {
					if s.status[position].ID == s.instanceIDs[index] {
						s.status[position].Error = "close_failed"
					}
				}
				s.mu.Unlock()
			}
		}
		s.mu.RLock()
		jobErr := s.jobErr
		s.mu.RUnlock()
		s.closeErr = errors.Join(s.closeErr, jobErr)
		s.Mark("closed")
	})
	return s.closeErr
}
