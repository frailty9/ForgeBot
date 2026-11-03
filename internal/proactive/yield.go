package proactive

import (
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/capability"
)

var YieldKey = capability.NewKey[*YieldState]("proactive.yield_state.v1")

// YieldState 由进程持有、模块消费；仅表示主动插话让位，不改变全局暂停或@资格。
// revision 使暂停前已生成的回复即使到期后也无法补发。
type YieldState struct {
	mu         sync.Mutex
	groups     map[string]yieldEntry
	now        func() time.Time
	restricted bool
	sequence   uint64
}
type yieldEntry struct {
	until    time.Time
	revision uint64
}

func NewYieldState() *YieldState { return &YieldState{groups: map[string]yieldEntry{}, now: time.Now} }
func (s *YieldState) Snapshot(group string) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.groups[group]
	if s.restricted && !exists {
		return 0, true
	}
	return entry.revision, s.now().Before(entry.until)
}
func (s *YieldState) Suspend(group string, duration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.groups[group]
	if s.restricted && !exists {
		return
	}
	s.sequence++
	entry.revision = s.sequence
	entry.until = s.now().Add(duration)
	s.groups[group] = entry
}
func (s *YieldState) Resume(group string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.groups[group]
	if s.restricted && !exists {
		return
	}
	s.sequence++
	entry.revision = s.sequence
	entry.until = time.Time{}
	s.groups[group] = entry
}

// Configure 在发布时按已绑定群收口状态容量，移除群立即阻止旧批次投递。
func (s *YieldState) Configure(groups []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := map[string]yieldEntry{}
	for _, group := range groups {
		entry, exists := s.groups[group]
		if !exists {
			s.sequence++
			entry.revision = s.sequence
		}
		next[group] = entry
	}
	s.groups = next
	s.restricted = true
}
func (s *YieldState) CanSend(group string, revision uint64) bool {
	current, paused := s.Snapshot(group)
	return !paused && current == revision
}
