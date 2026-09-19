package modules

import (
	"context"
	"encoding/json"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/tooling"
)

// RegistrationKind identifies one host-owned extension surface. These values
// describe internal Go registrations and are not an external plugin protocol.
type RegistrationKind string

const (
	RegistrationTools        RegistrationKind = "tools"
	RegistrationCommands     RegistrationKind = "commands"
	RegistrationObservers    RegistrationKind = "observers"
	RegistrationParticipants RegistrationKind = "participants"
	RegistrationJobs         RegistrationKind = "jobs"
	RegistrationStatus       RegistrationKind = "status"
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

type ParticipantRegistration struct {
	ID     string
	Handle func(context.Context, messaging.InboundMessage) (ParticipantResult, error)
}

type BackgroundJob struct {
	ID  string
	Run func(context.Context) error
}

type CapabilityRegistration struct {
	ID    capability.ID
	Value any
}

type ExtendedRegistrations struct {
	Tools        []ToolRegistration
	Commands     []CommandRegistration
	Observers    []ObserverRegistration
	Participants []ParticipantRegistration
	Jobs         []BackgroundJob
	Capabilities []CapabilityRegistration
	Status       func() any
}

type ExtendedProvider interface {
	ExtendedRegistrations() ExtendedRegistrations
}

// Starter and Retirer are optional lifecycle phases. Build must prepare all
// fallible resources; Start only activates already prepared background work.
type Starter interface{ Start(context.Context) }
type Retirer interface{ Retire() }
