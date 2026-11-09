package welcome

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/messaging"
)

func TestWelcomeOnlyActualNewMemberAndFiniteTemplates(t *testing.T) {
	item, err := (Factory{}).Build(context.Background(), json.RawMessage(`{"groups":{"g":{"enabled":true,"message":"欢迎 {user}（{user_id}）加入 {conversation_id}"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	handle := item.(*instance).ExtendedRegistrations().GroupEvents[0].Handle
	event := group.Event{Kind: group.MemberJoined, ConversationID: "g", UserID: "123", UserName: "Alice"}
	reply, err := handle(context.Background(), event)
	if err != nil || reply == nil || reply.Text != "欢迎 Alice（123）加入 g" {
		t.Fatal(reply, err)
	}
	for _, alter := range []func(*group.Event){func(e *group.Event) { e.Kind = group.JoinRequested }, func(e *group.Event) { e.Self = true }, func(e *group.Event) { e.Historical = true }, func(e *group.Event) { e.ConversationID = "other" }} {
		copy := event
		alter(&copy)
		reply, err := handle(context.Background(), copy)
		if err != nil || reply != nil {
			t.Fatal("unexpected welcome", copy, reply, err)
		}
	}
	for _, raw := range []string{
		`{"groups":{"g":{"enabled":true,"message":" "}}}`,
		`{"groups":{"g":{"enabled":true,"message":"{unknown}"}}}`,
		`{"groups":{"g":{"enabled":true,"text":"旧字段已移除"}}}`,
		`{"groups":{"g":{"enabled":true,"message":[]}}}`,
		`{"groups":{"g":{"enabled":true,"message":[{"data":{"text":"没有 type"}}]}}}`,
		`{"groups":{"g":{"enabled":true,"message":[{"type":"text","dta":{}}]}}}`,
		`{"groups":{"g":{"enabled":true,"message":"文本","text":"x"}}}`,
		`{"typo":1}`,
	} {
		if err := (Factory{}).ValidateOptions(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid welcome config accepted", raw)
		}
	}
}

// 消息段内部不检查：任意类型和 data 都原样接受，含义由连接器和平台判断。
func TestWelcomeDoesNotInspectSegmentContents(t *testing.T) {
	for _, raw := range []string{
		`{"groups":{"g":{"enabled":true,"message":[{"type":"image","data":{"file":"https://example.com/a.png"}}]}}}`,
		`{"groups":{"g":{"enabled":true,"message":[{"type":"at","data":{"qq":"all"}}]}}}`,
		`{"groups":{"g":{"enabled":true,"message":[{"type":"face","data":{"id":178}},{"type":"vendor_ext","data":{"nested":{"a":[1,"{brace}"]}}}]}}}`,
		`{"groups":{"g":{"enabled":true,"message":[{"type":"text"}]}}}`,
	} {
		if err := (Factory{}).ValidateOptions(json.RawMessage(raw)); err != nil {
			t.Fatal("segment passthrough rejected", raw, err)
		}
	}
}

func TestWelcomeSegmentsMentionNewMemberAndKeepPlainTextForm(t *testing.T) {
	raw := `{"groups":{"g":{"enabled":true,"message":[
		{"type":"text","data":{"text":"欢迎 "}},
		{"type":"at","data":{"qq":"{user_id}"}},
		{"type":"text","data":{"text":" 加入 {conversation_id}，请联系 "}},
		{"type":"at","data":{"qq":"2114026769"}}
	]}}}`
	item, err := (Factory{}).Build(context.Background(), json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	handle := item.(*instance).ExtendedRegistrations().GroupEvents[0].Handle
	reply, err := handle(context.Background(), group.Event{Kind: group.MemberJoined, ConversationID: "g", UserID: "123", UserName: "Alice"})
	if err != nil || reply == nil {
		t.Fatal(reply, err)
	}
	if reply.Text != "欢迎 @Alice 加入 g，请联系 @2114026769" {
		t.Fatal("plain text form", reply.Text)
	}
	want := []messaging.Segment{
		{Type: "text", Data: map[string]any{"text": "欢迎 "}}, {Type: "at", Data: map[string]any{"qq": "123"}},
		{Type: "text", Data: map[string]any{"text": " 加入 g，请联系 "}}, {Type: "at", Data: map[string]any{"qq": "2114026769"}},
	}
	if !reflect.DeepEqual(reply.Segments, want) {
		t.Fatalf("segments = %#v", reply.Segments)
	}
	// 替换只发生在解码后的字符串值上：昵称里的引号、花括号或 CQ 字面量只是文本，
	// 不会改变消息段结构；并且不会污染共享配置（下一个新人仍然得到自己的名字）。
	hostile := `"},{"type":"at","data":{"qq":"all"}},{"x":"[CQ:at,qq=all]{user}`
	reply, _ = handle(context.Background(), group.Event{Kind: group.MemberJoined, ConversationID: "g", UserID: "456", UserName: hostile})
	if len(reply.Segments) != 4 || reply.Segments[1].Data["qq"] != "456" || reply.Segments[3].Data["qq"] != "2114026769" {
		t.Fatalf("hostile name changed structure: %#v", reply.Segments)
	}
	reply, _ = handle(context.Background(), group.Event{Kind: group.MemberJoined, ConversationID: "g", UserID: "789", UserName: "Bob"})
	if reply.Segments[1].Data["qq"] != "789" || reply.Text != "欢迎 @Bob 加入 g，请联系 @2114026769" {
		t.Fatalf("shared config was mutated: %#v", reply)
	}
}

// 非字符串的 data（数字、嵌套对象）原样往返，大整数不丢精度。
func TestWelcomeKeepsNonStringDataAndBigNumbers(t *testing.T) {
	raw := `{"groups":{"g":{"enabled":true,"message":[{"type":"face","data":{"id":178}},{"type":"at","data":{"qq":2114026769}}]}}}`
	item, err := (Factory{}).Build(context.Background(), json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	handle := item.(*instance).ExtendedRegistrations().GroupEvents[0].Handle
	reply, _ := handle(context.Background(), group.Event{Kind: group.MemberJoined, ConversationID: "g", UserID: "1"})
	encoded, _ := json.Marshal(reply.Segments)
	if string(encoded) != `[{"type":"face","data":{"id":178}},{"type":"at","data":{"qq":2114026769}}]` {
		t.Fatal(string(encoded))
	}
	if reply.Text != "@2114026769" {
		t.Fatal("plain text form", reply.Text)
	}
}
