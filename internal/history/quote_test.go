package history

import (
	"testing"

	"github.com/soleapi/forgebot/internal/messaging"
)

func TestQuotedTextIsBoundedAndReturnedByValue(t *testing.T) {
	w, _ := newTestWindow(Policy{MaxTextChars: 3})
	input := messaging.InboundMessage{ConversationID: "g", MessageID: "a", ReplyToMessageID: "q", Text: "回答",
		Quoted: &messaging.QuotedMessage{MessageID: "q", Text: "这是引用问题", Media: []messaging.Media{{URL: "https://unused.invalid/image"}}}}
	var event Record
	w.SetSubscribers([]Subscriber{{ID: "quote", Appended: func(record Record) { event = record; record.QuotedText = "changed" }}})
	stored, ok := w.Append(FromInbound(input))
	if !ok || stored.QuotedText != "这是引" || event.QuotedText != "这是引" || stored.QuotedMessageID != "q" {
		t.Fatalf("stored=%+v event=%+v", stored, event)
	}
	input.Quoted.Text = "changed"
	recent := w.Recent("g", 0)
	recent[0].QuotedText = "changed"
	found, ok := w.Find("g", "a")
	if !ok || found.QuotedText != "这是引" {
		t.Fatal(found, ok)
	}
	since, _ := w.Since("g", 0)
	if since[0].QuotedText != "这是引" {
		t.Fatal(since)
	}
	input.Quoted.MessageID = "different"
	if record := FromInbound(input); record.QuotedText != "" || record.QuotedMessageID != "" {
		t.Fatal("mismatched quote retained", record)
	}
}
