// Package operations parses and authorizes the deliberately small command
// surface exposed to administrators through chat conversations.
package operations

import (
	"context"
	"errors"
	"strings"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
)

// Command is a normalized chat-side administrative command.
type Command string

const (
	CommandStatus     Command = "status"
	CommandRestart    Command = "restart"
	CommandPause      Command = "pause"
	CommandResume     Command = "resume"
	CommandProactive  Command = "proactive"
	CommandReloadDocs Command = "reload_docs"
)

var ErrUnsupportedCommand = errors.New("operator command is not supported")

// Request is a parsed command request.
type Request struct {
	Command        Command
	Arguments      []string
	ConversationID string
	UserID         string
	MessageID      string
	Administrator  bool
}

// Result is the user-facing outcome of an operation.
type Result struct {
	Reply    string
	Followup func(context.Context) (string, error)
}

// Service is intentionally narrower than the Web administration service.
type Service interface {
	Execute(context.Context, Request) (Result, error)
}

type GroupPolicy struct {
	ConversationID string
	Notifications  bool
	Commands       []Command
}

// Router intercepts authorized commands before forwarding ordinary messages.
type Router struct {
	next    platform.Handler
	service Service
	sender  platform.Sender
	groups  map[string]map[Command]struct{}
}

func (r *Router) SetService(service Service) { r.service = service }

// NewRouter creates a command router for configured group policies.
func NewRouter(next platform.Handler, service Service, sender platform.Sender, policies []GroupPolicy) (*Router, error) {
	if next == nil {
		return nil, errors.New("operator router next handler is required")
	}
	if len(policies) > 0 && sender == nil {
		return nil, errors.New("operator router sender is required")
	}
	router := &Router{next: next, service: service, sender: sender, groups: make(map[string]map[Command]struct{}, len(policies))}
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
		return r.next.HandleInbound(ctx, message)
	}
	if _, ok := allowed[request.Command]; !ok {
		return r.respond(ctx, message, "此管理群未启用该命令。", false, "operator_command_denied")
	}
	if r.service == nil {
		return r.respond(ctx, message, "管理能力尚未就绪。", false, "operator_unavailable")
	}
	result, err := r.service.Execute(ctx, request)
	if err != nil {
		return r.respond(ctx, message, "管理命令执行失败："+err.Error(), false, "operator_failed")
	}
	if strings.TrimSpace(result.Reply) != "" {
		if _, err := r.respond(ctx, message, result.Reply, true, "operator_command"); err != nil {
			return platform.HandleResult{Accepted: false, Verdict: "operator_reply_failed"}, err
		}
	}
	if result.Followup != nil {
		go r.runFollowup(request, result.Followup)
	}
	return platform.HandleResult{Accepted: true, Verdict: "operator_command"}, nil
}

func (r *Router) respond(ctx context.Context, message messaging.InboundMessage, text string, accepted bool, verdict string) (platform.HandleResult, error) {
	err := r.sender.Send(ctx, messaging.OutboundMessage{
		ConversationID: message.ConversationID, ReplyToMessageID: message.MessageID, Text: text,
	})
	return platform.HandleResult{Accepted: accepted, Verdict: verdict}, err
}

func (r *Router) runFollowup(request Request, followup func(context.Context) (string, error)) {
	text, err := followup(context.Background())
	if err != nil {
		text = "管理命令执行失败：" + err.Error()
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	_ = r.sender.Send(context.Background(), messaging.OutboundMessage{
		ConversationID: request.ConversationID, ReplyToMessageID: request.MessageID, Text: text,
	})
}

// Parse recognizes a supported slash command from an inbound message.
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
		Administrator: message.Administrator,
	}, true
}

func (c Command) Valid() bool {
	switch c {
	case CommandStatus, CommandRestart, CommandPause, CommandResume, CommandProactive, CommandReloadDocs:
		return true
	default:
		return false
	}
}
