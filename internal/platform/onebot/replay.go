package onebot

import (
	"sync"
	"time"
)

type replayCache struct {
	mu   sync.Mutex
	ids  map[string]replayEntry
	ttl  time.Duration
	max  int
	next uint64
}

type replayEntry struct {
	expiresAt time.Time
	sequence  uint64
}

func newReplayCache(ttl time.Duration, max int) *replayCache {
	return &replayCache{ids: make(map[string]replayEntry), ttl: ttl, max: max}
}

func (c *replayCache) seenOrAdd(id string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, exists := c.ids[id]; exists && entry.expiresAt.After(now) {
		return true
	}
	if len(c.ids) >= c.max {
		c.evict(now)
	}
	c.next++
	c.ids[id] = replayEntry{expiresAt: now.Add(c.ttl), sequence: c.next}
	return false
}

func (c *replayCache) evict(now time.Time) {
	var oldestID string
	var oldestSequence uint64
	for id, entry := range c.ids {
		if !entry.expiresAt.After(now) {
			delete(c.ids, id)
			continue
		}
		if oldestID == "" || entry.sequence < oldestSequence {
			oldestID, oldestSequence = id, entry.sequence
		}
	}
	if len(c.ids) >= c.max && oldestID != "" {
		delete(c.ids, oldestID)
	}
}
