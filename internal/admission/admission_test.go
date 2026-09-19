package admission_test

import (
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/admission"
	"github.com/soleapi/forgebot/internal/messaging"
)

func TestCheckerDeduplicatesByMessageID(t *testing.T) {
	checker := admission.New(time.Minute, 100)
	message := messaging.InboundMessage{ConversationID: "c", UserID: "u", MessageID: "m", Text: "hello"}
	if checker.Check(message) != admission.Pass {
		t.Fatal("first message rejected")
	}
	if checker.Check(message) != admission.Duplicate {
		t.Fatal("duplicate message passed")
	}
	if checker.Duplicates() != 1 {
		t.Fatalf("duplicates = %d", checker.Duplicates())
	}
}

func TestCheckerDoesNotCollapseEqualTextWithDistinctIDs(t *testing.T) {
	checker := admission.New(time.Minute, 100)
	first := messaging.InboundMessage{ConversationID: "c", UserID: "u", MessageID: "m1", Text: "same"}
	second := first
	second.MessageID = "m2"
	if checker.Check(first) != admission.Pass || checker.Check(second) != admission.Pass {
		t.Fatal("distinct platform messages were collapsed")
	}
}

func TestCheckerFallsBackToTextWithoutMessageID(t *testing.T) {
	checker := admission.New(time.Minute, 100)
	message := messaging.InboundMessage{ConversationID: "c", UserID: "u", Text: "same"}
	if checker.Check(message) != admission.Pass || checker.Check(message) != admission.Duplicate {
		t.Fatal("text fallback did not deduplicate")
	}
}
