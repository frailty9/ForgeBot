package platform

import (
	"context"
	"testing"

	"github.com/soleapi/forgebot/internal/messaging"
)

type senderFunc func(context.Context, messaging.OutboundMessage) error

func (f senderFunc) SendTo(ctx context.Context, _ string, msg messaging.OutboundMessage) error {
	return f(ctx, msg)
}

func TestRegistrySendsByGroupBinding(t *testing.T) {
	registry := NewRegistry()
	var got messaging.OutboundMessage
	err := registry.RegisterConnector("echo-main", senderFunc(func(_ context.Context, msg messaging.OutboundMessage) error {
		got = msg
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.BindConversation(ConversationBinding{ConversationID: "conversation-a", ConnectorID: "echo-main", Target: "external-a"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Send(context.Background(), messaging.OutboundMessage{ConversationID: "conversation-a", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if got.Text != "hi" {
		t.Fatalf("sent message = %#v", got)
	}
}

func TestRegistryRejectsDuplicateSender(t *testing.T) {
	registry := NewRegistry()
	sender := senderFunc(func(context.Context, messaging.OutboundMessage) error { return nil })
	if err := registry.RegisterConnector("echo", sender); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterConnector("echo", sender); err == nil {
		t.Fatal("duplicate sender accepted")
	}
}

func TestRegistrySupportsMultipleGroupsAndSameProtocolInstances(t *testing.T) {
	registry := NewRegistry()
	var targets []string
	first := connectorSenderFunc(func(_ context.Context, target string, _ messaging.OutboundMessage) error {
		targets = append(targets, "first:"+target)
		return nil
	})
	second := connectorSenderFunc(func(_ context.Context, target string, _ messaging.OutboundMessage) error {
		targets = append(targets, "second:"+target)
		return nil
	})
	if err := registry.RegisterConnector("qq-main", first); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterConnector("qq-secondary", second); err != nil {
		t.Fatal(err)
	}
	bindings := []ConversationBinding{
		{ConversationID: "support", ConnectorID: "qq-main", Target: "10001"},
		{ConversationID: "engineering", ConnectorID: "qq-main", Target: "10002"},
		{ConversationID: "private-project", ConnectorID: "qq-secondary", Target: "20001"},
	}
	for _, binding := range bindings {
		if err := registry.BindConversation(binding); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"support", "engineering", "private-project"} {
		if err := registry.Send(context.Background(), messaging.OutboundMessage{ConversationID: id, Text: "hello"}); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"first:10001", "first:10002", "second:20001"}
	if len(targets) != len(want) {
		t.Fatalf("targets=%v", targets)
	}
	for i := range want {
		if targets[i] != want[i] {
			t.Fatalf("targets=%v want=%v", targets, want)
		}
	}
}

type connectorSenderFunc func(context.Context, string, messaging.OutboundMessage) error

func (f connectorSenderFunc) SendTo(ctx context.Context, target string, msg messaging.OutboundMessage) error {
	return f(ctx, target, msg)
}
