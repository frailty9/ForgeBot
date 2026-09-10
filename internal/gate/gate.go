package gate

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Input struct {
	Platform  string
	ChatID    string
	MsgID     string
	UserID    string
	UserName  string
	Text      string
	Addressed bool
	HasMedia  bool
	At        time.Time
}

func (i Input) SessionKey() string { return i.Platform + ":" + i.ChatID }

type Message struct {
	MsgID    string
	UserID   string
	UserName string
	Text     string
	At       time.Time
}

type FlushReason uint8

const (
	ReasonSilence FlushReason = iota
	ReasonCount
	ReasonWindow
)

type Batch struct {
	SessionKey string
	Platform   string
	ChatID     string
	Msgs       []Message
	Reason     FlushReason
	OpenedAt   time.Time
}

func (b Batch) Transcript(maxTextLen int) string {
	var out strings.Builder
	for i, msg := range b.Msgs {
		name := msg.UserName
		if name == "" {
			name = msg.UserID
		}
		text := msg.Text
		if maxTextLen > 0 {
			runes := []rune(text)
			if len(runes) > maxTextLen {
				text = string(runes[:maxTextLen]) + "..."
			}
		}
		fmt.Fprintf(&out, "#%d [%s](%s): %s\n", i+1, name, msg.UserID, text)
	}
	return out.String()
}

func (b Batch) Trigger(asker string) Message {
	for i := len(b.Msgs) - 1; i >= 0; i-- {
		if b.Msgs[i].UserID == asker {
			return b.Msgs[i]
		}
	}
	if len(b.Msgs) == 0 {
		return Message{}
	}
	return b.Msgs[len(b.Msgs)-1]
}

type Policy struct {
	Silence        time.Duration
	MaxCount       int
	MaxWindow      time.Duration
	MaxBuffered    int
	MaxTextLen     int
	RequireMention bool
}

type Verdict uint8

const (
	VerdictPass Verdict = iota
	VerdictAbsorbed
	VerdictDuplicate
	VerdictIgnored
)

func (v Verdict) String() string {
	switch v {
	case VerdictPass:
		return "pass"
	case VerdictAbsorbed:
		return "absorbed"
	case VerdictDuplicate:
		return "duplicate"
	case VerdictIgnored:
		return "ignored"
	default:
		return "unknown"
	}
}

type bucket struct {
	mu         sync.Mutex
	key        string
	platform   string
	chatID     string
	msgs       []Message
	openedAt   time.Time
	lastAt     time.Time
	lastTouch  time.Time
	timer      timer
	generation uint64
	flushing   bool
}

type Pool struct {
	lifecycle sync.RWMutex
	clock     clock
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	buckets   map[string]*bucket
	policy    func(string) Policy
	onFlush   func(context.Context, Batch)
	dedup     *dedupTable
	sem       chan struct{}
	wg        sync.WaitGroup
	closed    atomic.Bool
	absorbed  atomic.Int64
	deduped   atomic.Int64
	ignored   atomic.Int64
	dropped   atomic.Int64
	flushed   atomic.Int64
}

type Options struct {
	clock         clock
	DedupTTL      time.Duration
	DedupMax      int
	MaxConcurrent int
	IdleBucketTTL time.Duration
}

func New(ctx context.Context, policy func(string) Policy, onFlush func(context.Context, Batch), opts Options) *Pool {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	if policy == nil {
		policy = func(string) Policy { return Policy{} }
	}
	if opts.DedupTTL <= 0 {
		opts.DedupTTL = time.Minute
	}
	if opts.DedupMax <= 0 {
		opts.DedupMax = 10000
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 1
	}
	if opts.clock == nil {
		opts.clock = realClock{}
	}
	p := &Pool{clock: opts.clock, ctx: ctx, cancel: cancel, buckets: make(map[string]*bucket), policy: policy, onFlush: onFlush, dedup: newDedupTable(opts.DedupTTL, opts.DedupMax), sem: make(chan struct{}, opts.MaxConcurrent)}
	if opts.IdleBucketTTL > 0 {
		go p.janitor(opts.IdleBucketTTL)
	}
	return p
}

func (p *Pool) Absorb(in Input) Verdict {
	if p == nil {
		return VerdictPass
	}
	p.lifecycle.RLock()
	defer p.lifecycle.RUnlock()
	if p.closed.Load() || p.ctx.Err() != nil {
		return VerdictIgnored
	}
	now := p.clock.Now()
	messageTime := in.At
	if messageTime.IsZero() {
		messageTime = now
	}
	key := in.SessionKey()
	if in.MsgID != "" && p.dedup.seenOrAdd("id|"+key+"|"+in.MsgID, now) {
		p.deduped.Add(1)
		return VerdictDuplicate
	}
	if text := strings.TrimSpace(in.Text); text != "" && p.dedup.seenOrAdd("tx|"+key+"|"+in.UserID+"|"+text, now) {
		p.deduped.Add(1)
		return VerdictDuplicate
	}
	pol := p.policy(in.Platform)
	if in.Addressed {
		return VerdictPass
	}
	if pol.RequireMention {
		p.ignored.Add(1)
		return VerdictIgnored
	}
	if strings.TrimSpace(in.Text) == "" || in.HasMedia {
		p.dropped.Add(1)
		return VerdictIgnored
	}
	b := p.getOrCreate(in)
	if len(b.msgs) == 0 {
		b.openedAt = now
	}
	b.msgs = append(b.msgs, Message{MsgID: in.MsgID, UserID: in.UserID, UserName: in.UserName, Text: in.Text, At: messageTime})
	if pol.MaxBuffered > 0 && len(b.msgs) > pol.MaxBuffered {
		b.msgs = b.msgs[len(b.msgs)-pol.MaxBuffered:]
	}
	b.lastAt, b.lastTouch = now, now
	var batch *Batch
	if !b.flushing && pol.MaxCount > 0 && len(b.msgs) >= pol.MaxCount {
		batch = b.detachLocked(ReasonCount)
	} else if !b.flushing {
		p.armLocked(b, pol, now)
	}
	b.mu.Unlock()
	p.absorbed.Add(1)
	if batch != nil {
		p.dispatch(b, *batch)
	}
	return VerdictAbsorbed
}

func (p *Pool) getOrCreate(in Input) *bucket {
	key := in.SessionKey()
	p.mu.Lock()
	b := p.buckets[key]
	if b == nil {
		b = &bucket{key: key, platform: in.Platform, chatID: in.ChatID}
		p.buckets[key] = b
	}
	p.mu.Unlock()
	b.mu.Lock()
	return b
}

func (p *Pool) armLocked(b *bucket, pol Policy, now time.Time) {
	wait := pol.Silence
	if wait <= 0 {
		wait = time.Nanosecond
	}
	if pol.MaxWindow > 0 {
		windowLeft := b.openedAt.Add(pol.MaxWindow).Sub(now)
		if windowLeft <= 0 {
			wait = time.Nanosecond
		} else if windowLeft < wait {
			wait = windowLeft
		}
	}
	b.generation++
	generation := b.generation
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timer = p.clock.AfterFunc(wait, func() {
		p.onTimer(b, generation)
	})
}

func (p *Pool) onTimer(b *bucket, generation uint64) {
	pol := p.policy(b.platform)
	now := p.clock.Now()

	b.mu.Lock()
	if b.generation != generation || len(b.msgs) == 0 {
		b.mu.Unlock()
		return
	}
	if b.flushing {
		b.mu.Unlock()
		return
	}
	silentFor := now.Sub(b.lastAt)
	windowExpired := pol.MaxWindow > 0 && !b.openedAt.Add(pol.MaxWindow).After(now)
	if pol.Silence > 0 && silentFor < pol.Silence && !windowExpired {
		p.armLocked(b, pol, now)
		b.mu.Unlock()
		return
	}
	reason := ReasonSilence
	if windowExpired {
		reason = ReasonWindow
	}
	batch := b.detachLocked(reason)
	b.mu.Unlock()

	p.dispatch(b, *batch)
}

func (b *bucket) detachLocked(reason FlushReason) *Batch {
	msgs := append([]Message(nil), b.msgs...)
	b.msgs = nil
	b.generation++
	b.flushing = true
	return &Batch{SessionKey: b.key, Platform: b.platform, ChatID: b.chatID, Msgs: msgs, Reason: reason, OpenedAt: b.openedAt}
}

func (p *Pool) dispatch(b *bucket, batch Batch) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.sem <- struct{}{}
		defer func() {
			<-p.sem
			if recovered := recover(); recovered != nil {
				p.dropped.Add(int64(len(batch.Msgs)))
			}
			b.mu.Lock()
			b.flushing = false
			if len(b.msgs) > 0 && !p.closed.Load() {
				p.armLocked(b, p.policy(b.platform), p.clock.Now())
			}
			b.mu.Unlock()
		}()
		if p.onFlush != nil {
			p.onFlush(p.ctx, batch)
		}
		p.flushed.Add(1)
	}()
}

func (p *Pool) Refresh() {
	if p == nil {
		return
	}
	p.mu.Lock()
	buckets := make([]*bucket, 0, len(p.buckets))
	for _, b := range p.buckets {
		buckets = append(buckets, b)
	}
	p.mu.Unlock()
	now := p.clock.Now()
	for _, b := range buckets {
		b.mu.Lock()
		if len(b.msgs) > 0 && !b.flushing {
			p.armLocked(b, p.policy(b.platform), now)
		}
		b.mu.Unlock()
	}
}

func (p *Pool) DropPending() {
	if p == nil {
		return
	}
	p.mu.Lock()
	buckets := make([]*bucket, 0, len(p.buckets))
	for _, b := range p.buckets {
		buckets = append(buckets, b)
	}
	p.mu.Unlock()
	for _, b := range buckets {
		b.mu.Lock()
		if b.timer != nil {
			b.timer.Stop()
		}
		if len(b.msgs) > 0 {
			p.dropped.Add(int64(len(b.msgs)))
			b.msgs = nil
		}
		b.generation++
		b.mu.Unlock()
	}
}

func (p *Pool) janitor(idle time.Duration) {
	ticker := time.NewTicker(idle)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case now := <-ticker.C:
			p.dedup.gc(now)
			p.mu.Lock()
			for key, b := range p.buckets {
				b.mu.Lock()
				emptyAndIdle := len(b.msgs) == 0 && !b.flushing && now.Sub(b.lastTouch) >= idle
				if emptyAndIdle {
					delete(p.buckets, key)
				}
				b.mu.Unlock()
			}
			p.mu.Unlock()
		}
	}
}

func (p *Pool) Close(grace time.Duration) {
	if p == nil || !p.closed.CompareAndSwap(false, true) {
		return
	}
	p.lifecycle.Lock()
	p.cancel()
	p.DropPending()
	p.lifecycle.Unlock()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	if grace <= 0 {
		<-done
		return
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func (p *Pool) Stats() Stats {
	if p == nil {
		return Stats{}
	}
	return Stats{
		Absorbed: p.absorbed.Load(),
		Deduped:  p.deduped.Load(),
		Ignored:  p.ignored.Load(),
		Dropped:  p.dropped.Load(),
		Flushed:  p.flushed.Load(),
		Buckets:  p.BucketCount(),
	}
}

func (p *Pool) BucketCount() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.buckets)
}

type Stats struct {
	Absorbed int64 `json:"absorbed"`
	Deduped  int64 `json:"deduped"`
	Ignored  int64 `json:"ignored"`
	Dropped  int64 `json:"dropped"`
	Flushed  int64 `json:"flushed"`
	Buckets  int   `json:"buckets"`
}
