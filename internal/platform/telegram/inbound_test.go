package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/operation"
)

func testMessage() message {
	return message{ID: 42, Date: time.Now().Unix(), Chat: chat{ID: -100, Type: "supergroup"}, From: user{ID: 7, FirstName: "Member"}, Text: "hello"}
}

func TestEntitiesUseOriginalUTF16AndBotCommandTargets(t *testing.T) {
	self := user{ID: 99, Username: "ForgeBot"}
	for _, tc := range []struct {
		name, text, want string
		entities         []entity
		mentioned, skip  bool
	}{
		{"emoji", "😀 @ForgeBot hello", "😀  hello", []entity{{Type: "mention", Offset: 3, Length: 9}}, true, false},
		{"unordered", "@ForgeBot x @ForgeBot", "x", []entity{{Type: "mention", Offset: 12, Length: 9}, {Type: "mention", Offset: 0, Length: 9}}, true, false},
		{"other mention", "@OtherBot hello", "@OtherBot hello", []entity{{Type: "mention", Offset: 0, Length: 9}}, false, false},
		{"text mention", "Me hello", "hello", []entity{{Type: "text_mention", Offset: 0, Length: 2, User: &self}}, true, false},
		{"own command", "/status@ForgeBot", "/status", []entity{{Type: "bot_command", Offset: 0, Length: 16}}, false, false},
		{"other command", "/status@OtherBot", "", []entity{{Type: "bot_command", Offset: 0, Length: 16}}, false, true},
		{"other without entity", "/status@OtherBot", "", nil, false, true},
		{"mention before command", "@ForgeBot /status@ForgeBot", "/status", []entity{{Type: "mention", Offset: 0, Length: 9}, {Type: "bot_command", Offset: 10, Length: 16}}, true, false},
		{"split surrogate", "😀 hello", "😀 hello", []entity{{Type: "text_mention", Offset: 1, Length: 1, User: &self}}, false, false},
		{"overflow", "hello", "hello", []entity{{Type: "mention", Offset: 1, Length: int(^uint(0) >> 1)}}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, mentioned, skip := normalizeText(tc.text, tc.entities, self)
			if text != tc.want || mentioned != tc.mentioned || skip != tc.skip {
				t.Fatal(text, mentioned, skip)
			}
		})
	}
}

func TestInboundBoundGroupsQuotesAndUnsupportedShapes(t *testing.T) {
	a := testAdapter(t)
	m := testMessage()
	reply := testMessage()
	reply.ID, reply.Text, reply.From = 40, "quoted answer", a.self
	m.Reply = &reply
	got, ok, err := a.inbound(m)
	if err != nil || !ok || !got.Mentioned || got.ReplyToMessageID != "40" || got.Text != "hello" || got.Quoted == nil || got.Quoted.Text != "quoted answer" || got.PromptSuffix != "connector\ngroup" {
		t.Fatal(got, ok, err)
	}
	reply.Chat.ID = -999
	got, ok, err = a.inbound(m)
	if err != nil || !ok || got.Quoted != nil || got.Mentioned {
		t.Fatal("cross-group quote retained", got, err)
	}
	for _, mutate := range []func(*message){
		func(m *message) { m.Chat.Type = "private" },
		func(m *message) { m.Chat.Type = "channel" },
		func(m *message) { m.Chat.IsForum = true },
		func(m *message) { m.IsTopic = true },
		func(m *message) { m.Chat.ID = -999 },
		func(m *message) { m.SenderChat = &m.Chat },
		func(m *message) { m.From.IsBot = true },
		func(m *message) { m.From.ID = 99 },
		func(m *message) { m.NewMembers = []user{{ID: 8}} },
	} {
		m := testMessage()
		mutate(&m)
		if got, ok, err := a.inbound(m); err != nil || ok {
			t.Fatal(got, ok, err)
		}
	}
	m = testMessage()
	m.Photo = []photoSize{{FileID: "huge", Width: 4000, Height: 4000, FileSize: maxImageBytes + 1}}
	if _, _, err := a.inbound(m); operation.Describe(err).Code != operation.Invalid {
		t.Fatal("oversized photo silently lost", err)
	}
	m = testMessage()
	m.Date = 0
	if _, _, err := a.inbound(m); operation.Describe(err).Code != operation.Invalid {
		t.Fatal(err)
	}
	m = testMessage()
	m.From.IsBot, m.MigrateTo = true, -1001234567890
	if _, _, err := a.inbound(m); operation.Describe(err).Code != operation.Conflict {
		t.Fatal("group migration service message was silently filtered", err)
	}
}

func TestRoleCacheExpiresConservativelyAndMembershipInvalidates(t *testing.T) {
	a := testAdapter(t)
	now := time.Now()
	a.now = func() time.Time { return now }
	calls, offline := 0, false
	a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/getChatAdministrators") {
			t.Fatal(r.URL.Path)
		}
		calls++
		if offline {
			return response(503, `{"ok":false}`), nil
		}
		return response(200, `{"ok":true,"result":[{"status":"creator","user":{"id":7}},{"status":"administrator","user":{"id":8}}]}`), nil
	})
	if a.role(context.Background(), -100, 7) != "owner" || a.role(context.Background(), -100, 8) != "admin" || calls != 1 {
		t.Fatal(calls)
	}
	now = now.Add(6 * time.Minute)
	offline = true
	if a.role(context.Background(), -100, 7) != "member" || a.role(context.Background(), -100, 8) != "member" || calls != 2 {
		t.Fatal("expired admin survived failure", calls)
	}
	var change update
	_ = json.Unmarshal([]byte(`{"update_id":99,"my_chat_member":{"chat":{"id":-100,"type":"supergroup"}}}`), &change)
	if err := a.handle(context.Background(), nil, change); err != nil {
		t.Fatal(err)
	}
	offline = false
	if a.role(context.Background(), -100, 7) != "owner" || calls != 3 {
		t.Fatal("membership did not invalidate roles", calls)
	}
}
