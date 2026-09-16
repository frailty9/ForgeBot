package gate

import (
	"sync"
	"time"
)

type dedupTable struct {
	mu  sync.Mutex
	m   map[string]time.Time
	ttl time.Duration
	max int
}

func newDedupTable(ttl time.Duration, max int) *dedupTable {
	return &dedupTable{m: make(map[string]time.Time), ttl: ttl, max: max}
}

func (d *dedupTable) seenOrAdd(key string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if expiry, ok := d.m[key]; ok && expiry.After(now) {
		return true
	}
	if len(d.m) >= d.max {
		d.gcLocked(now)
		if len(d.m) >= d.max {
			var oldestKey string
			var oldestExpiry time.Time
			for candidate, expiry := range d.m {
				if oldestKey == "" || expiry.Before(oldestExpiry) {
					oldestKey, oldestExpiry = candidate, expiry
				}
			}
			delete(d.m, oldestKey)
		}
	}
	d.m[key] = now.Add(d.ttl)
	return false
}

func (d *dedupTable) gc(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gcLocked(now)
}

func (d *dedupTable) gcLocked(now time.Time) {
	for key, expiry := range d.m {
		if !expiry.After(now) {
			delete(d.m, key)
		}
	}
}
