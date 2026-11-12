package reflector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	answersession "github.com/soleapi/forgebot/internal/reflection/session"
)

type fakeClient struct {
	response llm.Response
	request  llm.Request
}

func (f *fakeClient) Protocol() llm.Protocol { return llm.OpenAIResponses }
func (f *fakeClient) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	f.request = request
	return f.response, nil
}
func (f *fakeClient) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

func testSession() answersession.AnswerSession {
	return answersession.AnswerSession{
		Key:       "support\x00admin\x00q1",
		Question:  messaging.InboundMessage{ConversationID: "support", MessageID: "q1", Text: "How do I rotate the key?"},
		Answers:   []messaging.InboundMessage{{ConversationID: "support", MessageID: "a1", Text: "Open the console and create a replacement key."}},
		StartedAt: time.Now(), LastAt: time.Now(),
	}
}

func TestReflectParsesStrictJSONAndDisablesTools(t *testing.T) {
	client := &fakeClient{response: llm.Response{Text: `{"accept":true,"title":"Rotate an API key","summary":"Replace an API key without interrupting clients.","body":"## Create a replacement key\n\nOpen the console and create a replacement key.","tags":["api-key"],"confidence":0.96,"reason":"reusable procedure"}`}}
	r, err := New(client, "model")
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Reflect(context.Background(), testSession())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accept || result.Confidence != 0.96 || result.Summary == "" || !strings.Contains(result.Body, "## Create a replacement key") {
		t.Fatalf("result=%+v", result)
	}
	if len(client.request.Tools) != 0 || client.request.Temperature == nil || *client.request.Temperature != 0 {
		t.Fatalf("request=%+v", client.request)
	}
	if client.request.Instructions == "" {
		t.Fatal("missing system instruction")
	}
}

func TestParseResultValidatesDocumentFields(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*Result)
	}{
		{"missing summary", func(r *Result) { r.Summary = " " }},
		{"missing body", func(r *Result) { r.Body = " " }},
		{"multiline title", func(r *Result) { r.Title = "Title\n## Another topic" }},
		{"multiline summary", func(r *Result) { r.Summary = "Summary\nAnother line" }},
		{"long summary", func(r *Result) { r.Summary = strings.Repeat("字", 1001) }},
		{"long body", func(r *Result) { r.Body = strings.Repeat("字", 12001) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := Result{Accept: true, Title: "Title", Summary: "Summary", Body: "A standalone paragraph.", Confidence: .95}
			test.edit(&result)
			if err := normalizeAndValidate(&result); !errors.Is(err, ErrInvalidResult) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	// Ordinary prose needs no section template; Markdown blocks remain intact.
	for _, body := range []string{"A standalone paragraph.", "## Configuration\n\n```json\n{\"enabled\": true}\n```"} {
		result := Result{Accept: true, Title: "Title", Summary: "Summary", Body: body, Confidence: .95}
		if err := normalizeAndValidate(&result); err != nil || result.Body != body {
			t.Fatalf("body=%q err=%v", result.Body, err)
		}
	}
}

func TestReflectRejectsInvalidAcceptedResult(t *testing.T) {
	client := &fakeClient{response: llm.Response{Text: `{"accept":true,"confidence":2}`}}
	r, _ := New(client, "model")
	if _, err := r.Reflect(context.Background(), testSession()); err == nil {
		t.Fatal("invalid result accepted")
	}
}

func TestReflectAllowsFencedJSONRejection(t *testing.T) {
	client := &fakeClient{response: llm.Response{Text: "```json\n{\"accept\":false,\"confidence\":0.2,\"reason\":\"chatty\"}\n```"}}
	r, _ := New(client, "model")
	result, err := r.Reflect(context.Background(), testSession())
	if err != nil || result.Accept {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
