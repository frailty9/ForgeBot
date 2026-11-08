package modules

import (
	"context"
	"encoding/json"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/tooling"
)

// RegistrationKind identifies one host-owned extension surface. These values
// describe internal Go registrations and are not an external plugin protocol.
type RegistrationKind string

const (
	RegistrationTools              RegistrationKind = "tools"
	RegistrationCommands           RegistrationKind = "commands"
	RegistrationObservers          RegistrationKind = "observers"
	RegistrationParticipants       RegistrationKind = "participants"
	RegistrationJobs               RegistrationKind = "jobs"
	RegistrationStatus             RegistrationKind = "status"
	RegistrationConnectorProviders RegistrationKind = "connector_providers"
	RegistrationGroupEvents        RegistrationKind = "group_events"
	RegistrationJoinReview         RegistrationKind = "join_review"
)

// BuildContext exposes only capabilities prepared earlier in the same
// generation. It deliberately excludes the runtime manager and config store.
type BuildContext struct {
	Capabilities capability.View
}

// ContextFactory is implemented by modules that consume capabilities. The
// legacy Factory.Build path remains available while existing modules migrate.
type ContextFactory interface {
	BuildWithContext(context.Context, BuildContext, json.RawMessage) (Instance, error)
}

type ToolRegistration struct {
	ID       string
	Tool     tooling.Tool
	MaxCalls int
}

type CommandRequest struct {
	Name           string
	Arguments      []string
	ConversationID string
	UserID         string
	MessageID      string
	Administrator  bool
	Instructions   string
}

type CommandResult struct {
	Reply string
}

type CommandRegistration struct {
	ID     string
	Name   string
	Handle func(context.Context, CommandRequest) (CommandResult, error)
}

type ObserverRegistration struct {
	ID      string
	Observe func(messaging.InboundMessage)
}

type ParticipantResult struct {
	Consume  bool
	Accepted bool
	Verdict  string
}

type DeferredResponder func(context.Context, messaging.InboundMessage)

type ParticipantRegistration struct {
	ID     string
	Handle func(context.Context, messaging.InboundMessage, DeferredResponder) (ParticipantResult, error)
}

type BackgroundJob struct {
	ID  string
	Run func(context.Context) error
}

type CapabilityRegistration struct {
	ID    capability.ID
	Value any
}

// ProviderRegistration is the immutable host view of a connector provider.
// ModuleOptions are captured by the registry rather than supplied by modules,
// so providers always receive the options of their owning module generation.
type ProviderRegistration struct {
	ModuleID      string
	Provider      platform.ConnectorProvider
	ModuleOptions json.RawMessage
}

type ExtendedRegistrations struct {
	GroupEvents        []GroupEventRegistration
	JoinReview         *JoinReviewRegistration
	Tools              []ToolRegistration
	Commands           []CommandRegistration
	Observers          []ObserverRegistration
	Participants       []ParticipantRegistration
	Jobs               []BackgroundJob
	Capabilities       []CapabilityRegistration
	ConnectorProviders []platform.ConnectorProvider
	Status             func() any
}

// GroupEventRegistration 只返回当前事件对应的回复意图，不获得任意群发送权限。
type GroupEventRegistration struct {
	ID     string
	Handle func(context.Context, group.Event) (*group.EventReply, error)
}

// JoinReviewRegistration 只提供各群的审批规则；判断、去重、执行前复核与
// 调用连接器都由宿主完成，模块不获得审批或任何群操作权限。
type JoinReviewRegistration struct {
	Policy func(conversationID string) (group.Policy, bool)
}

// GroupReferencer 由按逻辑群配置的模块工厂实现，返回 options 中引用的群 ID，
// 宿主据此拒绝引用未绑定群的配置，避免写错 ID 后功能静默不生效。
type GroupReferencer interface {
	ReferencedGroups(json.RawMessage) ([]string, error)
}

type ExtendedProvider interface {
	ExtendedRegistrations() ExtendedRegistrations
}

// Starter and Retirer are optional lifecycle phases. Build must prepare all
// fallible resources; Start only activates already prepared background work.
type Starter interface{ Start(context.Context) }
type Retirer interface{ Retire() }
