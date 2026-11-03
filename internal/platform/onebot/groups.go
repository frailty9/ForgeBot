package onebot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/group"
	"github.com/soleapi/forgebot/internal/operation"
)

// 审核句柄保留原事件 flag/sub_type，并绑定原群；Core 只将它作为不透明引用。
type requestReference struct {
	Flag    string `json:"flag"`
	SubType string `json:"sub_type"`
	Target  string `json:"target"`
}

func (e rawEvent) groupEvent(cfg Config) (group.Event, bool, error) {
	target := scalar(e.GroupID)
	binding, bound := cfg.Groups[target]
	if !bound {
		return group.Event{}, false, nil
	}
	kind := group.EventKind("")
	if e.PostType == "request" && e.RequestType == "group" && e.SubType == "add" {
		kind = group.JoinRequested
	}
	if e.PostType == "notice" && e.NoticeType == "group_increase" {
		kind = group.MemberJoined
	}
	if kind == "" {
		return group.Event{}, false, nil
	}
	user := scalar(e.UserID)
	if user == "" || e.Time <= 0 {
		return group.Event{}, true, operation.New(operation.Invalid, "group event user and timestamp are required")
	}
	event := group.Event{Kind: kind, ConversationID: binding.ConversationID, ConnectorID: cfg.ID, UserID: user, At: time.Unix(e.Time, 0), Comment: e.Comment, Self: user == scalar(e.SelfID)}
	if kind == group.JoinRequested {
		if e.Flag == "" || len(e.Flag) > 2048 {
			return event, true, operation.New(operation.Invalid, "join request flag is required and bounded")
		}
		encoded, _ := json.Marshal(requestReference{Flag: e.Flag, SubType: e.SubType, Target: target})
		event.Reference = string(encoded)
		event.ID = group.ID(scalar(e.SelfID), target, e.Flag, e.SubType)
	} else {
		event.ID = group.ID(scalar(e.SelfID), target, user, fmt.Sprint(e.Time), e.SubType)
	}
	return event, true, nil
}

func (s *Sender) ReviewGroupRequest(ctx context.Context, target string, request group.ReviewRequest) error {
	var reference requestReference
	if err := json.Unmarshal([]byte(request.Reference), &reference); err != nil || reference.Flag == "" || reference.SubType != "add" {
		return operation.New(operation.Invalid, "invalid group request reference")
	}
	if reference.Target != target {
		return operation.New(operation.Conflict, "join request group binding changed")
	}
	return s.groupAPI(ctx, "set_group_add_request", map[string]any{"flag": reference.Flag, "sub_type": reference.SubType, "approve": request.Approve, "reason": request.Reason}, nil)
}

// _send_group_notice 是实现扩展；仅发布普通文本公告，不宣称 OneBot11 标准保证。
// SnowLuma packages/onebot/src/actions/extended.ts（2026-11-03 核对）。
func (s *Sender) PublishGroupAnnouncement(ctx context.Context, target, text string) error {
	return s.groupAPI(ctx, "_send_group_notice", map[string]any{"group_id": numericOrString(target), "content": text}, nil)
}
func (s *Sender) RecallGroupMessage(ctx context.Context, target, message string) error {
	var info struct {
		GroupID     json.RawMessage `json:"group_id"`
		MessageType string          `json:"message_type"`
	}
	if err := s.groupAPI(ctx, "get_msg", map[string]any{"message_id": numericOrString(message)}, &info); err != nil {
		return err
	}
	if scalar(info.GroupID) == "" {
		return operation.New(operation.Unsupported, "connector cannot confirm message group before recall")
	}
	if scalar(info.GroupID) != target || info.MessageType != "group" {
		return operation.New(operation.PolicyDenied, "message does not belong to the requested group")
	}
	return s.groupAPI(ctx, "delete_msg", map[string]any{"message_id": numericOrString(message)}, nil)
}

func (s *Sender) groupAPI(ctx context.Context, action string, payload any, out any) error {
	if err := ctx.Err(); err != nil {
		return operation.Wrap("onebot."+action, operation.Canceled, operation.NotStarted, err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return operation.Wrap("onebot."+action, operation.Invalid, operation.NotStarted, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.APIURL, "/")+"/"+action, bytes.NewReader(data))
	if err != nil {
		return operation.Wrap("onebot."+action, operation.Invalid, operation.NotStarted, err)
	}
	request.Header.Set("Content-Type", "application/json")
	if s.cfg.SendToken != "" {
		request.Header.Set("Authorization", "Bearer "+s.cfg.SendToken)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return operation.Wrap("onebot."+action, operation.Unavailable, operation.EffectUnknown, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code := operation.HTTPCode(response.StatusCode)
		if response.StatusCode == http.StatusNotFound {
			code = operation.Unsupported
		}
		return operation.Wrap("onebot."+action, code, operation.EffectUnknown, fmt.Errorf("OneBot HTTP status %d", response.StatusCode))
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil {
		return operation.Wrap("onebot."+action, operation.Unavailable, operation.EffectUnknown, err)
	}
	if len(data) > 65536 {
		return operation.Wrap("onebot."+action, operation.Invalid, operation.EffectUnknown, fmt.Errorf("OneBot operation result exceeds limit"))
	}
	var envelope struct {
		Status  string          `json:"status"`
		Retcode *int            `json:"retcode"`
		Data    json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil {
		return operation.Wrap("onebot."+action, operation.Invalid, operation.EffectUnknown, err)
	}
	if envelope.Status != "ok" || envelope.Retcode == nil || *envelope.Retcode != 0 {
		return operation.Wrap("onebot."+action, operation.Unknown, operation.EffectUnknown, fmt.Errorf("OneBot rejected group operation: status=%s", envelope.Status))
	}
	if out != nil {
		if err = json.Unmarshal(envelope.Data, out); err != nil {
			return operation.Wrap("onebot."+action, operation.Invalid, operation.EffectUnknown, err)
		}
	}
	return nil
}
