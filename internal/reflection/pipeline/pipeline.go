// Package pipeline coordinates reflection, publication and one reload per
// scanner batch. It contains no scheduling or platform-specific code.
package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/soleapi/forgebot/internal/knowledge"
	"github.com/soleapi/forgebot/internal/reflection/buffer"
	"github.com/soleapi/forgebot/internal/reflection/reflector"
)

var ErrReloadFailed = errors.New("reflection search reload failed")

type Reflector interface {
	Reflect(context.Context, buffer.AnswerSession) (reflector.Result, error)
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
	reflector Reflector
	publisher Publisher
	reload    Reload
	min       float64
}

func New(cfg Config) (*Processor, error) {
	if cfg.Reflector == nil || cfg.Publisher == nil || cfg.Reload == nil || cfg.MinConfidence <= 0 || cfg.MinConfidence > 1 {
		return nil, errors.New("reflection pipeline configuration is invalid")
	}
	return &Processor{reflector: cfg.Reflector, publisher: cfg.Publisher, reload: cfg.Reload, min: cfg.MinConfidence}, nil
}

// ProcessBatch returns one error per input session. A nil error means the
// scanner may complete that session. Rejected or duplicate candidates are
// successful terminal outcomes and therefore also return nil.
func (p *Processor) ProcessBatch(ctx context.Context, sessions []buffer.AnswerSession) []error {
	errs := make([]error, len(sessions))
	published := make([]int, 0, len(sessions))
	entries := make([]knowledge.Entry, len(sessions))
	for i, session := range sessions {
		result, err := p.reflector.Reflect(ctx, session)
		if err != nil {
			errs[i] = err
			continue
		}
		if !result.Accept || result.Confidence < p.min {
			continue
		}
		entry := knowledge.Entry{Title: result.Title, Question: result.Question, Answer: result.Answer, Tags: result.Tags,
			ConversationID: session.Question.ConversationID, SourceQuestionMessageID: session.Question.MessageID,
			SourceAnswerMessageID: session.Answers[0].MessageID, CreatedAt: session.StartedAt}
		entries[i] = entry
		if _, err := p.publisher.Publish(entry); err != nil {
			errs[i] = err
			continue
		}
		published = append(published, i)
	}
	if len(published) == 0 {
		return errs
	}
	if err := p.reload(ctx); err != nil {
		for _, index := range published {
			errs[index] = fmt.Errorf("%w: %v", ErrReloadFailed, err)
		}
	}
	return errs
}
