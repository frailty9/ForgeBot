package decision

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestDecisionConcurrencyWaitHonorsParentCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s, err := New([]Task{sampleTask()}, Options{MaxConcurrent: 1, LLM: providerFunc(func(context.Context, ProviderRequest) (Answer, error) {
		calls.Add(1)
		close(entered)
		<-release
		return Answer{"action": "allow"}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.Decide(context.Background(), sampleRequest()); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("first decision never entered provider")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = s.Decide(ctx, sampleRequest())
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatal(err, calls.Load())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
