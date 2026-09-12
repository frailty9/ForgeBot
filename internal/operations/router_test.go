package operations

import (
	"context"
	"testing"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
)

type handlerFunc func(context.Context, messaging.InboundMessage) (platform.HandleResult, error)

func (f handlerFunc) HandleInbound(ctx context.Context, msg messaging.InboundMessage) (platform.HandleResult, error) {
	return f(ctx, msg)
}

type serviceFunc func(context.Context, Request) (string, error)

func (f serviceFunc) Execute(ctx context.Context, request Request) (string, error) {
	return f(ctx, request)
}

func TestOperatorCommandTakesPriorityOverBot(t *testing.T) {
	nextCalls, serviceCalls := 0, 0
	router, err := NewRouter(handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		nextCalls++
		return platform.HandleResult{}, nil
	}), serviceFunc(func(_ context.Context, request Request) (string, error) {
		serviceCalls++
		if request.Command != CommandStatus || request.ConversationID != "ops" || request.UserID != "u1" {
			t.Fatalf("request = %+v", request)
		}
		return "ready", nil
	}), []GroupPolicy{{ConversationID: "ops", Commands: []Command{CommandStatus}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.HandleInbound(context.Background(), messaging.InboundMessage{
		ConversationID: "ops", UserID: "u1", MessageID: "m1", Text: "/status",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted || result.Verdict != "operator_command" || serviceCalls != 1 || nextCalls != 0 {
		t.Fatalf("result=%+v service_calls=%d next_calls=%d", result, serviceCalls, nextCalls)
	}
}

func TestNonCommandInOperatorGroupContinuesToBot(t *testing.T) {
	nextCalls := 0
	router, err := NewRouter(handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		nextCalls++
		return platform.HandleResult{Accepted: true, Verdict: "bot"}, nil
	}), nil, []GroupPolicy{{ConversationID: "ops", Commands: []Command{CommandStatus}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.HandleInbound(context.Background(), messaging.InboundMessage{ConversationID: "ops", Text: "hello"})
	if err != nil || result.Verdict != "bot" || nextCalls != 1 {
		t.Fatalf("result=%+v err=%v next_calls=%d", result, err, nextCalls)
	}
}

func TestDeniedOperatorCommandDoesNotReachBot(t *testing.T) {
	router, err := NewRouter(handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		t.Fatal("bot handler called")
		return platform.HandleResult{}, nil
	}), nil, []GroupPolicy{{ConversationID: "ops", Commands: []Command{CommandStatus}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.HandleInbound(context.Background(), messaging.InboundMessage{ConversationID: "ops", Text: "/restart"})
	if err != nil || result.Accepted || result.Verdict != "operator_command_denied" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestUnknownOperatorCommandDoesNotReachBot(t *testing.T) {
	router, err := NewRouter(handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		t.Fatal("bot handler called")
		return platform.HandleResult{}, nil
	}), nil, []GroupPolicy{{ConversationID: "ops", Commands: []Command{CommandStatus}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.HandleInbound(context.Background(), messaging.InboundMessage{ConversationID: "ops", Text: "/unknown"})
	if err != nil || result.Accepted || result.Verdict != "operator_command_unknown" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestOperatorCommandOutsideOperatorGroupContinuesToBot(t *testing.T) {
	nextCalls := 0
	router, err := NewRouter(handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		nextCalls++
		return platform.HandleResult{Accepted: true, Verdict: "bot"}, nil
	}), nil, []GroupPolicy{{ConversationID: "ops", Commands: []Command{CommandStatus}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.HandleInbound(context.Background(), messaging.InboundMessage{ConversationID: "business", Text: "/status"})
	if err != nil || result.Verdict != "bot" || nextCalls != 1 {
		t.Fatalf("result=%+v err=%v next_calls=%d", result, err, nextCalls)
	}
}
