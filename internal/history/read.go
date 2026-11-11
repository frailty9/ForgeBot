package history

import (
	"time"

	"github.com/soleapi/forgebot/internal/capability"
)

// Reader 是模块拿到的只读视图：返回的都是副本，模块无法改动容器内容。
// 已过期但还没被下次写入清理的记录不会被读到。
type Reader interface {
	// Recent 返回该群最近 n 条记录，按时间从旧到新；n <= 0 时返回全部。
	Recent(conversationID string, n int) []Record
	// Since 返回 Seq 大于 seq 的记录；gap 为 true 表示其中一部分已被移出，调用方读到的不连续。
	Since(conversationID string, seq uint64) (records []Record, gap bool)
	// Find 按平台消息 ID 查找；Bot 发出的消息没有平台 ID，查不到。
	Find(conversationID, messageID string) (Record, bool)
	Policy() Policy
}

// ReaderKey 由宿主在构建模块前提供，依赖它的模块在 Descriptor.Requires 中声明。
var ReaderKey = capability.NewKey[Reader]("history.reader.v1")

func (w *Window) visibleLocked(id string, now time.Time) []Record {
	g := w.groups[id]
	if g == nil {
		return nil
	}
	start := 0
	for start < len(g.records) && now.Sub(g.records[start].At) >= w.policy.TTL {
		start++
	}
	return g.records[start:]
}

func (w *Window) Recent(conversationID string, n int) []Record {
	w.mu.Lock()
	defer w.mu.Unlock()
	visible := w.visibleLocked(conversationID, w.now())
	if n > 0 && len(visible) > n {
		visible = visible[len(visible)-n:]
	}
	return append([]Record(nil), visible...)
}

func (w *Window) Since(conversationID string, seq uint64) ([]Record, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	g := w.groups[conversationID]
	if g == nil || seq+1 >= g.nextSeq {
		return nil, false
	}
	visible := w.visibleLocked(conversationID, w.now())
	start := 0
	for start < len(visible) && visible[start].Seq <= seq {
		start++
	}
	out := append([]Record(nil), visible[start:]...)
	if len(out) == 0 {
		return nil, true
	}
	return out, out[0].Seq > seq+1
}

func (w *Window) Find(conversationID, messageID string) (Record, bool) {
	if messageID == "" {
		return Record{}, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, record := range w.visibleLocked(conversationID, w.now()) {
		if record.MessageID == messageID {
			return record, true
		}
	}
	return Record{}, false
}

func (w *Window) Policy() Policy {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.policy
}

func truncate(text string, limit int) (string, bool) {
	runes := []rune(text)
	if len(runes) <= limit {
		return text, false
	}
	return string(runes[:limit]), true
}
