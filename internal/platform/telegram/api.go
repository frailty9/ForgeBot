package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/operation"
)

const maxResponseBytes = 8 << 20

type responseParameters struct {
	RetryAfter int64 `json:"retry_after"`
	MigrateTo  int64 `json:"migrate_to_chat_id"`
}

// apiFailure 仅保存公开分类和平台控制参数，不保留含 Token 的 URL 或响应正文。
type apiFailure struct {
	failure    error
	parameters responseParameters
}

func (e *apiFailure) Error() string { return e.failure.Error() }
func (e *apiFailure) Unwrap() error { return e.failure }

func safeTransportError(err error) error {
	var address *url.Error
	for errors.As(err, &address) {
		err = address.Err
	}
	// 不保留原始诊断链，防止底层 Transport 把凭据地址嵌入自定义错误。
	failure := operation.Describe(err)
	return operation.New(failure.Code, "Telegram transport failed")
}

func (a *Adapter) call(ctx context.Context, method string, input, output any, timeout time.Duration) error {
	ctx, cancel := a.requestContext(ctx, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return operation.Wrap("telegram."+method, operation.Canceled, operation.NotStarted, err)
	}
	if input == nil {
		input = map[string]any{}
	}
	body, err := json.Marshal(input)
	if err != nil {
		return operation.New(operation.Invalid, "invalid Telegram request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiBase+"/bot"+a.cfg.Token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return operation.New(operation.Invalid, "invalid Telegram API address")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return operation.Wrap("telegram."+method, operation.Unavailable, operation.EffectUnknown, safeTransportError(err))
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return operation.Wrap("telegram."+method, operation.Unavailable, operation.EffectUnknown, safeTransportError(err))
	}
	if len(data) > maxResponseBytes {
		return operation.Wrap("telegram."+method, operation.Invalid, operation.EffectUnknown, errors.New("Telegram response exceeds 8MiB"))
	}
	var envelope struct {
		OK          bool               `json:"ok"`
		Result      json.RawMessage    `json:"result"`
		ErrorCode   int                `json:"error_code"`
		Description string             `json:"description"`
		Parameters  responseParameters `json:"parameters"`
	}
	success := response.StatusCode >= 200 && response.StatusCode < 300
	if json.Unmarshal(data, &envelope) != nil {
		// 代理返回的 502/504 HTML 等非 JSON 正文按 HTTP 状态分类，保持可重试语义。
		if !success {
			code := operation.HTTPCode(response.StatusCode)
			return operation.Wrap("telegram."+method, code, operation.EffectUnknown, operation.New(code, "Telegram API returned a non-JSON error"))
		}
		return operation.Wrap("telegram."+method, operation.Invalid, operation.EffectUnknown, errors.New("invalid Telegram response"))
	}
	if !success || !envelope.OK {
		code := operation.HTTPCode(response.StatusCode)
		if success {
			code = operation.HTTPCode(envelope.ErrorCode)
		}
		code = describedCode(code, envelope.Description)
		message := "Telegram API rejected operation"
		// 平台描述不含凭据，保留有界原文便于区分“消息不存在”“无权限”“被移出群”等原因。
		if description := strings.TrimSpace(envelope.Description); description != "" {
			message += ": " + truncateRunes(description, 200)
		}
		if envelope.Parameters.MigrateTo != 0 {
			code = operation.Conflict
			message = fmt.Sprintf("Telegram group migrated; update binding to chat_id %d", envelope.Parameters.MigrateTo)
		}
		effect := operation.NotStarted
		if code == operation.Unavailable || code == operation.Timeout || code == operation.Unknown {
			effect = operation.EffectUnknown
		}
		return &apiFailure{failure: operation.Wrap("telegram."+method, code, effect, operation.New(code, message)), parameters: envelope.Parameters}
	}
	if output != nil && (len(envelope.Result) == 0 || bytes.Equal(envelope.Result, []byte("null")) || json.Unmarshal(envelope.Result, output) != nil) {
		return operation.Wrap("telegram."+method, operation.Invalid, operation.EffectUnknown, errors.New("invalid Telegram result"))
	}
	return nil
}

// describedCode 只细分 Bot API 用 400 返回、但调用方需要区别对待的几类常见描述。
func describedCode(code operation.Code, description string) operation.Code {
	description = strings.ToLower(description)
	switch {
	case strings.Contains(description, "not found"):
		return operation.NotFound
	case strings.Contains(description, "can't be deleted"), strings.Contains(description, "not enough rights"),
		strings.Contains(description, "bot was kicked"), strings.Contains(description, "have no rights"):
		return operation.PermissionDenied
	}
	return code
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit]) + "…"
}

func parseID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id == 0 || strconv.FormatInt(id, 10) != value {
		return 0, operation.New(operation.Invalid, "Telegram id must be a nonzero canonical decimal integer")
	}
	return id, nil
}

func (a *Adapter) target(value string) (int64, error) {
	id, err := parseID(value)
	if err != nil {
		return 0, err
	}
	if _, exists := a.cfg.Groups[id]; !exists {
		return 0, operation.New(operation.NotConfigured, "Telegram group is not bound")
	}
	return id, nil
}

func (a *Adapter) SendTo(ctx context.Context, target string, message messaging.OutboundMessage) error {
	id, err := a.target(target)
	if err != nil {
		return err
	}
	if len(message.Media) > 0 {
		return operation.New(operation.Unsupported, "Telegram outbound media is not implemented")
	}
	if !utf8.ValidString(message.Text) || strings.TrimSpace(message.Text) == "" || utf8.RuneCountInString(message.Text) > 4096 {
		return operation.New(operation.Invalid, "Telegram text must contain 1..4096 Unicode characters")
	}
	payload := map[string]any{"chat_id": id, "text": message.Text}
	if message.ReplyToMessageID != "" {
		reply, err := parseID(message.ReplyToMessageID)
		if err != nil || reply < 0 {
			return operation.New(operation.Invalid, "Telegram reply message id must be positive")
		}
		payload["reply_parameters"] = map[string]any{"message_id": reply, "allow_sending_without_reply": false}
	}
	return a.call(ctx, "sendMessage", payload, nil, a.cfg.Timeout)
}

func (a *Adapter) RecallMessage(ctx context.Context, target, messageID string) error {
	id, err := a.target(target)
	if err != nil {
		return err
	}
	message, err := parseID(messageID)
	if err != nil || message < 0 {
		return operation.New(operation.Invalid, "Telegram message id must be positive")
	}
	return a.call(ctx, "deleteMessage", map[string]any{"chat_id": id, "message_id": message}, nil, a.cfg.Timeout)
}
