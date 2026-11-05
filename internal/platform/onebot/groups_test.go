package onebot

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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
