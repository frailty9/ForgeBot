package platform

import (
	"context"
	"testing"

	"github.com/soleapi/forgebot/internal/messaging"
)

type senderFunc func(context.Context, messaging.OutboundMessage) error

func (f senderFunc) Send(ctx context.Context, msg messaging.OutboundMessage) error {
	return f(ctx, msg)
}

func TestRegistrySendsByPlatform(t *testing.T) {
	registry := NewRegistry()
	var got messaging.OutboundMessage
	err := registry.RegisterSender("echo", senderFunc(func(_ context.Context, msg messaging.OutboundMessage) error {
		got = msg
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Send(context.Background(), messaging.OutboundMessage{Platform: "echo", ChatID: "c", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if got.Text != "hi" {
		t.Fatalf("sent message = %#v", got)
	}
}

func TestRegistryRejectsDuplicateSender(t *testing.T) {
	registry := NewRegistry()
	sender := senderFunc(func(context.Context, messaging.OutboundMessage) error { return nil })
	if err := registry.RegisterSender("echo", sender); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterSender("echo", sender); err == nil {
		t.Fatal("duplicate sender accepted")
	}
}
