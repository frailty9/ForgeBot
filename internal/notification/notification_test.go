package notification

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type targets []string

func (t targets) NotificationTargets() []string { return append([]string(nil), t...) }

type recordingSender struct {
	mu      sync.Mutex
	sent    map[string][]string
	failFor string
	done    chan struct{}
}

func (s *recordingSender) SendNotification(_ context.Context, conversationID, text string) error {
	if conversationID == s.failFor {
		return errors.New("send failed")
	}
	s.mu.Lock()
	s.sent[conversationID] = append(s.sent[conversationID], text)
	s.mu.Unlock()
	select {
	case s.done <- struct{}{}:
	default:
	}
	return nil
}

func TestDispatcherSendsToEveryTargetAndContinuesAfterFailure(t *testing.T) {
	sender := &recordingSender{sent: make(map[string][]string), failFor: "broken", done: make(chan struct{}, 2)}
	dispatcher, err := NewDispatcher(targets{"broken", "ops-a", "ops-b"}, sender, slog.New(slog.NewTextHandler(io.Discard, nil)), 4)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go dispatcher.Run(ctx)
	dispatcher.Publish(Event{Level: LevelError, Component: "connector", Code: "failed", Message: "qq-main disconnected"})
	for range 2 {
		select {
		case <-sender.done:
		case <-time.After(time.Second):
			t.Fatal("notification was not delivered")
		}
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent["ops-a"]) != 1 || len(sender.sent["ops-b"]) != 1 {
		t.Fatalf("sent = %+v", sender.sent)
	}
}

func TestPublishDoesNotBlockWhenQueueIsFull(t *testing.T) {
	dispatcher, err := NewDispatcher(targets{"ops"}, &recordingSender{}, slog.New(slog.NewTextHandler(io.Discard, nil)), 1)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.Publish(Event{Code: "first"})
	done := make(chan struct{})
	go func() {
		dispatcher.Publish(Event{Code: "dropped"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked")
	}
}

func TestDispatcherSkipsExcludedConversation(t *testing.T) {
	sender := &recordingSender{sent: make(map[string][]string), done: make(chan struct{}, 1)}
	dispatcher, err := NewDispatcher(targets{"source", "other"}, sender, slog.New(slog.NewTextHandler(io.Discard, nil)), 2)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go dispatcher.Run(ctx)
	dispatcher.Publish(Event{Component: "search", Code: "reload_failed", ExcludeConversationID: "source"})
	select {
	case <-sender.done:
	case <-time.After(time.Second):
		t.Fatal("notification was not delivered")
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent["source"]) != 0 || len(sender.sent["other"]) != 1 {
		t.Fatalf("sent = %+v", sender.sent)
	}
}
