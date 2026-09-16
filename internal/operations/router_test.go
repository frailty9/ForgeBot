package operations

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
)

type handlerFunc func(context.Context, messaging.InboundMessage) (platform.HandleResult, error)

func (f handlerFunc) HandleInbound(ctx context.Context, msg messaging.InboundMessage) (platform.HandleResult, error) {
	return f(ctx, msg)
}

type serviceFunc func(context.Context, Request) (Result, error)

func (f serviceFunc) Execute(ctx context.Context, request Request) (Result, error) {
	return f(ctx, request)
}

func TestOperatorCommandTakesPriorityOverBot(t *testing.T) {
	nextCalls, serviceCalls := 0, 0
	router, err := NewRouter(handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		nextCalls++
		return platform.HandleResult{}, nil
	}), serviceFunc(func(_ context.Context, request Request) (Result, error) {
		serviceCalls++
		if request.Command != CommandStatus || request.ConversationID != "ops" || request.UserID != "u1" || !request.Administrator {
			t.Fatalf("request = %+v", request)
		}
		return Result{Reply: "ready"}, nil
	}), testSender{}, []GroupPolicy{{ConversationID: "ops", Commands: []Command{CommandStatus}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.HandleInbound(context.Background(), messaging.InboundMessage{
		ConversationID: "ops", UserID: "u1", MessageID: "m1", Text: "/status", Administrator: true,
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
	}), nil, testSender{}, []GroupPolicy{{ConversationID: "ops", Commands: []Command{CommandStatus}}})
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
	}), nil, testSender{}, []GroupPolicy{{ConversationID: "ops", Commands: []Command{CommandStatus}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.HandleInbound(context.Background(), messaging.InboundMessage{ConversationID: "ops", Text: "/restart"})
	if err != nil || result.Accepted || result.Verdict != "operator_command_denied" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestUnknownOperatorCommandContinuesToBot(t *testing.T) {
	nextCalls := 0
	router, err := NewRouter(handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		nextCalls++
		return platform.HandleResult{Accepted: true, Verdict: "bot"}, nil
	}), nil, testSender{}, []GroupPolicy{{ConversationID: "ops", Commands: []Command{CommandStatus}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.HandleInbound(context.Background(), messaging.InboundMessage{ConversationID: "ops", Text: "/unknown"})
	if err != nil || !result.Accepted || result.Verdict != "bot" || nextCalls != 1 {
		t.Fatalf("result=%+v err=%v next_calls=%d", result, err, nextCalls)
	}
}

func TestOperatorCommandOutsideOperatorGroupContinuesToBot(t *testing.T) {
	nextCalls := 0
	router, err := NewRouter(handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		nextCalls++
		return platform.HandleResult{Accepted: true, Verdict: "bot"}, nil
	}), nil, testSender{}, []GroupPolicy{{ConversationID: "ops", Commands: []Command{CommandStatus}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.HandleInbound(context.Background(), messaging.InboundMessage{ConversationID: "business", Text: "/status"})
	if err != nil || result.Verdict != "bot" || nextCalls != 1 {
		t.Fatalf("result=%+v err=%v next_calls=%d", result, err, nextCalls)
	}
}

func TestFollowupRunsAfterAcknowledgementAndSendsCompletion(t *testing.T) {
	sender := &recordingSender{sent: make(chan messaging.OutboundMessage, 2)}
	acknowledged := make(chan struct{})
	router, err := NewRouter(handlerFunc(func(context.Context, messaging.InboundMessage) (platform.HandleResult, error) {
		t.Fatal("bot handler called")
		return platform.HandleResult{}, nil
	}), serviceFunc(func(context.Context, Request) (Result, error) {
		return Result{Reply: "accepted", Followup: func(context.Context) (string, error) {
			<-acknowledged
			return "complete", nil
		}}, nil
	}), sender, []GroupPolicy{{ConversationID: "ops", Commands: []Command{CommandRestart}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.HandleInbound(context.Background(), messaging.InboundMessage{
		ConversationID: "ops", UserID: "u", MessageID: "m", Text: "/restart",
	})
	if err != nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	first := <-sender.sent
	if first.Text != "accepted" {
		t.Fatalf("first reply = %+v", first)
	}
	close(acknowledged)
	select {
	case second := <-sender.sent:
		if second.Text != "complete" || second.ConversationID != "ops" || second.ReplyToMessageID != "m" {
			t.Fatalf("followup = %+v", second)
		}
	case <-time.After(time.Second):
		t.Fatal("followup was not sent")
	}
}

type testSender struct{}

func (testSender) Send(context.Context, messaging.OutboundMessage) error { return nil }

type recordingSender struct {
	mu   sync.Mutex
	sent chan messaging.OutboundMessage
}

func (s *recordingSender) Send(_ context.Context, message messaging.OutboundMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent <- message
	return nil
}
