// Package scanner provides the periodic, single-flight stage that drains
// completed reflection sessions from the in-memory observation buffer.
package scanner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/soleapi/forgebot/internal/reflection/buffer"
)

var ErrInvalidConfig = errors.New("reflection scanner configuration is invalid")

type Config struct {
	Interval     time.Duration
	MaxSessions  int
	Buffer       *buffer.Buffer
	Process      func(context.Context, buffer.AnswerSession) error
	ProcessBatch func(context.Context, []buffer.AnswerSession) []error
	Now          func() time.Time
}

type Stats struct {
	Running      bool      `json:"running"`
	Scans        uint64    `json:"scans"`
	Skipped      uint64    `json:"skipped"`
	Processed    uint64    `json:"processed"`
	Retried      uint64    `json:"retried"`
	LastStarted  time.Time `json:"last_started,omitempty"`
	LastFinished time.Time `json:"last_finished,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
	Ready        int       `json:"ready"`
}

type Scanner struct {
	interval     time.Duration
	maxSessions  int
	buf          *buffer.Buffer
	process      func(context.Context, buffer.AnswerSession) error
	processBatch func(context.Context, []buffer.AnswerSession) []error
	now          func() time.Time
	running      atomic.Bool
	mu           sync.RWMutex
	stats        Stats
	cancel       context.CancelFunc
	done         chan struct{}
}

func New(cfg Config) (*Scanner, error) {
	if cfg.Interval <= 0 || cfg.MaxSessions <= 0 || cfg.Buffer == nil || (cfg.Process == nil && cfg.ProcessBatch == nil) {
		return nil, ErrInvalidConfig
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Scanner{interval: cfg.Interval, maxSessions: cfg.MaxSessions, buf: cfg.Buffer, process: cfg.Process, processBatch: cfg.ProcessBatch, now: cfg.Now}, nil
}

// Start launches the ticker. Calling Start more than once is safe and does
// not create a second loop.
func (s *Scanner) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return nil
	}
	loopCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	s.mu.Unlock()
	go s.loop(loopCtx)
	return nil
}

func (s *Scanner) Stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// Cancel requests shutdown without waiting for an in-flight batch. The batch
// still uses its context and must finish independently before its session is
// completed or retried.
func (s *Scanner) Cancel() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ScanOnce runs at most one batch. If a previous batch is still running, it
// returns immediately with skipped=true.
func (s *Scanner) ScanOnce(ctx context.Context) (processed, retried int, skipped bool) {
	if !s.running.CompareAndSwap(false, true) {
		s.mu.Lock()
		s.stats.Skipped++
		s.mu.Unlock()
		return 0, 0, true
	}
	defer s.running.Store(false)
	now := s.now().UTC()
	s.mu.Lock()
	s.stats.Scans++
	s.stats.Running = true
	s.stats.LastStarted = now
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.stats.Running = false
		s.stats.Processed += uint64(processed)
		s.stats.Retried += uint64(retried)
		s.stats.LastFinished = s.now().UTC()
		s.mu.Unlock()
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	ready := s.buf.Ready(now)
	s.mu.Lock()
	s.stats.Ready = len(ready)
	s.mu.Unlock()
	if len(ready) > s.maxSessions {
		ready = ready[:s.maxSessions]
	}
	if s.processBatch != nil {
		errs := s.processBatch(ctx, ready)
		for i, candidate := range ready {
			var err error
			if i < len(errs) {
				err = errs[i]
			}
			if err != nil {
				_ = s.buf.Retry(candidate.Key)
				retried++
				s.mu.Lock()
				s.stats.LastError = err.Error()
				s.mu.Unlock()
				continue
			}
			if err := s.buf.Complete(candidate.Key); err != nil {
				retried++
				continue
			}
			processed++
		}
		return processed, retried, false
	}
	for _, candidate := range ready {
		if err := s.process(ctx, candidate); err != nil {
			_ = s.buf.Retry(candidate.Key)
			retried++
			s.mu.Lock()
			s.stats.LastError = err.Error()
			s.mu.Unlock()
			continue
		}
		if err := s.buf.Complete(candidate.Key); err != nil {
			retried++
			continue
		}
		processed++
	}
	return processed, retried, false
}

func (s *Scanner) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stats := s.stats
	stats.Running = s.running.Load()
	return stats
}

func (s *Scanner) loop(ctx context.Context) {
	s.mu.RLock()
	done := s.done
	s.mu.RUnlock()
	defer close(done)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.ScanOnce(ctx)
		}
	}
}
