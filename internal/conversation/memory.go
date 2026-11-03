package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/operation"
)

// MemoryPolicy 仅约束进程内成功问答上下文，不是群消息历史存储。
// Disabled 缺省 false，即默认启用；不落盘、不保存图片二进制和工具结果。
type MemoryPolicy struct {
	Disabled        bool          `json:"disabled"`
	TTL             time.Duration `json:"-"`
	MaxPairs        int           `json:"max_pairs"`
	MaxSessions     int           `json:"max_sessions"`
	MaxPending      int           `json:"max_pending"`
	MaxSessionBytes int           `json:"max_session_bytes"`
	MaxTotalBytes   int           `json:"max_total_bytes"`
}

func (p MemoryPolicy) Defaults() MemoryPolicy {
	if p.TTL == 0 {
		p.TTL = 20 * time.Minute
	}
	if p.MaxPairs == 0 {
		p.MaxPairs = 6
	}
	if p.MaxSessions == 0 {
		p.MaxSessions = 1000
	}
	if p.MaxPending == 0 {
		p.MaxPending = 8
	}
	if p.MaxSessionBytes == 0 {
		p.MaxSessionBytes = 64 << 10
	}
	if p.MaxTotalBytes == 0 {
		p.MaxTotalBytes = 8 << 20
	}
	return p
}
func (p MemoryPolicy) Validate() error {
	p = p.Defaults()
	if p.TTL <= 0 || p.TTL > 24*time.Hour || p.MaxPairs < 1 || p.MaxPairs > 64 || p.MaxSessions < 1 || p.MaxSessions > 10000 || p.MaxPending < 1 || p.MaxPending > 64 || p.MaxSessionBytes < 1024 || p.MaxSessionBytes > 1<<20 || p.MaxTotalBytes < p.MaxSessionBytes || p.MaxTotalBytes > 64<<20 {
		return fmt.Errorf("invalid conversation memory limits")
	}
	return nil
}
func (p MemoryPolicy) MarshalJSON() ([]byte, error) {
	type alias MemoryPolicy
	p = p.Defaults()
	return json.Marshal(struct {
		alias
		TTL string `json:"ttl"`
	}{alias(p), p.TTL.String()})
}
func (p *MemoryPolicy) UnmarshalJSON(data []byte) error {
	type alias MemoryPolicy
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("conversation_memory must be an object")
	}
	for name, value := range fields {
		switch name {
		case "disabled", "ttl", "max_pairs", "max_sessions", "max_pending", "max_session_bytes", "max_total_bytes":
		default:
			return fmt.Errorf("unknown conversation memory option %q", name)
		}
		if string(value) == "null" {
			return fmt.Errorf("memory option %s cannot be null", name)
		}
	}
	raw := struct {
		alias
		TTL string `json:"ttl"`
	}{alias: alias(p.Defaults())}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.TTL != "" {
		ttl, err := time.ParseDuration(raw.TTL)
		if err != nil {
			return err
		}
		raw.alias.TTL = ttl
	}
	*p = MemoryPolicy(raw.alias)
	return p.Validate()
}

type memoryKey struct{ group, user string }
type pair struct {
	user, answer llm.Message
	bytes        int
}
type session struct {
	active  bool
	waiters []*waiter
	pairs   []pair
	bytes   int
	updated time.Time
}
type waiter struct {
	ready   chan struct{}
	granted bool
}

// Memory 的所有权属于进程 Runtime，而不是 generation。换代时旧 Engine
// 只释放自己的租约，不能清空新 Engine 仍在使用的会话服务。
type Memory struct {
	mu       sync.Mutex
	policy   MemoryPolicy
	sessions map[memoryKey]*session
	total    int
	now      func() time.Time
	epoch    uint64
}

func NewMemory(policy MemoryPolicy) *Memory {
	return &Memory{policy: policy.Defaults(), sessions: map[memoryKey]*session{}, now: time.Now}
}

// Configure 仅在配置发布后调用；候选构建失败不能提前剪裁当前记忆。
func (m *Memory) Configure(policy MemoryPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.policy.Disabled && policy.Disabled {
		m.epoch++
	}
	m.policy = policy.Defaults()
	for key, s := range m.sessions {
		if m.policy.Disabled || m.now().Sub(s.updated) >= m.policy.TTL {
			m.clear(s)
		} else {
			m.trim(s)
		}
		if !s.active && len(s.waiters) == 0 && len(s.pairs) == 0 {
			delete(m.sessions, key)
		}
	}
	m.evict()
	// 缩小全局预算时，在途快照可继续使用，但保留池必须立即有界。
	for m.total > m.policy.MaxTotalBytes {
		var oldest *session
		for _, s := range m.sessions {
			if len(s.pairs) > 0 && (oldest == nil || s.updated.Before(oldest.updated)) {
				oldest = s
			}
		}
		if oldest == nil {
			break
		}
		m.dropFirst(oldest)
	}
}

// Lease 持有一个群+用户的顺序执行权。排队时间计入调用方 context 的总预算。
type Lease struct {
	owner   *Memory
	session *session
	epoch   uint64
	once    sync.Once
}

func (m *Memory) Acquire(ctx context.Context, group, user string) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.policy.Disabled {
		m.mu.Unlock()
		return nil, nil
	}
	key := memoryKey{group, user}
	s := m.sessions[key]
	if s == nil {
		m.evict()
		if len(m.sessions) >= m.policy.MaxSessions {
			m.evictOne()
		}
		if len(m.sessions) >= m.policy.MaxSessions {
			m.mu.Unlock()
			return nil, operation.New(operation.Busy, "conversation session capacity exhausted")
		}
		s = &session{updated: m.now()}
		m.sessions[key] = s
	}
	if !s.active {
		s.active = true
		lease := &Lease{owner: m, session: s, epoch: m.epoch}
		m.mu.Unlock()
		return lease, nil
	}
	if len(s.waiters) >= m.policy.MaxPending {
		m.mu.Unlock()
		return nil, operation.New(operation.Busy, "conversation queue capacity exhausted")
	}
	w := &waiter{ready: make(chan struct{})}
	s.waiters = append(s.waiters, w)
	epoch := m.epoch
	m.mu.Unlock()
	select {
	case <-w.ready:
		lease := &Lease{owner: m, session: s, epoch: epoch}
		if err := ctx.Err(); err != nil {
			lease.Release()
			return nil, err
		}
		return lease, nil
	case <-ctx.Done():
		m.mu.Lock()
		if w.granted {
			m.mu.Unlock()
			(&Lease{owner: m, session: s, epoch: epoch}).Release()
		} else {
			for i, candidate := range s.waiters {
				if candidate == w {
					s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
					break
				}
			}
			m.mu.Unlock()
		}
		return nil, ctx.Err()
	}
}

func (l *Lease) History() []llm.Message {
	if l == nil {
		return nil
	}
	m := l.owner
	m.mu.Lock()
	defer m.mu.Unlock()
	s := l.session
	if m.policy.Disabled || l.epoch != m.epoch {
		return nil
	}
	if m.now().Sub(s.updated) >= m.policy.TTL {
		m.clear(s)
		return nil
	}
	result := make([]llm.Message, 0, len(s.pairs)*2)
	for _, pair := range s.pairs {
		result = append(result, cloneMessage(pair.user), cloneMessage(pair.answer))
	}
	return result
}

// Commit 仅在最终投递确认成功后调用；超限问答整对不保存，避免截断成误导上下文。
func (l *Lease) Commit(user llm.Message, answer string) {
	if l == nil {
		return
	}
	user = retainedUser(user)
	if len(user.Content) == 0 || answer == "" {
		return
	}
	p := pair{user: user, answer: llm.TextMessage(llm.RoleAssistant, answer)}
	encoded, _ := json.Marshal([]llm.Message{p.user, p.answer})
	p.bytes = len(encoded)
	m := l.owner
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.policy.Disabled || l.epoch != m.epoch || p.bytes > m.policy.MaxSessionBytes {
		return
	}
	s := l.session
	if m.now().Sub(s.updated) >= m.policy.TTL {
		m.clear(s)
	}
	s.pairs = append(s.pairs, p)
	s.bytes += p.bytes
	m.total += p.bytes
	s.updated = m.now()
	m.trim(s)
	m.evict()
	// 所有会话都在途时也不允许突破总字节预算；新提交可保留较少上下文。
	for m.total > m.policy.MaxTotalBytes && len(s.pairs) > 0 {
		m.dropFirst(s)
	}
}

func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		m := l.owner
		m.mu.Lock()
		defer m.mu.Unlock()
		s := l.session
		if len(s.waiters) > 0 {
			w := s.waiters[0]
			s.waiters = s.waiters[1:]
			w.granted = true
			close(w.ready)
		} else {
			s.active = false
		}
		m.evict()
	})
}

func (m *Memory) clear(s *session) { m.total -= s.bytes; s.pairs = nil; s.bytes = 0 }
func (m *Memory) dropFirst(s *session) {
	p := s.pairs[0]
	s.pairs[0] = pair{}
	s.pairs = s.pairs[1:]
	s.bytes -= p.bytes
	m.total -= p.bytes
}
func (m *Memory) trim(s *session) {
	for len(s.pairs) > m.policy.MaxPairs || s.bytes > m.policy.MaxSessionBytes {
		m.dropFirst(s)
	}
}
func (m *Memory) evict() {
	for key, s := range m.sessions {
		if !s.active && len(s.waiters) == 0 && m.now().Sub(s.updated) >= m.policy.TTL {
			m.clear(s)
			delete(m.sessions, key)
		}
	}
	for (m.total > m.policy.MaxTotalBytes || len(m.sessions) > m.policy.MaxSessions) && m.evictOne() {
	}
}
func (m *Memory) evictOne() bool {
	var oldest *session
	var key memoryKey
	for candidate, s := range m.sessions {
		if !s.active && len(s.waiters) == 0 && (oldest == nil || s.updated.Before(oldest.updated)) {
			oldest = s
			key = candidate
		}
	}
	if oldest == nil {
		return false
	}
	m.clear(oldest)
	delete(m.sessions, key)
	return true
}
func cloneMessage(message llm.Message) llm.Message {
	message.Content = append([]llm.ContentPart(nil), message.Content...)
	return message
}
func retainedUser(message llm.Message) llm.Message {
	result := llm.Message{Role: llm.RoleUser}
	images := 0
	for _, part := range message.Content {
		if (part.Type == "" || part.Type == "text") && part.Text != "" {
			result.Content = append(result.Content, llm.ContentPart{Type: "text", Text: part.Text})
		}
		if part.Type == "image" && part.MediaURL != "" && len(part.MediaURL) <= 2048 && images < 4 {
			// data URL 同样包含二进制，不进入短期记忆。
			if len(part.MediaURL) >= 5 && part.MediaURL[:5] == "data:" {
				continue
			}
			result.Content = append(result.Content, llm.ContentPart{Type: "image", MediaURL: part.MediaURL, MimeType: part.MimeType})
			images++
		}
	}
	return result
}
