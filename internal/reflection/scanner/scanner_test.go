package scanner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/reflection/buffer"
)

func scannerBuffer(t *testing.T) *buffer.Buffer {
	t.Helper()
	b, err := buffer.New(buffer.Config{ConversationIDs: []string{"support"}, MessageTTL: time.Hour, QuietWindow: time.Minute, MaxSessionDuration: time.Hour, MaxMessagesPerConversation: 20, MaxMessagesTotal: 20, MaxMessageChars: 1000})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	q := messaging.InboundMessage{ConversationID: "support", UserID: "u", MessageID: "q", Text: "question"}
	a := messaging.InboundMessage{ConversationID: "support", UserID: "admin", MessageID: "a", ReplyToMessageID: "q", Text: "answer", Administrator: true}
	b.ObserveAt(base, q)
	b.ObserveAt(base.Add(time.Minute), a)
	return b
}

func TestScanOnceProcessesAndCompletes(t *testing.T) {
	b := scannerBuffer(t)
	var got buffer.AnswerSession
	s, err := New(Config{Interval: time.Minute, MaxSessions: 10, Buffer: b, Now: func() time.Time { return time.Date(2026, 9, 12, 8, 3, 0, 0, time.UTC) }, Process: func(_ context.Context, candidate buffer.AnswerSession) error { got = candidate; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	processed, retried, skipped := s.ScanOnce(context.Background())
	if processed != 1 || retried != 0 || skipped || got.Key == "" || len(got.Answers) != 1 {
		t.Fatalf("result=%d/%d/%v session=%+v", processed, retried, skipped, got)
	}
	if stats := b.Stats(time.Now()); stats.Sessions != 0 {
		t.Fatalf("sessions remain: %+v", stats)
	}
}

func TestScanOnceRetriesFailures(t *testing.T) {
	b := scannerBuffer(t)
	s, err := New(Config{Interval: time.Minute, MaxSessions: 10, Buffer: b, Now: func() time.Time { return time.Date(2026, 9, 12, 8, 3, 0, 0, time.UTC) }, Process: func(context.Context, buffer.AnswerSession) error { return errors.New("temporary") }})
	if err != nil {
		t.Fatal(err)
	}
	processed, retried, _ := s.ScanOnce(context.Background())
	if processed != 0 || retried != 1 {
		t.Fatalf("result=%d/%d", processed, retried)
	}
	if stats := b.Stats(time.Date(2026, 9, 12, 8, 3, 0, 0, time.UTC)); stats.Sessions != 1 {
		t.Fatalf("session not retained: %+v", stats)
	}
}

func TestScannerSingleFlight(t *testing.T) {
	b := scannerBuffer(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	s, err := New(Config{Interval: time.Minute, MaxSessions: 10, Buffer: b, Now: func() time.Time { return time.Date(2026, 9, 12, 8, 3, 0, 0, time.UTC) }, Process: func(context.Context, buffer.AnswerSession) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.ScanOnce(context.Background()); close(done) }()
	<-started
	_, _, skipped := s.ScanOnce(context.Background())
	if !skipped {
		t.Fatal("expected overlapping scan to be skipped")
	}
	close(release)
	<-done
}

func TestScanOnceUsesBatchProcessor(t *testing.T) {
	b := scannerBuffer(t)
	s, err := New(Config{
		Interval: time.Minute, MaxSessions: 10, Buffer: b,
		Now: func() time.Time { return time.Date(2026, 9, 12, 8, 3, 0, 0, time.UTC) },
		ProcessBatch: func(_ context.Context, sessions []buffer.AnswerSession) []error {
			if len(sessions) != 1 {
				t.Fatalf("batch size = %d", len(sessions))
			}
			return []error{nil}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, retried, skipped := s.ScanOnce(context.Background())
	if processed != 1 || retried != 0 || skipped {
		t.Fatalf("result=%d/%d/%v", processed, retried, skipped)
	}
}
