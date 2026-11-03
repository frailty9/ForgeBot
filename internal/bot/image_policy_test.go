package bot

import (
	"context"
	"testing"

	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/operation"
)

func TestDisabledImageUnderstandingNeverCallsModel(t *testing.T) {
	for _, quoted := range []bool{false, true} {
		for _, text := range []string{"", "what is wrong?"} {
			fake := &fakeLLM{response: llm.Response{Text: "must not run"}}
			sender := newRecordedSender()
			policy := testPolicy()
			policy.ImageUnderstanding = false
			engine, err := newTestEngine(context.Background(), Dependencies{Sender: sender, LLM: fake, Policy: policy}, nil)
			if err != nil {
				t.Fatal(err)
			}
			message := testInbound(text)
			media := []messaging.Media{{Kind: messaging.MediaImage, URL: "https://example.invalid/image.png"}}
			if quoted {
				message.Quoted = &messaging.QuotedMessage{Media: media}
			} else {
				message.Media = media
			}
			result, err := engine.HandleInbound(context.Background(), message)
			if err != nil || !result.Accepted || len(fake.requests) != 0 {
				t.Fatalf("result=%+v requests=%d err=%v", result, len(fake.requests), err)
			}
			if sent := sender.Sent(); len(sent) != 1 || sent[0].Text != "当前暂不支持图片" {
				t.Fatalf("sent=%+v", sent)
			}
			message.MessageID = "unaddressed"
			message.Mentioned = false
			message.Command = false
			result, err = engine.HandleInbound(context.Background(), message)
			if err != nil || result.Verdict != "ignored" || len(sender.Sent()) != 1 {
				t.Fatalf("unsolicited image response: %+v %v", result, err)
			}
			engine.Shutdown(0)
		}
	}
}

func TestMissingModelNeverEchoes(t *testing.T) {
	engine, err := NewEngine(context.Background(), Dependencies{Sender: newRecordedSender(), Policy: testPolicy()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown(0)
	_, err = engine.HandleInbound(context.Background(), testInbound("hello"))
	if err == nil || operation.Describe(err).Code != operation.NotConfigured {
		t.Fatalf("missing LLM: %v", err)
	}
}
