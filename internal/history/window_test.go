package history

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(format string, args ...any) {
	l.mu.Lock()
	l.events = append(l.events, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *eventLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.events, ",")
}

func (l *eventLog) subscriber() Subscriber {
	return Subscriber{ID: "log",
		Appended: func(r Record) { l.add("+%d", r.Seq) },
		Evicted: func(_ string, records []Record, reason EvictReason) {
			for _, r := range records {
				l.add("-%d:%s", r.Seq, reason)
			}
		}}
}

func newTestWindow(policy Policy) (*Window, *time.Time) {
	current := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	w := New(policy, []string{"g"}, nil)
	w.now = func() time.Time { return current }
	return w, &current
}

func appendText(t *testing.T, w *Window, id, text string) Record {
	t.Helper()
	record, ok := w.Append(Record{ConversationID: "g", MessageID: id, Role: RoleMember, Text: text})
	if !ok {
		t.Fatalf("append %s rejected", id)
	}
	return record
}

// 满了逐条滑动：每进一条挤出最旧的一条，“移出”事件先于“进入”事件；Seq 不复用。
func TestWindowSlidesOneByOneAndOrdersEvents(t *testing.T) {
	w, _ := newTestWindow(Policy{MaxMessages: 3})
	var log eventLog
	w.SetSubscribers([]Subscriber{log.subscriber()})
	for i := 1; i <= 5; i++ {
		appendText(t, w, fmt.Sprint("m", i), "hello")
	}
	if got := log.String(); got != "+1,+2,+3,-1:capacity,+4,-2:capacity,+5" {
		t.Fatal(got)
	}
	recent := w.Recent("g", 0)
	if len(recent) != 3 || recent[0].Seq != 3 || recent[2].Seq != 5 {
		t.Fatalf("unexpected window %+v", recent)
	}
	if last := w.Recent("g", 2); len(last) != 2 || last[0].Seq != 4 {
		t.Fatalf("Recent(2) = %+v", last)
	}
}

// 过期记录读不到；真正移出发生在下次写入，原因是 expired，可能一次移出多条。
func TestExpiredRecordsHiddenAndEvictedOnNextWrite(t *testing.T) {
	w, now := newTestWindow(Policy{MaxMessages: 10, TTL: time.Minute})
	var log eventLog
	w.SetSubscribers([]Subscriber{log.subscriber()})
	appendText(t, w, "a", "x")
	appendText(t, w, "b", "x")
	*now = now.Add(2 * time.Minute)
	if got := w.Recent("g", 0); len(got) != 0 {
		t.Fatalf("expired records visible: %+v", got)
	}
	if _, ok := w.Find("g", "a"); ok {
		t.Fatal("expired record found")
	}
	appendText(t, w, "c", "x")
	if got := log.String(); got != "+1,+2,-1:expired,-2:expired,+3" {
		t.Fatal(got)
	}
}

// Since 让模块按自己的游标批量读取；读到的不连续时 gap 为 true。
func TestSinceReportsGapsForEvictedRecords(t *testing.T) {
	w, now := newTestWindow(Policy{MaxMessages: 3, TTL: time.Minute})
	for i := 1; i <= 5; i++ {
		appendText(t, w, fmt.Sprint("m", i), "x")
	}
	cases := []struct {
		seq     uint64
		first   uint64
		count   int
		wantGap bool
	}{{0, 3, 3, true}, {2, 3, 3, false}, {4, 5, 1, false}, {5, 0, 0, false}}
	for _, tc := range cases {
		records, gap := w.Since("g", tc.seq)
		if len(records) != tc.count || gap != tc.wantGap || (tc.count > 0 && records[0].Seq != tc.first) {
			t.Fatalf("Since(%d) = %+v gap=%v", tc.seq, records, gap)
		}
	}
	if records, gap := w.Since("other", 0); records != nil || gap {
		t.Fatal("unknown group reported records")
	}
	*now = now.Add(2 * time.Minute)
	if records, gap := w.Since("g", 2); len(records) != 0 || !gap {
		t.Fatalf("all expired: %+v gap=%v", records, gap)
	}
}

// 只记录已绑定的群；同一平台消息 ID 只记一次；文本按字符截断；Bot 消息没有 ID 也能记录。
func TestAppendRejectsUnboundAndDuplicatesAndTruncates(t *testing.T) {
	w, _ := newTestWindow(Policy{MaxTextChars: 3})
	if _, ok := w.Append(Record{ConversationID: "unbound", MessageID: "x", Text: "x"}); ok {
		t.Fatal("unbound group recorded")
	}
	first := appendText(t, w, "a", "你好世界")
	if first.Text != "你好世" {
		t.Fatalf("truncated text = %q", first.Text)
	}
	if _, ok := w.Append(Record{ConversationID: "g", MessageID: "a", Text: "replay"}); ok {
		t.Fatal("duplicate message id recorded twice")
	}
	for i := 0; i < 2; i++ {
		if _, ok := w.Append(Record{ConversationID: " g ", Role: RoleBot, ReplyTo: "a", Text: "答"}); !ok {
			t.Fatal("bot reply without message id rejected")
		}
	}
	if found, ok := w.Find("g", "a"); !ok || found.Seq != 1 {
		t.Fatal("Find failed", found, ok)
	}
	if len(w.Recent("g", 0)) != 3 {
		t.Fatal("bot replies not recorded")
	}
}

// 热更新调小上限按 config 原因移出；解绑的群直接丢弃，不发事件，也不再接收写入。
func TestConfigureShrinksAndDropsUnboundGroups(t *testing.T) {
	w, _ := newTestWindow(Policy{MaxMessages: 5})
	w.Configure(Policy{MaxMessages: 5}, []string{"g", "h"})
	var log eventLog
	w.SetSubscribers([]Subscriber{log.subscriber()})
	for i := 1; i <= 4; i++ {
		appendText(t, w, fmt.Sprint("m", i), "x")
	}
	if _, ok := w.Append(Record{ConversationID: "h", MessageID: "h1", Text: "x"}); !ok {
		t.Fatal("bound group h rejected")
	}
	w.Configure(Policy{MaxMessages: 2}, []string{"g"})
	if got := log.String(); got != "+1,+2,+3,+4,+1,-1:config,-2:config" {
		t.Fatal(got)
	}
	if len(w.Recent("h", 0)) != 0 {
		t.Fatal("unbound group kept its records")
	}
	if _, ok := w.Append(Record{ConversationID: "h", MessageID: "h2", Text: "x"}); ok {
		t.Fatal("unbound group still accepted")
	}
	if got := w.Recent("g", 0); len(got) != 2 || got[0].Seq != 3 {
		t.Fatalf("shrunk window = %+v", got)
	}
}

// 一个订阅者 panic 不影响其他订阅者和容器；旧一代被替换后不再收到事件。
func TestSubscriberPanicIsolatedAndReplacementStopsOldGeneration(t *testing.T) {
	w, _ := newTestWindow(Policy{})
	var old, current eventLog
	panicking := Subscriber{ID: "bad", Appended: func(Record) { panic("boom") }}
	w.SetSubscribers([]Subscriber{panicking, old.subscriber()})
	appendText(t, w, "a", "x")
	w.SetSubscribers([]Subscriber{current.subscriber()})
	appendText(t, w, "b", "x")
	if old.String() != "+1" || current.String() != "+2" {
		t.Fatalf("old=%q current=%q", old.String(), current.String())
	}
}

// 订阅者在回调里再写入（例如回调触发了一次发送）不会死锁，事件仍按写入顺序投递。
func TestReentrantAppendFromSubscriberKeepsOrder(t *testing.T) {
	w, _ := newTestWindow(Policy{})
	var log eventLog
	reply := Subscriber{ID: "reply", Appended: func(r Record) {
		if r.Role == RoleMember {
			w.Append(Record{ConversationID: "g", Role: RoleBot, ReplyTo: r.MessageID, Text: "answer"})
		}
	}}
	w.SetSubscribers([]Subscriber{reply, log.subscriber()})
	done := make(chan struct{})
	go func() {
		w.Append(Record{ConversationID: "g", MessageID: "q", Role: RoleMember, Text: "question"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("re-entrant append deadlocked")
	}
	if got := log.String(); got != "+1,+2" {
		t.Fatal(got)
	}
}

// 并发写入时同群 Seq 连续且不重复，每条“进入”事件恰好投递一次。
func TestConcurrentAppendsAssignUniqueSequence(t *testing.T) {
	w, _ := newTestWindow(Policy{MaxMessages: 1000})
	var mu sync.Mutex
	seen := map[uint64]int{}
	w.SetSubscribers([]Subscriber{{ID: "count", Appended: func(r Record) {
		mu.Lock()
		seen[r.Seq]++
		mu.Unlock()
	}}})
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w.Append(Record{ConversationID: "g", MessageID: fmt.Sprint("m", i), Text: "x"})
		}(i)
	}
	wg.Wait()
	w.drain()
	if len(seen) != 200 {
		t.Fatalf("delivered %d distinct records", len(seen))
	}
	for seq := uint64(1); seq <= 200; seq++ {
		if seen[seq] != 1 {
			t.Fatalf("seq %d delivered %d times", seq, seen[seq])
		}
	}
}
