package conversation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/operation"
)

func acquire(t *testing.T, m *Memory, group, user string) *Lease {
	t.Helper()
	l, err := m.Acquire(context.Background(), group, user)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestMemoryIsolationTTLAndSnapshot(t *testing.T) {
	m := NewMemory(MemoryPolicy{MaxPairs: 2})
	now := time.Now()
	m.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		l := acquire(t, m, "g", "u")
		l.Commit(llm.TextMessage(llm.RoleUser, fmt.Sprint(i)), "answer")
		l.Release()
	}
	l := acquire(t, m, "g", "u")
	history := l.History()
	if len(history) != 4 || history[0].Text() != "1" {
		t.Fatal(history)
	}
	history[0].Content[0].Text = "mutated"
	if l.History()[0].Text() == "mutated" {
		t.Fatal("snapshot aliases memory")
	}
	l.Release()
	for _, key := range [][2]string{{"g", "other"}, {"other", "u"}} {
		l := acquire(t, m, key[0], key[1])
		if len(l.History()) != 0 {
			t.Fatal("cross-user context")
		}
		l.Release()
	}
	now = now.Add(21 * time.Minute)
	l = acquire(t, m, "g", "u")
	defer l.Release()
	if len(l.History()) != 0 {
		t.Fatal("expired history retained")
	}
}

func TestMemoryQueueFIFOAndCancellation(t *testing.T) {
	m := NewMemory(MemoryPolicy{MaxPending: 1})
	first := acquire(t, m, "g", "u")
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan error, 1)
	go func() {
		l, err := m.Acquire(ctx, "g", "u")
		if l != nil {
			l.Release()
		}
		waiting <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		m.mu.Lock()
		count := len(first.session.waiters)
		m.mu.Unlock()
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waiter not enqueued")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := m.Acquire(context.Background(), "g", "u"); err == nil || operation.Describe(err).Code != operation.Busy {
		t.Fatal("queue unbounded", err)
	}
	// 其他群或用户无需等待当前租约。
	other := acquire(t, m, "g", "other")
	other.Release()
	cancel()
	if err := <-waiting; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	first.Commit(llm.TextMessage(llm.RoleUser, "first"), "answer")
	first.Release()
	next := acquire(t, m, "g", "u")
	defer next.Release()
	if len(next.History()) != 2 {
		t.Fatal("committed context lost after canceled waiter")
	}
}

func TestMemoryHotPolicyAndDisabledLeaseCannotRepopulate(t *testing.T) {
	m := NewMemory(MemoryPolicy{})
	l := acquire(t, m, "g", "u")
	l.Commit(llm.TextMessage(llm.RoleUser, "one"), "answer")
	m.Configure(MemoryPolicy{MaxPairs: 1})
	if len(l.History()) != 2 {
		t.Fatal("unrelated update discarded context")
	}
	m.Configure(MemoryPolicy{Disabled: true})
	m.Configure(MemoryPolicy{})
	l.Commit(llm.TextMessage(llm.RoleUser, "stale"), "must not return")
	l.Release()
	fresh := acquire(t, m, "g", "u")
	defer fresh.Release()
	if len(fresh.History()) != 0 {
		t.Fatal("old lease resurrected disabled memory")
	}
	if restarted := acquire(t, NewMemory(MemoryPolicy{}), "g", "u"); len(restarted.History()) != 0 {
		t.Fatal("restart retained memory")
	} else {
		restarted.Release()
	}
}

func TestMemoryCapacityAndMediaBounds(t *testing.T) {
	m := NewMemory(MemoryPolicy{MaxSessions: 2, MaxSessionBytes: 1024, MaxTotalBytes: 1024})
	first := acquire(t, m, "g", "a")
	second := acquire(t, m, "g", "b")
	if _, err := m.Acquire(context.Background(), "g", "c"); err == nil {
		t.Fatal("active session cap exceeded")
	}
	first.Commit(llm.TextMessage(llm.RoleUser, strings.Repeat("x", 600)), "answer")
	second.Commit(llm.TextMessage(llm.RoleUser, strings.Repeat("y", 600)), "answer")
	if m.total > 1024 {
		t.Fatal("global byte cap exceeded")
	}
	first.Release()
	second.Release()
	third := acquire(t, m, "g", "c")
	third.Commit(llm.Message{Role: llm.RoleUser, Content: []llm.ContentPart{{Type: "image", Data: "binary"}, {Type: "image", MediaURL: "data:image/png;base64,abc"}, {Type: "text", Text: "question"}}}, "answer")
	if history := third.History(); len(history) != 2 || len(history[0].Content) != 1 {
		t.Fatal("binary retained", history)
	}
	third.Release()
}
