package llm

import (
	"context"
	"testing"
	"time"
)

func TestModelTimeoutStillAppliesWithParentDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	child, stop := contextWithTimeout(parent, time.Second)
	defer stop()
	outer, _ := parent.Deadline()
	inner, ok := child.Deadline()
	if !ok || !inner.Before(outer) {
		t.Fatal("parent run deadline disabled model timeout")
	}
	shorter, end := contextWithTimeout(child, time.Hour)
	defer end()
	shortest, _ := shorter.Deadline()
	if !shortest.Equal(inner) {
		t.Fatal("model timeout extended parent deadline")
	}
}
