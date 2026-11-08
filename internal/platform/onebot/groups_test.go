package onebot

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/platform"
)

func TestGroupReviewUsesOneBotWithoutProbingSupport(t *testing.T) {
	var paths []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if r.URL.Path == "/set_group_add_request" && (payload["flag"] != "opaque" || payload["sub_type"] != "add" || payload["approve"] != true) {
			t.Error("incorrect review payload", payload)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "retcode": 0, "data": nil})
	}))
	defer api.Close()
	s := &Sender{cfg: testConfig(api.URL), client: api.Client(), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	registry := platform.NewRegistry()
	if err := registry.RegisterConnector("one", s); err != nil {
		t.Fatal(err)
	}
	if err := registry.BindConversation(platform.ConversationBinding{ConversationID: "g", ConnectorID: "one", Target: "8"}); err != nil {
		t.Fatal(err)
	}
	support, err := registry.Support(context.Background(), "g")
	if err != nil || !support.JoinEvents || !support.MemberEvents || !support.JoinReviewReason || support.Permission != "unknown" || len(paths) != 0 {
		t.Fatal("support query is incorrect or probed network", support, err)
	}
	reference, _ := json.Marshal(requestReference{Flag: "opaque", SubType: "add", Target: "8"})
	if err := s.ReviewGroupRequest(context.Background(), "8", group.ReviewRequest{Reference: string(reference), Approve: true}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "/set_group_add_request" {
		t.Fatal(paths)
	}
}

func TestJoinReviewReasonOnlySentForRejection(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		_, hasReason := payload["reason"]
		if payload["approve"] == true && hasReason {
			t.Error("approval included rejection reason", payload)
		}
		if payload["approve"] == false && payload["reason"] != "请填写申请说明" {
			t.Error(payload)
		}
		_, _ = io.WriteString(w, `{"status":"ok","retcode":0,"data":null}`)
	}))
	defer api.Close()
	s := &Sender{cfg: testConfig(api.URL), client: api.Client()}
	reference, _ := json.Marshal(requestReference{Flag: "opaque", SubType: "add", Target: "8"})
	for _, approve := range []bool{true, false} {
		if err := s.ReviewGroupRequest(context.Background(), "8", group.ReviewRequest{Reference: string(reference), Approve: approve, Reason: "请填写申请说明"}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestGroupRequestAndActualMemberJoinAreDistinct(t *testing.T) {
	for _, raw := range []string{
		`{"post_type":"request","request_type":"group","sub_type":"add","group_id":8,"user_id":7,"self_id":99,"time":1700000000,"flag":"opaque","comment":"hello"}`,
		`{"post_type":"notice","notice_type":"group_increase","sub_type":"approve","group_id":8,"user_id":7,"self_id":99,"time":1700000001}`,
	} {
		var event rawEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		mapped, handled, err := event.groupEvent(testConfig(""))
		if err != nil || !handled || mapped.ID == "" || mapped.ConversationID != "support" {
			t.Fatal(mapped, handled, err)
		}
		if event.PostType == "request" && (mapped.Kind != group.JoinRequested || mapped.Reference == "") {
			t.Fatal(mapped)
		}
		if event.PostType == "notice" && (mapped.Kind != group.MemberJoined || mapped.Reference != "") {
			t.Fatal(mapped)
		}
	}
}

type groupEventRecorder struct {
	handlerFunc
	events []group.Event
}

func (r *groupEventRecorder) HandleGroupEvent(_ context.Context, event group.Event) (group.EventResult, error) {
	r.events = append(r.events, event)
	return group.EventResult{Accepted: true, Verdict: "approved"}, nil
}

// 入群申请的内容在 DEBUG 日志中可见，便于排查规则为何不匹配；审批句柄 flag 不进日志。
func TestJoinRequestDebugLogShowsCommentButNeverFlag(t *testing.T) {
	var logs bytes.Buffer
	a, err := New(testConfig("http://127.0.0.1:5700"), slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}
	recorder := &groupEventRecorder{}
	body := mustJSON(t, map[string]any{
		"post_type": "request", "request_type": "group", "sub_type": "add", "group_id": 8, "user_id": 7,
		"self_id": 99, "time": time.Now().Unix(), "flag": "secret-approval-flag", "comment": "问题：暗号\n答案：ABCDE12345",
	})
	rr := httptest.NewRecorder()
	a.webhook(recorder)(rr, httptest.NewRequest(http.MethodPost, "/onebot", bytes.NewReader(body)))
	if rr.Code != http.StatusOK || len(recorder.events) != 1 {
		t.Fatalf("status=%d events=%d", rr.Code, len(recorder.events))
	}
	output := logs.String()
	for _, want := range []string{"onebot group event received", "request_type=group", "sub_type=add", "user_id=7", "答案：ABCDE12345", "conversation_id=support"} {
		if !strings.Contains(output, want) {
			t.Fatalf("debug log missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "secret-approval-flag") {
		t.Fatalf("approval flag leaked into logs:\n%s", output)
	}
}

// 入群通知只带 user_id；适配器查询群名片，其次昵称，查询失败时留空由欢迎语退回用户 ID。
func TestMemberJoinedResolvesDisplayName(t *testing.T) {
	cases := []struct {
		name, response, want string
	}{
		{"card", `{"status":"ok","retcode":0,"data":{"card":"小明","nickname":"nick"}}`, "小明"},
		{"nickname", `{"status":"ok","retcode":0,"data":{"card":"","nickname":"nick"}}`, "nick"},
		{"failed", `{"status":"failed","retcode":1200}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var asked map[string]any
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/get_group_member_info" {
					t.Errorf("unexpected API %s", r.URL.Path)
				}
				_ = json.NewDecoder(r.Body).Decode(&asked)
				_, _ = w.Write([]byte(tc.response))
			}))
			defer api.Close()
			a, err := New(testConfig(api.URL), slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			recorder := &groupEventRecorder{}
			body := mustJSON(t, map[string]any{
				"post_type": "notice", "notice_type": "group_increase", "sub_type": "approve",
				"group_id": 8, "user_id": 7, "self_id": 99, "time": time.Now().Unix(),
			})
			rr := httptest.NewRecorder()
			a.webhook(recorder)(rr, httptest.NewRequest(http.MethodPost, "/onebot", bytes.NewReader(body)))
			if rr.Code != http.StatusOK || len(recorder.events) != 1 {
				t.Fatalf("status=%d events=%d", rr.Code, len(recorder.events))
			}
			if got := recorder.events[0].UserName; got != tc.want {
				t.Fatalf("UserName = %q, want %q", got, tc.want)
			}
			if asked["group_id"] != float64(8) || asked["user_id"] != float64(7) {
				t.Fatal("unexpected member lookup payload", asked)
			}
		})
	}
}

// QQ 把验证问题拼成“问题：…\n答案：…”；适配器只交出答案，其余内容原样保留。
func TestJoinAnswerExtractsOnlyTheApplicantAnswer(t *testing.T) {
	for _, tc := range []struct{ name, comment, want string }{
		{"real sample from QQ", "问题：1111\n答案：ABCDE1ABCD", "ABCDE1ABCD"},
		{"crlf line break", "问题：暗号\r\n答案：abc", "abc"},
		{"no verification question", "我想加入学习", "我想加入学习"},
		{"empty comment", "", ""},
		{"empty answer", "问题：暗号\n答案：", ""},
		{"question without answer marker", "问题：暗号", "问题：暗号"},
		{"answer is not trimmed", "问题：暗号\n答案： abc ", " abc "},
		{"multiline answer keeps later lines", "问题：暗号\n答案：第一行\n第二行", "第一行\n第二行"},
		// 以第一处分隔为界：申请人无法在答案里伪造另一段“答案：”来改变被匹配的内容。
		{"forged marker inside the answer", "问题：暗号\n答案：随便\n答案：ABCDE1ABCD", "随便\n答案：ABCDE1ABCD"},
		{"answer text without question prefix", "答案：ABCDE1ABCD", "答案：ABCDE1ABCD"},
	} {
		if got := joinAnswer(tc.comment); got != tc.want {
			t.Errorf("%s: joinAnswer(%q) = %q, want %q", tc.name, tc.comment, got, tc.want)
		}
	}
}

// 映射后的入群事件携带答案；DEBUG 日志同时给出原始 comment 与提取后的 application。
func TestJoinRequestEventCarriesExtractedAnswer(t *testing.T) {
	var logs bytes.Buffer
	a, err := New(testConfig("http://127.0.0.1:5700"), slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}
	recorder := &groupEventRecorder{}
	body := mustJSON(t, map[string]any{
		"post_type": "request", "request_type": "group", "sub_type": "add", "group_id": 8, "user_id": 7,
		"self_id": 99, "time": time.Now().Unix(), "flag": "opaque", "comment": "问题：1111\n答案：ABCDE1ABCD",
	})
	rr := httptest.NewRecorder()
	a.webhook(recorder)(rr, httptest.NewRequest(http.MethodPost, "/onebot", bytes.NewReader(body)))
	if len(recorder.events) != 1 || recorder.events[0].Comment != "ABCDE1ABCD" {
		t.Fatalf("event comment = %+v", recorder.events)
	}
	if !strings.Contains(logs.String(), `application=ABCDE1ABCD`) || !strings.Contains(logs.String(), `答案：ABCDE1ABCD`) {
		t.Fatalf("debug log should show both raw comment and extracted answer:\n%s", logs.String())
	}
}
