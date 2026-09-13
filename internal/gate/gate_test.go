package gate

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeTimer struct {
	mu       sync.Mutex
	stopped  bool
	deadline time.Time
	fn       func()
}

func (t *fakeTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	was := !t.stopped
	t.stopped = true
	return was
}

func (t *fakeTimer) fire(now time.Time) bool {
	t.mu.Lock()
	if t.stopped || t.deadline.After(now) {
		t.mu.Unlock()
		return false
	}
	t.stopped = true
	fn := t.fn
	t.mu.Unlock()
	fn()
	return true
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(100, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, fn func()) timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{deadline: c.now.Add(d), fn: fn}
	c.timers = append(c.timers, t)
	return t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	timers := append([]*fakeTimer(nil), c.timers...)
	c.mu.Unlock()
	for _, t := range timers {
		t.fire(now)
	}
}

func TestTranscriptAndTrigger(t *testing.T) {
	batch := Batch{Msgs: []Message{{UserID: "u1", UserName: "A", Text: "first"}, {UserID: "u2", Text: "second"}}}
	if got := batch.Transcript(0); got != "#1 [A](u1): first\n#2 [u2](u2): second\n" {
		t.Fatalf("unexpected transcript: %q", got)
	}
	if got := batch.Trigger("u1"); got.UserID != "u1" {
		t.Fatalf("unexpected trigger: %+v", got)
	}
}

func TestAutoReplyUnmentionedPolicy(t *testing.T) {
	clock := newFakeClock()
	p := New(context.Background(), func(string) Policy { return Policy{} }, nil, Options{clock: clock})
	if got := p.Absorb(Input{ConversationID: "conversation-1", UserID: "u", MsgID: "1", Text: "hello"}); got != VerdictIgnored {
		t.Fatalf("got %v, want ignored", got)
	}
	p = New(context.Background(), func(string) Policy { return Policy{AutoReplyUnmentioned: true, Silence: time.Hour} }, nil, Options{clock: clock})
	if got := p.Absorb(Input{ConversationID: "conversation-1", UserID: "u", MsgID: "1", Text: "hello"}); got != VerdictAbsorbed {
		t.Fatalf("got %v, want absorbed", got)
	}
	p.Close(time.Second)
}

func TestAutoReplyUnmentionedWaitsForSilence(t *testing.T) {
	clock := newFakeClock()
	flushed := make(chan Batch, 1)
	p := New(context.Background(), func(string) Policy {
		return Policy{Silence: 4 * time.Second, MaxCount: 1, AutoReplyUnmentioned: true}
	}, func(_ context.Context, b Batch) { flushed <- b }, Options{clock: clock})
	if got := p.Absorb(Input{ConversationID: "c", UserID: "u", MsgID: "q", Text: "question"}); got != VerdictAbsorbed {
		t.Fatalf("got %v, want absorbed", got)
	}
	clock.Advance(3 * time.Second)
	select {
	case <-flushed:
		t.Fatal("flushed before silence window")
	default:
	}
	clock.Advance(1 * time.Second)
	select {
	case batch := <-flushed:
		if len(batch.Msgs) != 1 || batch.Msgs[0].MsgID != "q" {
			t.Fatalf("unexpected batch: %#v", batch)
		}
	case <-time.After(time.Second):
		t.Fatal("flush timeout")
	}
	p.Close(time.Second)
}

func TestAdministratorReplyCancelsPendingQuestion(t *testing.T) {
	clock := newFakeClock()
	flushed := make(chan Batch, 1)
	p := New(context.Background(), func(string) Policy { return Policy{Silence: time.Second, AutoReplyUnmentioned: true} }, func(_ context.Context, b Batch) { flushed <- b }, Options{clock: clock})
	p.Absorb(Input{ConversationID: "c", UserID: "u", MsgID: "q", Text: "question"})
	if got := p.Absorb(Input{ConversationID: "c", UserID: "admin", MsgID: "a", ReplyToMessageID: "q", Text: "answer", Administrator: true}); got != VerdictIgnored {
		t.Fatalf("admin reply got %v, want ignored", got)
	}
	clock.Advance(time.Second)
	select {
	case batch := <-flushed:
		t.Fatalf("question was flushed after administrator reply: %#v", batch)
	case <-time.After(100 * time.Millisecond):
	}
	p.Close(time.Second)
}

func TestDedupFastLane(t *testing.T) {
	p := New(context.Background(), func(string) Policy { return Policy{} }, nil, Options{})
	in := Input{ConversationID: "conversation-1", UserID: "u", MsgID: "1", Text: "/help", Addressed: true}
	if got := p.Absorb(in); got != VerdictPass {
		t.Fatalf("first addressed message got %v", got)
	}
	if got := p.Absorb(in); got != VerdictDuplicate {
		t.Fatalf("duplicate addressed message got %v", got)
	}
	p.Close(time.Second)
}

func TestSilenceFlushesBatch(t *testing.T) {
	clock := newFakeClock()
	flushed := make(chan Batch, 1)
	p := New(context.Background(), func(string) Policy {
		return Policy{Silence: 4 * time.Second, MaxWindow: 30 * time.Second, AutoReplyUnmentioned: true}
	}, func(_ context.Context, b Batch) { flushed <- b }, Options{clock: clock})
	p.Absorb(Input{ConversationID: "conversation-1", UserID: "u", MsgID: "1", Text: "one"})
	clock.Advance(3 * time.Second)
	p.Absorb(Input{ConversationID: "conversation-1", UserID: "u", MsgID: "2", Text: "two"})
	clock.Advance(4 * time.Second)
	select {
	case batch := <-flushed:
		if len(batch.Msgs) != 2 || batch.Reason != ReasonSilence {
			t.Fatalf("unexpected batch: %#v", batch)
		}
	case <-time.After(time.Second):
		t.Fatal("flush timeout")
	}
	p.Close(time.Second)
}

func TestAutoReplyIgnoresCountFlush(t *testing.T) {
	clock := newFakeClock()
	flushed := make(chan Batch, 1)
	p := New(context.Background(), func(string) Policy {
		return Policy{Silence: time.Second, MaxCount: 2, AutoReplyUnmentioned: true}
	}, func(_ context.Context, b Batch) { flushed <- b }, Options{clock: clock})
	p.Absorb(Input{ConversationID: "conversation-1", UserID: "u", MsgID: "1", Text: "one"})
	p.Absorb(Input{ConversationID: "conversation-1", UserID: "u", MsgID: "2", Text: "two"})
	clock.Advance(time.Second)
	select {
	case batch := <-flushed:
		if len(batch.Msgs) != 2 || batch.Reason != ReasonSilence {
			t.Fatalf("unexpected batch: %#v", batch)
		}
	case <-time.After(time.Second):
		t.Fatal("flush timeout")
	}
	p.Close(time.Second)
}
