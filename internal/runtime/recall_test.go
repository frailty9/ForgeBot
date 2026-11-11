package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/platform"
)

type recallSender struct {
	sent   []messaging.OutboundMessage
	called []string
	fail   error
}

func (s *recallSender) SendTo(_ context.Context, _ string, message messaging.OutboundMessage) error {
	s.sent = append(s.sent, message)
	return nil
}
func (s *recallSender) RecallMessage(_ context.Context, target, messageID string) error {
	s.called = append(s.called, target+":"+messageID)
	return s.fail
}

func recallManager(t *testing.T, consumer modules.Factory) (*Manager, *recallSender) {
	t.Helper()
	sender := &recallSender{}
	registry := platform.NewRegistry()
	if err := registry.RegisterConnector("test", sender); err != nil {
		t.Fatal(err)
	}
	if err := registry.BindConversation(platform.ConversationBinding{ConversationID: "local", ConnectorID: "test", Target: "platform-group"}); err != nil {
		t.Fatal(err)
	}
	builder := Builder{Sender: registry}
	if consumer != nil {
		builder.Modules = moduleRegistry(t, consumer)
	}
	manager, err := NewManager(context.Background(), testSnapshot(), builder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	return manager, sender
}

func TestMessageRecallIsCallableWithIDWithoutHistoryOrChatCommand(t *testing.T) {
	manager, sender := recallManager(t, nil)
	recall, err := manager.ForConversation("local")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.builder.History.Find("local", "message-from-yesterday"); ok {
		t.Fatal("unexpected history record")
	}
	if err := recall.Recall(context.Background(), "message-from-yesterday"); err != nil {
		t.Fatal(err)
	}
	if len(sender.called) != 1 || sender.called[0] != "platform-group:message-from-yesterday" {
		t.Fatal(sender.called)
	}
	if len(sender.sent) != 0 {
		t.Fatal("capability sent chat confirmation")
	}
	result, err := manager.HandleInbound(context.Background(), messaging.InboundMessage{ConversationID: "local", UserID: "admin", MessageID: "command", Text: "/recall", ReplyToMessageID: "message-from-yesterday", PlatformRole: "admin", Command: true})
	if err == nil || result.Verdict != "llm_failed" || len(sender.called) != 1 {
		t.Fatalf("/recall unexpectedly executed: %+v %v calls=%v", result, err, sender.called)
	}
}

func TestMessageRecallValidationPauseAndOperationFailures(t *testing.T) {
	manager, sender := recallManager(t, nil)
	if _, err := manager.ForConversation("unbound"); operation.Describe(err).Code != operation.NotFound {
		t.Fatal(err)
	}
	recall, err := manager.ForConversation("local")
	if err != nil {
		t.Fatal(err)
	}
	if err := recall.Recall(context.Background(), ""); operation.Describe(err).Code != operation.Invalid {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := recall.Recall(ctx, "m"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	manager.SetPaused(true)
	if err := recall.Recall(context.Background(), "m"); operation.Describe(err).Code != operation.Disabled {
		t.Fatal(err)
	}
	manager.SetPaused(false)
	if len(sender.called) != 0 {
		t.Fatal(sender.called)
	}
	for _, failure := range []error{operation.New(operation.PermissionDenied, "denied"), operation.New(operation.Unsupported, "unsupported"), operation.New(operation.Expired, "platform rejected age"), operation.Wrap("platform.recall", operation.Timeout, operation.EffectUnknown, context.DeadlineExceeded)} {
		sender.fail = failure
		before := len(sender.called)
		err := recall.Recall(context.Background(), "m")
		got, want := operation.Describe(err), operation.Describe(failure)
		if !errors.Is(err, failure) || got.Code != want.Code || got.Effect != want.Effect || len(sender.called) != before+1 {
			t.Fatalf("got=%+v want=%+v calls=%v", got, want, sender.called)
		}
	}
	managerWithoutRecall, err := NewManager(context.Background(), testSnapshot(), Builder{Sender: exampleNoRecallSender{}})
	if err != nil {
		t.Fatal(err)
	}
	defer managerWithoutRecall.Shutdown(context.Background())
	unsupported, err := managerWithoutRecall.ForConversation("local")
	if err != nil {
		t.Fatal(err)
	}
	if err := unsupported.Recall(context.Background(), "m"); operation.Describe(err).Code != operation.Unsupported {
		t.Fatal(err)
	}
}

type exampleNoRecallSender struct{}

func (exampleNoRecallSender) Send(context.Context, messaging.OutboundMessage) error { return nil }

func TestBoundRecallCapabilitySurvivesUnrelatedUpdatesButRejectsRebinding(t *testing.T) {
	manager, sender := recallManager(t, nil)
	recall, err := manager.ForConversation("local")
	if err != nil {
		t.Fatal(err)
	}
	next, _ := manager.Snapshot()
	next.Version++
	next.Runtime.Bot.Name = "renamed"
	if _, err := manager.Apply(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if err := recall.Recall(context.Background(), "same-group"); err != nil {
		t.Fatal(err)
	}
	next = next.Clone()
	next.Version++
	next.Runtime.Platforms.Groups[0].Target = "new-target"
	if _, err := manager.Apply(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if err := recall.Recall(context.Background(), "old-platform-id"); operation.Describe(err).Code != operation.Conflict {
		t.Fatal(err)
	}
	if len(sender.called) != 1 {
		t.Fatal("stale capability reached new binding", sender.called)
	}
	next = next.Clone()
	next.Version++
	next.Runtime.Platforms.Groups = nil
	if _, err := manager.Apply(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if err := recall.Recall(context.Background(), "old-platform-id"); operation.Describe(err).Code != operation.Conflict {
		t.Fatal(err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := recall.Recall(context.Background(), "m"); operation.Describe(err).Code != operation.Unavailable {
		t.Fatal(err)
	}
}

type recallConsumerFactory struct{ provider *group.RecallProvider }

func (recallConsumerFactory) Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: "test.recall_consumer", DefaultEnabled: true, Requires: []capability.ID{group.RecallKey.ID()}}
}
func (recallConsumerFactory) ValidateOptions(json.RawMessage) error      { return nil }
func (recallConsumerFactory) PublicOptions(json.RawMessage) (any, error) { return nil, nil }
func (recallConsumerFactory) Build(context.Context, json.RawMessage) (modules.Instance, error) {
	return nil, errors.New("capabilities required")
}
func (f recallConsumerFactory) BuildWithContext(_ context.Context, build modules.BuildContext, _ json.RawMessage) (modules.Instance, error) {
	provider, ok := capability.Resolve(build.Capabilities, group.RecallKey)
	if !ok {
		return nil, errors.New("missing recall capability")
	}
	*f.provider = provider
	return testPlainModuleInstance{}, nil
}
func TestModulesResolveNarrowRecallCapability(t *testing.T) {
	var provider group.RecallProvider
	_, sender := recallManager(t, recallConsumerFactory{&provider})
	if provider == nil {
		t.Fatal("host did not provide recall capability")
	}
	recall, err := provider.ForConversation("local")
	if err != nil {
		t.Fatal(err)
	}
	if err := recall.Recall(context.Background(), "module-target"); err != nil {
		t.Fatal(err)
	}
	if len(sender.called) != 1 || sender.called[0] != "platform-group:module-target" {
		t.Fatal(sender.called)
	}
}

func TestRegistryRecallReportsSupportAndPropagatesErrors(t *testing.T) {
	sender := &recallSender{fail: errors.New("failure")}
	registry := platform.NewRegistry()
	_ = registry.RegisterConnector("test", sender)
	_ = registry.BindConversation(platform.ConversationBinding{ConversationID: "g", ConnectorID: "test", Target: "platform-g"})
	support, err := registry.Support(context.Background(), "g")
	if err != nil || !support.MessageRecall || support.Permission != "unknown" {
		t.Fatal(support, err)
	}
	if err := registry.Recall(context.Background(), "g", "m"); !errors.Is(err, sender.fail) {
		t.Fatal(err)
	}
}

func TestInboundWaitingForConnectorReplacementCanBeCanceled(t *testing.T) {
	manager, err := NewManager(context.Background(), testSnapshot(), Builder{Sender: registeredSender(t).registry})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	for _, kind := range []string{"inbound", "group_event"} {
		t.Run(kind, func(t *testing.T) {
			unlock := manager.lockRoute()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				var err error
				if kind == "inbound" {
					_, err = manager.HandleInbound(ctx, messaging.InboundMessage{})
				} else {
					_, err = manager.HandleGroupEvent(ctx, group.Event{Kind: group.MemberJoined})
				}
				done <- err
			}()
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				unlock()
				t.Fatal("canceled request remained blocked on replacement")
			}
			unlock()
		})
	}
}

func TestRouteBarrierWakesWaitersOnRelease(t *testing.T) {
	manager, err := NewManager(context.Background(), testSnapshot(), Builder{Sender: registeredSender(t).registry})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	unlock := manager.lockRoute()
	acquired := make(chan error, 1)
	go func() {
		err := manager.readRoute(context.Background())
		if err == nil {
			manager.routeMu.RUnlock()
		}
		acquired <- err
	}()
	select {
	case <-acquired:
		t.Fatal("reader passed an exclusive barrier")
	case <-time.After(20 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter was not woken after release")
	}
}
