// Package pipeline coordinates reflection, publication and one reload per
// scanner batch. It contains no scheduling or platform-specific code.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/soleapi/forgebot/internal/knowledge"
	"github.com/soleapi/forgebot/internal/reflection/reflector"
	answersession "github.com/soleapi/forgebot/internal/reflection/session"
)

var ErrReloadFailed = errors.New("reflection search reload failed")

type Reflector interface {
	Reflect(context.Context, answersession.AnswerSession) (reflector.Result, error)
}

type Publisher interface {
	Publish(knowledge.Entry) (knowledge.PublishResult, error)
}

type Reload func(context.Context) error

type Config struct {
	Reflector     Reflector
	Publisher     Publisher
	Reload        Reload
	MinConfidence float64
}

type Processor struct {
	reflector     Reflector
	publisher     Publisher
	reload        Reload
	min           float64
	mu            sync.RWMutex
	stats         Stats
	reloadPending bool
}

type Stats struct {
	Candidates    uint64 `json:"candidates"`
	Rejected      uint64 `json:"rejected"`
	LowConfidence uint64 `json:"low_confidence"`
	Published     uint64 `json:"published"`
	Duplicates    uint64 `json:"duplicates"`
	Failed        uint64 `json:"failed"`
	ReloadFailed  uint64 `json:"reload_failed"`
}

var ErrNoPublishedDocument = errors.New("reflection batch produced no new document")

// New validates the reflection pipeline dependencies and limits.
func New(cfg Config) (*Processor, error) {
	if cfg.Reflector == nil || cfg.Publisher == nil || cfg.Reload == nil || cfg.MinConfidence <= 0 || cfg.MinConfidence > 1 {
		return nil, errors.New("reflection pipeline configuration is invalid")
	}
	return &Processor{reflector: cfg.Reflector, publisher: cfg.Publisher, reload: cfg.Reload, min: cfg.MinConfidence}, nil
}

// ProcessBatch returns one error per input session. A nil error means the
// scanner may complete that session. Rejected or duplicate candidates are
// successful terminal outcomes and therefore also return nil.
// ProcessBatch reflects eligible sessions, publishes accepted entries, and
// reloads search once after any successful publication.
func (p *Processor) ProcessBatch(ctx context.Context, sessions []answersession.AnswerSession) []error {
	errs := make([]error, len(sessions))
	p.mu.RLock()
	pending := p.reloadPending
	p.mu.RUnlock()
	if pending {
		if err := p.reload(ctx); err != nil {
			for i := range errs {
				errs[i] = fmt.Errorf("%w: %v", ErrReloadFailed, err)
			}
			p.increment(func(stats *Stats) { stats.ReloadFailed++ })
			return errs
		}
		p.mu.Lock()
		p.reloadPending = false
		p.mu.Unlock()
	}
	published := make([]int, 0, len(sessions))
	p.mu.Lock()
	p.stats.Candidates += uint64(len(sessions))
	p.mu.Unlock()
	for i, session := range sessions {
		result, err := p.reflector.Reflect(ctx, session)
		if err != nil {
			errs[i] = err
			p.increment(func(stats *Stats) { stats.Failed++ })
			continue
		}
		if !result.Accept {
			p.increment(func(stats *Stats) { stats.Rejected++ })
			continue
		}
		if result.Confidence < p.min {
			p.increment(func(stats *Stats) { stats.LowConfidence++ })
			continue
		}
		entry := knowledge.Entry{Title: result.Title, Question: result.Question, Answer: result.Answer, Tags: result.Tags,
			ConversationID: session.Question.ConversationID, SourceQuestionMessageID: session.Question.MessageID,
			SourceAnswerMessageID: session.Answers[0].MessageID, CreatedAt: session.StartedAt}
		publishResult, err := p.publisher.Publish(entry)
		if err != nil {
			errs[i] = err
			p.increment(func(stats *Stats) { stats.Failed++ })
			continue
		}
		if publishResult.Status == knowledge.Duplicate {
			p.increment(func(stats *Stats) { stats.Duplicates++ })
			continue
		}
		p.increment(func(stats *Stats) { stats.Published++ })
		published = append(published, i)
	}
	if len(published) == 0 {
		return errs
	}
	if err := p.reload(ctx); err != nil {
		p.mu.Lock()
		p.reloadPending = true
		p.mu.Unlock()
		p.increment(func(stats *Stats) { stats.ReloadFailed++ })
		for _, index := range published {
			errs[index] = fmt.Errorf("%w: %v", ErrReloadFailed, err)
		}
	}
	return errs
}

func (p *Processor) Stats() Stats {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.stats
}

func (p *Processor) increment(update func(*Stats)) {
	p.mu.Lock()
	update(&p.stats)
	p.mu.Unlock()
}
