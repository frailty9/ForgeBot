package qqbot

import (
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
)

func eventWithMentions(t *testing.T, a *Adapter, kind, text string, mentions []mention) []byte {
	t.Helper()
	var envelope payload
	if err := json.Unmarshal(eventBody(a, "mention", kind, "member", text), &envelope); err != nil {
		t.Fatal(err)
	}
	var data groupMessage
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatal(err)
	}
	data.Mentions = mentions
	var err error
	if envelope.Data, err = json.Marshal(data); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestFullGroupMessageMentionsUsePlatformIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, text, wantText string
		mentions             []mention
		mentioned, command   bool
	}{
		{name: "ordinary", text: "hello", wantText: "hello"},
		{name: "everyone", text: "<@all> hello", wantText: "<@all> hello", mentions: []mention{{ID: "all"}}},
		{name: "other bot", text: "<@OTHER> hello", wantText: "<@OTHER> hello", mentions: []mention{{ID: "OTHER"}}},
		{name: "unconfirmed tag", text: "<@BOT> hello", wantText: "<@BOT> hello"},
		{name: "appid is not self identity", text: "<@app> hello", wantText: "<@app> hello", mentions: []mention{{ID: "app"}}},
		{name: "self member openid", text: "<@BOT> hello", wantText: "hello", mentions: []mention{{MemberOpenID: "BOT", IsYou: true}}, mentioned: true},
		{name: "self id", text: "<@!BOT> hello", wantText: "hello", mentions: []mention{{ID: "BOT", IsYou: true}}, mentioned: true},
		{name: "preserve other mentions", text: "<@BOT> <@OTHER> <@all> hello", wantText: "<@OTHER> <@all> hello", mentions: []mention{{ID: "OTHER"}, {ID: "BOT", IsYou: true}}, mentioned: true},
		{name: "self without tag id", text: "hello", wantText: "hello", mentions: []mention{{IsYou: true}}, mentioned: true},
		{name: "command after self mention", text: " <@BOT> /status ", wantText: "/status", mentions: []mention{{ID: "BOT", IsYou: true}}, mentioned: true, command: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testAdapter(t)
			dispatch(t, a, eventWithMentions(t, a, "GROUP_MESSAGE_CREATE", tc.text, tc.mentions), 0)
			message := <-a.queue
			if message.Mentioned != tc.mentioned || message.Command != tc.command || message.Text != tc.wantText {
				t.Fatalf("message=%+v", message)
			}
		})
	}
}

func TestExplicitMentionSurvivesDuplicateEventOrder(t *testing.T) {
	for _, order := range []string{"full first", "at first", "concurrent"} {
		t.Run(order, func(t *testing.T) {
			a := testAdapter(t)
			full := eventWithMentions(t, a, "GROUP_MESSAGE_CREATE", "<@BOT> question", []mention{{MemberOpenID: "BOT", IsYou: true}})
			at := eventWithMentions(t, a, "GROUP_AT_MESSAGE_CREATE", "question", nil)
			switch order {
			case "full first":
				dispatch(t, a, full, 0)
				dispatch(t, a, at, 0)
			case "at first":
				dispatch(t, a, at, 0)
				dispatch(t, a, full, 0)
			case "concurrent":
				var wg sync.WaitGroup
				for _, body := range [][]byte{full, at} {
					wg.Add(1)
					go func() {
						defer wg.Done()
						w := httptest.NewRecorder()
						a.webhook(w, request(a, body))
						var ack struct{ Op, D int }
						if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &ack) != nil || ack.Op != 12 || ack.D != 0 {
							t.Errorf("response=%d %s", w.Code, w.Body.String())
						}
					}()
				}
				wg.Wait()
			}
			if len(a.queue) != 1 {
				t.Fatalf("queued=%d, want exactly one message", len(a.queue))
			}
			message := <-a.queue
			if !message.Mentioned || message.Text != "question" {
				t.Fatalf("explicit mention lost: %+v", message)
			}
		})
	}
}
