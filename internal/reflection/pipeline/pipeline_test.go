package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/knowledge"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/reflection/buffer"
	"github.com/soleapi/forgebot/internal/reflection/reflector"
)

type fakeReflector struct {
	result reflector.Result
	called int
}

func (f *fakeReflector) Reflect(context.Context, buffer.AnswerSession) (reflector.Result, error) {
	f.called++
	return f.result, nil
}

type fakePublisher struct{ entries []knowledge.Entry }

func (f *fakePublisher) Publish(entry knowledge.Entry) (knowledge.PublishResult, error) {
	f.entries = append(f.entries, entry)
	return knowledge.PublishResult{Status: knowledge.Published}, nil
}

func session(id string) buffer.AnswerSession {
	return buffer.AnswerSession{Key: id, Question: structMessage("q")[0], Answers: structMessage("a"), StartedAt: time.Now()}
}

func structMessage(id string) []messaging.InboundMessage {
	return []messaging.InboundMessage{{ConversationID: "support", MessageID: id, Text: id}}
}

func TestProcessBatchReloadsOnce(t *testing.T) {
	r := &fakeReflector{result: reflector.Result{Accept: true, Title: "title", Question: "question", Answer: "answer", Confidence: .95}}
	p := &fakePublisher{}
	reloads := 0
	processor, err := New(Config{Reflector: r, Publisher: p, MinConfidence: .9, Reload: func(context.Context) error { reloads++; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	errs := processor.ProcessBatch(context.Background(), []buffer.AnswerSession{session("s1"), session("s2")})
	if errs[0] != nil || errs[1] != nil || len(p.entries) != 2 || reloads != 1 {
		t.Fatalf("errs=%v entries=%d reloads=%d", errs, len(p.entries), reloads)
	}
}

func TestProcessBatchRejectsWithoutReload(t *testing.T) {
	r := &fakeReflector{result: reflector.Result{Accept: false, Confidence: .2}}
	reloads := 0
	processor, _ := New(Config{Reflector: r, Publisher: &fakePublisher{}, MinConfidence: .9, Reload: func(context.Context) error { reloads++; return nil }})
	errs := processor.ProcessBatch(context.Background(), []buffer.AnswerSession{session("s1")})
	if errs[0] != nil || reloads != 0 {
		t.Fatalf("errs=%v reloads=%d", errs, reloads)
	}
}

func TestProcessBatchKeepsPublishedSessionsRetryableOnReloadFailure(t *testing.T) {
	r := &fakeReflector{result: reflector.Result{Accept: true, Title: "title", Question: "question", Answer: "answer", Confidence: .95}}
	processor, _ := New(Config{Reflector: r, Publisher: &fakePublisher{}, MinConfidence: .9, Reload: func(context.Context) error { return errors.New("offline") }})
	errs := processor.ProcessBatch(context.Background(), []buffer.AnswerSession{session("s1")})
	if errs[0] == nil || !errors.Is(errs[0], ErrReloadFailed) {
		t.Fatalf("err=%v", errs[0])
	}
}

func TestProcessBatchRetriesPendingReloadBeforeNewCandidates(t *testing.T) {
	r := &fakeReflector{result: reflector.Result{Accept: true, Title: "title", Question: "question", Answer: "answer", Confidence: .95}}
	publisher := &fakePublisher{}
	reloads := 0
	processor, _ := New(Config{Reflector: r, Publisher: publisher, MinConfidence: .9, Reload: func(context.Context) error {
		reloads++
		if reloads == 1 {
			return errors.New("offline")
		}
		return nil
	}})
	first := processor.ProcessBatch(context.Background(), []buffer.AnswerSession{session("s1")})
	if first[0] == nil {
		t.Fatal("first reload failure was lost")
	}
	second := processor.ProcessBatch(context.Background(), nil)
	if len(second) != 0 || reloads != 2 {
		t.Fatalf("second=%v reloads=%d", second, reloads)
	}
}
