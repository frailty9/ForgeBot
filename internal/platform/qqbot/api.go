package qqbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/operation"
)

func (a *Adapter) accessToken(ctx context.Context) (string, error) {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if a.token != "" && a.now().Add(60*time.Second).Before(a.tokenExpires) {
		return a.token, nil
	}
	body, _ := json.Marshal(map[string]string{"appId": a.cfg.AppID, "clientSecret": a.cfg.Secret})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiBase+"/app/getAppAccessToken", bytes.NewReader(body))
	if err != nil {
		return "", operation.New(operation.Invalid, "invalid QQ token request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return "", operation.Wrap("qqbot.token", operation.Unavailable, operation.NotStarted, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", operation.New(operation.Unauthenticated, "QQ access token was rejected")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		return "", operation.New(operation.Invalid, "invalid QQ access token response")
	}
	var result struct {
		Token   string          `json:"access_token"`
		Expires json.RawMessage `json:"expires_in"`
	}
	if json.Unmarshal(data, &result) != nil || result.Token == "" {
		return "", operation.New(operation.Unauthenticated, "QQ access token is unavailable")
	}
	lifetime := strings.Trim(string(result.Expires), "\"")
	seconds, err := strconv.ParseInt(lifetime, 10, 64)
	if err != nil || seconds <= 0 || seconds > 86400 {
		return "", operation.New(operation.Invalid, "invalid QQ token lifetime")
	}
	a.token, a.tokenExpires = result.Token, a.now().Add(time.Duration(seconds)*time.Second)
	return a.token, nil
}

func (a *Adapter) api(ctx context.Context, method, path string, payload any, out any) error {
	ctx, cancel := a.context(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return operation.Wrap("qqbot.request", operation.Canceled, operation.NotStarted, err)
	}
	token, err := a.accessToken(ctx)
	if err != nil {
		return err
	}
	var body []byte
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return operation.Wrap("qqbot.request", operation.Invalid, operation.NotStarted, err)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, a.apiBase+path, bytes.NewReader(body))
	if err != nil {
		return operation.New(operation.Invalid, "invalid QQ API request")
	}
	request.Header.Set("Authorization", "QQBot "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return operation.Wrap("qqbot.request", operation.Unavailable, operation.EffectUnknown, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil {
		return operation.Wrap("qqbot.response", operation.Unavailable, operation.EffectUnknown, err)
	}
	if len(data) > 65536 {
		return operation.Wrap("qqbot.response", operation.Invalid, operation.EffectUnknown, errors.New("QQ response exceeds limit"))
	}
	var envelope struct {
		Code int64 `json:"code"`
	}
	if len(data) > 0 && json.Unmarshal(data, &envelope) != nil {
		return operation.Wrap("qqbot.response", operation.Invalid, operation.EffectUnknown, errors.New("invalid QQ response"))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || envelope.Code != 0 {
		code := operation.HTTPCode(response.StatusCode)
		switch envelope.Code {
		case 40062003, 40034105, 40054002:
			code = operation.PermissionDenied
		case 40064004, 304103, 40034005, 40034026, 40034128:
			code = operation.Expired
		case 40034100, 40054005:
			code = operation.RateLimited
		case 40034101, 40054003:
			code = operation.NotFound
		case 40061001, 40034024, 40034025:
			code = operation.Invalid
		}
		effect := operation.NotStarted
		if code == operation.Unknown || code == operation.Unavailable || code == operation.Timeout {
			effect = operation.EffectUnknown
		}
		return operation.Wrap("qqbot.request", code, effect, operation.New(code, "QQ API rejected operation"))
	}
	if out != nil && json.Unmarshal(data, out) != nil {
		return operation.Wrap("qqbot.response", operation.Invalid, operation.EffectUnknown, errors.New("invalid QQ result"))
	}
	return nil
}

func (a *Adapter) SendTo(ctx context.Context, target string, message messaging.OutboundMessage) error {
	if _, ok := a.cfg.Groups[target]; !ok {
		return operation.New(operation.NotConfigured, "QQ group is not bound")
	}
	if strings.TrimSpace(message.Text) == "" || len(message.Media) > 0 {
		return operation.New(operation.Unsupported, "QQ connector supports outbound text only")
	}
	if message.ReplyToMessageID == "" {
		return operation.New(operation.Unsupported, "QQ connector requires a passive reply trigger")
	}
	a.mu.Lock()
	now := a.now()
	a.pruneLocked(now)
	key := messageKey(target, message.ReplyToMessageID)
	meta, ok := a.messages[key]
	if !ok || meta.bot || !now.Before(meta.at.Add(5*time.Minute)) {
		a.mu.Unlock()
		return operation.New(operation.Expired, "QQ passive reply trigger is unavailable or expired")
	}
	if meta.sequence >= 5 {
		a.mu.Unlock()
		return operation.New(operation.BudgetExhausted, "QQ passive reply count exhausted")
	}
	meta.sequence++
	a.messages[key] = meta
	a.mu.Unlock()
	payload := map[string]any{"msg_type": 0, "content": message.Text, "msg_id": meta.id, "msg_seq": meta.sequence}
	if meta.reference != "" {
		payload["message_reference"] = map[string]string{"message_id": meta.reference}
	}
	var result struct {
		ID        string `json:"id"`
		Timestamp string `json:"timestamp"`
		ExtInfo   struct {
			RefIndex string `json:"ref_idx"`
		} `json:"ext_info"`
	}
	if err := a.api(ctx, http.MethodPost, "/v2/groups/"+url.PathEscape(target)+"/messages", payload, &result); err != nil {
		return err
	}
	if result.ID != "" {
		at := a.now()
		if parsed, err := time.Parse(time.RFC3339Nano, result.Timestamp); err == nil {
			at = parsed
		}
		a.mu.Lock()
		a.rememberLocked(messageMeta{group: target, id: result.ID, reference: result.ExtInfo.RefIndex, at: at, bot: true})
		a.mu.Unlock()
	}
	return nil
}

func (a *Adapter) RecallMessage(ctx context.Context, target, id string) error {
	if _, ok := a.cfg.Groups[target]; !ok {
		return operation.New(operation.NotConfigured, "QQ group is not bound")
	}
	if strings.TrimSpace(id) == "" {
		return operation.New(operation.Invalid, "QQ message id is required")
	}
	if err := a.api(ctx, http.MethodDelete, "/v2/groups/"+url.PathEscape(target)+"/messages/"+url.PathEscape(id), nil, nil); err != nil {
		return err
	}
	a.mu.Lock()
	delete(a.messages, messageKey(target, id))
	a.mu.Unlock()
	return nil
}
