package history

import (
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// maxPendingEvents 限制尚未投递的事件数。订阅者过慢时丢弃最旧的事件并记日志，
// 不阻塞写入方（写入方可能正处在收发消息的必经路径上）。
const maxPendingEvents = 4096

type pendingEvent struct {
	appended *Record
	group    string
	evicted  []Record
	reason   EvictReason
}

type groupLog struct {
	records []Record
	nextSeq uint64
}

// Window 由宿主持有，跨热更新代际保留，进程重启清空。只记录已绑定的群。
type Window struct {
	mu       sync.Mutex
	policy   Policy
	allowed  map[string]bool
	groups   map[string]*groupLog
	now      func() time.Time
	logger   *slog.Logger
	dispatch sync.Mutex
	queue    []pendingEvent
	draining bool
	subs     atomic.Pointer[[]Subscriber]
}

func New(policy Policy, conversations []string, logger *slog.Logger) *Window {
	if logger == nil {
		logger = slog.Default()
	}
	w := &Window{policy: policy.Defaults(), groups: map[string]*groupLog{}, now: time.Now, logger: logger}
	w.allowed = allowedSet(conversations)
	return w
}

func allowedSet(conversations []string) map[string]bool {
	allowed := make(map[string]bool, len(conversations))
	for _, id := range conversations {
		if id = strings.TrimSpace(id); id != "" {
			allowed[id] = true
		}
	}
	return allowed
}

// SetSubscribers 在新一代模块生效时整体替换订阅者；旧一代随即不再收到事件。
func (w *Window) SetSubscribers(subscribers []Subscriber) {
	copied := append([]Subscriber(nil), subscribers...)
	w.subs.Store(&copied)
}

// Append 写入一条消息，返回分配的记录；未绑定的群、重复的平台消息 ID 返回 false。
// 写入时顺带清理过期记录；群内已满时逐条挤出最旧的一条。
func (w *Window) Append(record Record) (Record, bool) {
	id := strings.TrimSpace(record.ConversationID)
	w.mu.Lock()
	if !w.allowed[id] {
		w.mu.Unlock()
		return Record{}, false
	}
	now := w.now()
	g := w.groups[id]
	if g == nil {
		g = &groupLog{nextSeq: 1}
		w.groups[id] = g
	}
	w.expireLocked(id, g, now)
	if record.MessageID != "" {
		for _, existing := range g.records {
			if existing.MessageID == record.MessageID {
				w.mu.Unlock()
				w.drain()
				return Record{}, false
			}
		}
	}
	record.ConversationID, record.Seq, record.At = id, g.nextSeq, now
	record.Text = truncate(record.Text, w.policy.MaxTextChars)
	record.QuotedText = truncate(record.QuotedText, w.policy.MaxTextChars)
	g.nextSeq++
	if over := len(g.records) + 1 - w.policy.MaxMessages; over > 0 {
		w.evictLocked(id, g, over, EvictCapacity)
	}
	g.records = append(g.records, record)
	stored := record
	w.enqueue(pendingEvent{appended: &stored})
	w.mu.Unlock()
	w.drain()
	return record, true
}

func (w *Window) expireLocked(id string, g *groupLog, now time.Time) {
	count := 0
	for count < len(g.records) && now.Sub(g.records[count].At) >= w.policy.TTL {
		count++
	}
	w.evictLocked(id, g, count, EvictExpired)
}

// evictLocked 从最旧的一端移出 count 条，并排队一条“消息移出”事件。
func (w *Window) evictLocked(id string, g *groupLog, count int, reason EvictReason) {
	if count <= 0 {
		return
	}
	if count > len(g.records) {
		count = len(g.records)
	}
	removed := append([]Record(nil), g.records[:count]...)
	g.records = append(g.records[:0], g.records[count:]...)
	w.enqueue(pendingEvent{group: id, evicted: removed, reason: reason})
}

// Configure 在配置发布后调用：应用新的上限，丢弃已解绑群的记录（不发事件，
// 解绑即不再属于这个部署），调小条数上限时按 config 原因移出最旧的记录。
func (w *Window) Configure(policy Policy, conversations []string) {
	w.mu.Lock()
	w.policy = policy.Defaults()
	w.allowed = allowedSet(conversations)
	now := w.now()
	for id, g := range w.groups {
		if !w.allowed[id] {
			delete(w.groups, id)
			continue
		}
		w.expireLocked(id, g, now)
		w.evictLocked(id, g, len(g.records)-w.policy.MaxMessages, EvictConfig)
	}
	w.mu.Unlock()
	w.drain()
}
