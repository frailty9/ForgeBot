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

func (*Sender) GroupEventSupport() (bool, bool)  { return true, true }
func (*Sender) GroupReviewReasonSupported() bool { return true }

func (*Sender) MessageSupport() group.MessageSupport {
	return group.MessageSupport{TextMessages: true, OutboundSegments: true}
}

// joinAnswer 取出 QQ 验证问题拼接格式“问题：xxx 换行 答案：yyy”中的答案，使审批规则
// 只面对用户填写的内容；没有这个结构时原样返回。以第一处换行加“答案：”为界：问题由
// 群管理员设定，答案由申请人填写，若取最后一处，申请人可在答案里伪造“答案：xxx”绕过规则。
func joinAnswer(comment string) string {
	const questionPrefix, answerMarker = "问题：", "\n答案："
	if !strings.HasPrefix(comment, questionPrefix) {
		return comment
	}
	index := strings.Index(comment, answerMarker)
	if index < 0 {
		return comment
	}
	return comment[index+len(answerMarker):]
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
	event := group.Event{Kind: kind, ConversationID: binding.ConversationID, ConnectorID: cfg.ID, UserID: user, At: time.Unix(e.Time, 0), Comment: joinAnswer(e.Comment), Self: user == scalar(e.SelfID)}
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
	payload := map[string]any{"flag": reference.Flag, "sub_type": reference.SubType, "approve": request.Approve}
	if !request.Approve && request.Reason != "" {
		payload["reason"] = request.Reason
	}
	return s.groupAPI(ctx, "set_group_add_request", payload, nil)
}

// RecallMessage 实现 group.Recaller，是调用链中实际请求 OneBot 的一层。
// target 是 Registry 解析出的平台群号；OneBot delete_msg 本身只接收 message_id，
// 因而 target 不进入请求体。其他协议可能需要同时把群目标传给平台 API。
func (s *Sender) RecallMessage(ctx context.Context, target, messageID string) error {
	if strings.TrimSpace(target) == "" || strings.TrimSpace(messageID) == "" {
		return operation.New(operation.Invalid, "message recall target and id are required")
	}
	return s.groupAPI(ctx, "delete_msg", map[string]any{"message_id": numericOrString(messageID)}, nil)
}

// memberName 查询新成员的群名片或昵称；入群通知本身只带 user_id。失败时返回空串，
// 欢迎语的 {user} 随之退回用户 ID，不因查询失败而不欢迎。
func (s *Sender) memberName(ctx context.Context, target, user string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var info struct {
		Card     string `json:"card"`
		Nickname string `json:"nickname"`
	}
	payload := map[string]any{"group_id": numericOrString(target), "user_id": numericOrString(user), "no_cache": true}
	if err := s.groupAPI(ctx, "get_group_member_info", payload, &info); err != nil {
		s.logger.DebugContext(ctx, "onebot member name lookup failed", "group_id", target, "user_id", user, "err", err)
		return ""
	}
	if name := strings.TrimSpace(info.Card); name != "" {
		return name
	}
	return strings.TrimSpace(info.Nickname)
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
