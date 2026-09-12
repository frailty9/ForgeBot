package operations

import (
	"context"
	"errors"
	"strings"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
)

type Command string

const (
	CommandStatus     Command = "status"
	CommandRestart    Command = "restart"
	CommandPause      Command = "pause"
	CommandResume     Command = "resume"
	CommandGate       Command = "gate"
	CommandReloadDocs Command = "reload_docs"
)

var ErrUnsupportedCommand = errors.New("operator command is not supported")

type Request struct {
	Command        Command
	Arguments      []string
	ConversationID string
	UserID         string
	MessageID      string
}

// Service is intentionally narrower than the Web administration service.
type Service interface {
	Execute(context.Context, Request) (string, error)
}

type GroupPolicy struct {
	ConversationID string
	Notifications  bool
	Commands       []Command
}

type Router struct {
	next    platform.Handler
	service Service
	groups  map[string]map[Command]struct{}
}

func (r *Router) SetService(service Service) { r.service = service }

func NewRouter(next platform.Handler, service Service, policies []GroupPolicy) (*Router, error) {
	if next == nil {
		return nil, errors.New("operator router next handler is required")
	}
	router := &Router{next: next, service: service, groups: make(map[string]map[Command]struct{}, len(policies))}
	for _, policy := range policies {
		conversationID := strings.TrimSpace(policy.ConversationID)
		if conversationID == "" {
			return nil, errors.New("operator conversation id is required")
		}
		if _, exists := router.groups[conversationID]; exists {
			return nil, errors.New("operator conversation is duplicated")
		}
		commands := make(map[Command]struct{}, len(policy.Commands))
		for _, command := range policy.Commands {
			if !command.Valid() {
				return nil, ErrUnsupportedCommand
			}
			commands[command] = struct{}{}
		}
		router.groups[conversationID] = commands
	}
	return router, nil
}

func (r *Router) HandleInbound(ctx context.Context, message messaging.InboundMessage) (platform.HandleResult, error) {
	request, isCommand := Parse(message)
	allowed, isOperatorGroup := r.groups[message.ConversationID]
	if !isOperatorGroup || !isCommand {
		return r.next.HandleInbound(ctx, message)
	}
	if !request.Command.Valid() {
		return platform.HandleResult{Accepted: false, Verdict: "operator_command_unknown"}, nil
	}
	if _, ok := allowed[request.Command]; !ok {
		return platform.HandleResult{Accepted: false, Verdict: "operator_command_denied"}, nil
	}
	if r.service == nil {
		return platform.HandleResult{Accepted: false, Verdict: "operator_unavailable"}, nil
	}
	_, err := r.service.Execute(ctx, request)
	if err != nil {
		return platform.HandleResult{Accepted: false, Verdict: "operator_failed"}, err
	}
	return platform.HandleResult{Accepted: true, Verdict: "operator_command"}, nil
}

func Parse(message messaging.InboundMessage) (Request, bool) {
	text := strings.TrimSpace(message.Text)
	if !strings.HasPrefix(text, "/") {
		return Request{}, false
	}
	fields := strings.Fields(strings.TrimPrefix(text, "/"))
	if len(fields) == 0 {
		return Request{}, false
	}
	name := strings.ToLower(fields[0])
	if index := strings.IndexByte(name, '@'); index >= 0 {
		name = name[:index]
	}
	name = strings.ReplaceAll(name, "-", "_")
	return Request{
		Command: Command(name), Arguments: append([]string(nil), fields[1:]...),
		ConversationID: message.ConversationID, UserID: message.UserID, MessageID: message.MessageID,
	}, true
}

func (c Command) Valid() bool {
	switch c {
	case CommandStatus, CommandRestart, CommandPause, CommandResume, CommandGate, CommandReloadDocs:
		return true
	default:
		return false
	}
}
