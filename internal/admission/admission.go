// Package admission contains non-optional inbound safety checks. Business
// features such as proactive buffering must not be able to disable these checks.
package admission

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/soleapi/forgebot/internal/messaging"
)

type Verdict uint8

const (
	Pass Verdict = iota
	Duplicate
)

type table struct {
	// 去重属于核心一致性约束，与是否启用主动插话/记忆无关。
	// 每次检查和插入必须在同一把锁内完成，不能先查后写造成并发重复处理。
	mu  sync.Mutex
	m   map[string]time.Time
	ttl time.Duration
	max int
}

func newTable(ttl time.Duration, max int) *table {
	if ttl <= 0 {
		ttl = time.Minute
	}
	if max <= 0 {
		max = 10000
	}
	return &table{m: make(map[string]time.Time), ttl: ttl, max: max}
}

func (t *table) seenOrAdd(key string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if expiry, ok := t.m[key]; ok && expiry.After(now) {
		return true
	}
	t.gcLocked(now)
	if len(t.m) >= t.max {
		var oldest string
		var expiry time.Time
		for candidate, value := range t.m {
			if oldest == "" || value.Before(expiry) {
				oldest, expiry = candidate, value
			}
		}
		delete(t.m, oldest)
	}
	t.m[key] = now.Add(t.ttl)
	return false
}

func (t *table) gcLocked(now time.Time) {
	for key, expiry := range t.m {
		if !expiry.After(now) {
			delete(t.m, key)
		}
	}
}

type Checker struct {
	clock      func() time.Time
	dedup      *table
	duplicates atomic.Int64
}

func New(ttl time.Duration, max int) *Checker {
	return &Checker{clock: func() time.Time { return time.Now().UTC() }, dedup: newTable(ttl, max)}
}

func (c *Checker) Check(message messaging.InboundMessage) Verdict {
	if c == nil {
		return Pass
	}
	now := c.clock()
	key := "id|" + message.ConversationID + "|" + message.MessageID
	if message.MessageID != "" && c.dedup.seenOrAdd(key, now) {
		c.duplicates.Add(1)
		return Duplicate
	}
	text := strings.TrimSpace(message.Text)
	if message.MessageID == "" && text != "" {
		key = "tx|" + message.ConversationID + "|" + message.UserID + "|" + text
		if c.dedup.seenOrAdd(key, now) {
			c.duplicates.Add(1)
			return Duplicate
		}
	}
	return Pass
}

func (c *Checker) Duplicates() int64 {
	if c == nil {
		return 0
	}
	return c.duplicates.Load()
}
