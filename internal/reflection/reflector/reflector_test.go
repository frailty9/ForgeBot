package reflector

import (
	"context"
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
	client := &fakeClient{response: llm.Response{Text: `{"accept":true,"title":"Rotate an API key","question":"How do I rotate the key?","answer":"Create a replacement key, update clients, then revoke the old key.","tags":["api-key"],"confidence":0.96,"reason":"reusable procedure"}`}}
	r, err := New(client, "model")
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Reflect(context.Background(), testSession())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accept || result.Confidence != 0.96 || result.Title == "" {
		t.Fatalf("result=%+v", result)
	}
	if len(client.request.Tools) != 0 || client.request.Temperature == nil || *client.request.Temperature != 0 {
		t.Fatalf("request=%+v", client.request)
	}
	if client.request.Instructions == "" {
		t.Fatal("missing system instruction")
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
