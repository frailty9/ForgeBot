// Package onebot implements the OneBot 11 group-message adapter. It receives
// webhook events, resolves reply/image references through the OneBot API, and
// sends text replies to the bound group target.
package onebot

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/platform"
)

var (
	ErrNotMessageEvent = errors.New("onebot event is not a message event")
	ErrUnsupported     = errors.New("onebot message content is unsupported")
)

// Config contains OneBot HTTP endpoints, credentials, and group bindings.
type Config struct {
	ID            string
	APIURL        string
	SendToken     string
	ReceiveSecret string
	PromptSuffix  string
	WebhookAddr   string
	WebhookPath   string
	RequestLimit  int64
	Timeout       time.Duration
	ReplayTTL     time.Duration
	ReplayMax     int
	Groups        map[string]Group
}

// Group maps an external OneBot group ID to a logical ForgeBot conversation.
type Group struct {
	ConversationID string
	PromptSuffix   string
}

// Adapter is a OneBot webhook server and outbound sender.
type Adapter struct {
	cfg     Config
	client  *http.Client
	logger  *slog.Logger
	sender  *Sender
	server  *http.Server
	serverM sync.Mutex
	done    chan struct{}
	errors  chan error
	replay  *replayCache
}

// New validates configuration and precomputes connector/group prompt suffixes.
func New(cfg Config, logger *slog.Logger) (*Adapter, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	groups := make(map[string]Group, len(cfg.Groups))
	for target, group := range cfg.Groups {
		group.PromptSuffix = promptSuffix(cfg.PromptSuffix, group.PromptSuffix)
		groups[target] = group
	}
	cfg.Groups = groups
	a := &Adapter{
		cfg: cfg, logger: logger, errors: make(chan error, 1),
		replay: newReplayCache(cfg.ReplayTTL, cfg.ReplayMax),
	}
	a.client = &http.Client{Timeout: cfg.Timeout}
	a.sender = &Sender{cfg: cfg, client: a.client, logger: logger}
	return a, nil
}

// ID returns the configured connector identifier.
func (a *Adapter) ID() string { return a.cfg.ID }

// Sender returns the connector-scoped outbound sender.
func (a *Adapter) Sender() platform.ConnectorSender { return a.sender }

// Errors reports asynchronous listener failures.
func (a *Adapter) Errors() <-chan error { return a.errors }

// Start binds the OneBot callback listener and returns once it is ready.
func (a *Adapter) Start(ctx context.Context, handler platform.Handler) error {
	if handler == nil {
		return errors.New("onebot handler is required")
	}
	a.serverM.Lock()
	if a.server != nil {
		a.serverM.Unlock()
		return errors.New("onebot adapter is already started")
	}
	mux := http.NewServeMux()
	mux.HandleFunc(a.cfg.WebhookPath, a.webhook(handler))
	server := &http.Server{Addr: a.cfg.WebhookAddr, Handler: mux, ReadHeaderTimeout: a.cfg.Timeout}
	listener, err := net.Listen("tcp", a.cfg.WebhookAddr)
	if err != nil {
		a.serverM.Unlock()
		return err
	}
	done := make(chan struct{})
	a.server = server
	a.done = done
	a.serverM.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)
			_ = server.Shutdown(shutdownCtx)
			cancel()
		case <-done:
		}
	}()
	go func() {
		err := server.Serve(listener)
		a.serverM.Lock()
		if a.server == server {
			a.server = nil
			a.done = nil
		}
		a.serverM.Unlock()
		close(done)
		if err != nil && err != http.ErrServerClosed {
			select {
			case a.errors <- err:
			default:
			}
		}
	}()
	return nil
}

func (a *Adapter) Stop(ctx context.Context) error {
	a.serverM.Lock()
	server := a.server
	done := a.done
	a.serverM.Unlock()
	if server == nil {
		return nil
	}
	if err := server.Shutdown(ctx); err != nil {
		return err
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Adapter) webhook(handler platform.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		defer func() {
			a.logger.DebugContext(context.WithoutCancel(r.Context()), "onebot webhook completed", "path", r.URL.Path, "duration", time.Since(startedAt))
		}()
		a.logger.DebugContext(r.Context(), "onebot webhook received", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, a.cfg.RequestLimit))
		if err != nil {
			writeResponse(w, http.StatusBadRequest, map[string]string{"status": "failed", "retcode": "1001"})
			return
		}
		if !a.authorized(r, body) {
			a.logger.WarnContext(r.Context(), "onebot webhook unauthorized", "path", r.URL.Path)
			writeResponse(w, http.StatusUnauthorized, map[string]string{"status": "failed", "retcode": "1403"})
			return
		}
		var event rawEvent
		decoder := json.NewDecoder(bytes.NewReader(body))
		if err := decoder.Decode(&event); err != nil {
			a.logger.WarnContext(r.Context(), "onebot webhook invalid JSON", "err", err)
			writeResponse(w, http.StatusBadRequest, map[string]string{"status": "failed", "retcode": "1001"})
			return
		}
		msg, err := event.message(a.cfg.Groups)
		if err != nil {
			if errors.Is(err, ErrNotMessageEvent) {
				a.logger.DebugContext(r.Context(), "onebot event ignored", "post_type", event.PostType, "message_type", event.MessageType)
				writeResponse(w, http.StatusOK, map[string]string{"status": "ok", "retcode": "0"})
				return
			}
			writeResponse(w, http.StatusBadRequest, map[string]string{"status": "failed", "retcode": "1002"})
			return
		}
		msg.Media = a.resolveMedia(r.Context(), msg.Media)
		if len(msg.Media) == 0 && msg.ReplyToMessageID != "" {
			msg.Media = a.resolveReplyMedia(r.Context(), msg.ReplyToMessageID)
		}
		a.logger.DebugContext(r.Context(), "onebot message mapped", "conversation_id", msg.ConversationID,
			"message_id", msg.MessageID, "user_id", msg.UserID, "mentioned", msg.Mentioned,
			"text_len", len([]rune(msg.Text)), "media_count", len(msg.Media))
		if len(msg.Media) == 0 {
			a.logger.DebugContext(r.Context(), "onebot media not detected",
				"message_bytes", len(event.Message), "message_prefix", truncateForLog(string(event.Message), 320),
				"raw_message_len", len(event.RawMessage), "raw_message_prefix", truncateForLog(event.RawMessage, 160))
		}
		if a.replay.seenOrAdd(event.identity(), time.Now()) {
			a.logger.DebugContext(r.Context(), "onebot event replay suppressed", "message_id", msg.MessageID)
			writeResponse(w, http.StatusOK, map[string]string{"status": "ok", "retcode": "0"})
			return
		}
		// Reverse HTTP clients may cancel their request after a short ACK timeout.
		// Keep bot/LLM processing alive independently of that transport lifetime.
		processingCtx := context.WithoutCancel(r.Context())
		result, err := handler.HandleInbound(processingCtx, msg)
		if err != nil {
			a.logger.ErrorContext(processingCtx, "onebot inbound message failed", "err", err, "message_id", msg.MessageID, "conversation_id", msg.ConversationID, "verdict", result.Verdict)
		} else {
			a.logger.DebugContext(processingCtx, "onebot inbound message handled", "message_id", msg.MessageID, "conversation_id", msg.ConversationID, "accepted", result.Accepted, "verdict", result.Verdict)
		}
		writeResponse(w, http.StatusOK, map[string]any{"status": "ok", "retcode": "0", "data": result})
	}
}

// resolveMedia turns OneBot file references into content the LLM can access.
// OneBot implementations commonly omit url and expose only file/file_id.
func (a *Adapter) resolveMedia(ctx context.Context, media []messaging.Media) []messaging.Media {
	for index := range media {
		item := &media[index]
		if item.Kind != messaging.MediaImage || item.URL != "" || item.Name == "" {
			continue
		}
		var response struct {
			Data struct {
				File string `json:"file"`
				URL  string `json:"url"`
			} `json:"data"`
		}
		payload := map[string]any{"file": item.Name}
		data, err := json.Marshal(payload)
		if err != nil {
			continue
		}
		endpoint, err := url.JoinPath(strings.TrimRight(a.cfg.APIURL, "/"), "/get_image")
		if err != nil {
			continue
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
		if err != nil {
			continue
		}
		request.Header.Set("Content-Type", "application/json")
		if token := strings.TrimSpace(a.cfg.SendToken); token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		responseBody, err := a.client.Do(request)
		if err != nil {
			a.logger.DebugContext(ctx, "onebot get_image failed", "file", item.Name, "err", err)
			continue
		}
		_ = json.NewDecoder(responseBody.Body).Decode(&response)
		_ = responseBody.Body.Close()
		if isImageURL(response.Data.URL) {
			item.URL = strings.TrimSpace(response.Data.URL)
			continue
		}
		path := strings.TrimSpace(response.Data.File)
		if path == "" {
			path = strings.TrimSpace(item.Name)
		}
		if content, readErr := os.ReadFile(path); readErr == nil {
			item.Data = content
			if item.MimeType == "" {
				item.MimeType = "image/jpeg"
			}
		}
	}
	return media
}

func (a *Adapter) resolveReplyMedia(ctx context.Context, messageID string) []messaging.Media {
	payload := map[string]any{"message_id": numericOrString(messageID)}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	endpoint, err := url.JoinPath(strings.TrimRight(a.cfg.APIURL, "/"), "/get_msg")
	if err != nil {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil
	}
	request.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(a.cfg.SendToken); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := a.client.Do(request)
	if err != nil {
		a.logger.DebugContext(ctx, "onebot get_msg failed", "message_id", messageID, "err", err)
		return nil
	}
	defer response.Body.Close()
	var envelope struct {
		Data struct {
			Message    json.RawMessage `json:"message"`
			RawMessage string          `json:"raw_message"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		a.logger.DebugContext(ctx, "onebot get_msg decode failed", "message_id", messageID, "err", err)
		return nil
	}
	media := extractMedia(envelope.Data.Message, envelope.Data.RawMessage)
	media = a.resolveMedia(ctx, media)
	if len(media) > 0 {
		a.logger.DebugContext(ctx, "onebot reply media resolved", "message_id", messageID, "media_count", len(media))
	}
	return media
}

func (a *Adapter) authorized(r *http.Request, body []byte) bool {
	secret := strings.TrimSpace(a.cfg.ReceiveSecret)
	if secret == "" {
		return true
	}
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := "sha1=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(strings.TrimSpace(r.Header.Get("X-Signature"))), []byte(expected))
}

type Sender struct {
	cfg    Config
	client *http.Client
	logger *slog.Logger
}

// SendTo sends a text message to one configured external group target.
func (s *Sender) SendTo(ctx context.Context, target string, msg messaging.OutboundMessage) error {
	if strings.TrimSpace(target) == "" || strings.TrimSpace(msg.Text) == "" {
		return errors.New("onebot target and text are required")
	}
	if len(msg.Media) > 0 {
		return ErrUnsupported
	}
	payload := map[string]any{"message": msg.Text}
	payload["message_type"] = "group"
	payload["group_id"] = numericOrString(target)
	if msg.ReplyToMessageID != "" {
		payload["message"] = []map[string]any{{"type": "reply", "data": map[string]any{"id": numericOrString(msg.ReplyToMessageID)}}, {"type": "text", "data": map[string]string{"text": msg.Text}}}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint, err := url.JoinPath(strings.TrimRight(s.cfg.APIURL, "/"), "/send_msg")
	if err != nil {
		return fmt.Errorf("onebot API URL: %w", err)
	}
	s.logger.DebugContext(ctx, "onebot outbound request", "endpoint", endpoint, "target", target, "conversation_id", msg.ConversationID, "reply_to", msg.ReplyToMessageID, "text_len", len([]rune(msg.Text)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(s.cfg.SendToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.logger.DebugContext(ctx, "onebot outbound request failed", "endpoint", endpoint, "err", err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		s.logger.DebugContext(ctx, "onebot outbound response failed", "endpoint", endpoint, "status", resp.StatusCode)
		return fmt.Errorf("onebot API status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		Retcode int    `json:"retcode"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode onebot API response: %w", err)
	}
	if result.Retcode != 0 || result.Status == "failed" {
		s.logger.DebugContext(ctx, "onebot outbound response rejected", "endpoint", endpoint, "retcode", result.Retcode, "status", result.Status)
		return fmt.Errorf("onebot API failed: retcode=%d status=%s", result.Retcode, result.Status)
	}
	s.logger.DebugContext(ctx, "onebot outbound response accepted", "endpoint", endpoint, "status", result.Status)
	return nil
}

type rawEvent struct {
	PostType    string          `json:"post_type"`
	MessageType string          `json:"message_type"`
	Time        int64           `json:"time"`
	MessageID   json.RawMessage `json:"message_id"`
	UserID      json.RawMessage `json:"user_id"`
	GroupID     json.RawMessage `json:"group_id"`
	SelfID      json.RawMessage `json:"self_id"`
	Sender      struct {
		Nickname string `json:"nickname"`
	} `json:"sender"`
	RawMessage string          `json:"raw_message"`
	Message    json.RawMessage `json:"message"`
}

func (e rawEvent) identity() string {
	return scalar(e.SelfID) + "\x00" + scalar(e.GroupID) + "\x00" + scalar(e.UserID) + "\x00" + scalar(e.MessageID)
}

func (e rawEvent) message(groups map[string]Group) (messaging.InboundMessage, error) {
	if e.PostType != "message" || e.MessageType != "group" {
		return messaging.InboundMessage{}, ErrNotMessageEvent
	}
	externalGroupID := scalar(e.GroupID)
	group, ok := groups[externalGroupID]
	if !ok || group.ConversationID == "" {
		return messaging.InboundMessage{}, ErrNotMessageEvent
	}
	if externalGroupID == "" || scalar(e.UserID) == "" || scalar(e.MessageID) == "" {
		return messaging.InboundMessage{}, errors.New("onebot message identifiers are required")
	}
	text := extractText(e.Message)
	if text == "" {
		text = stripCQ(strings.TrimSpace(e.RawMessage))
	}
	media := extractMedia(e.Message, e.RawMessage)
	return messaging.InboundMessage{
		PromptSuffix:   group.PromptSuffix,
		ConversationID: group.ConversationID, UserID: scalar(e.UserID), UserName: e.Sender.Nickname,
		MessageID: scalar(e.MessageID), ReplyToMessageID: replyToMessageID(e.Message, e.RawMessage),
		Text: text, Media: media, Mentioned: mentions(e.Message, e.RawMessage, scalar(e.SelfID)),
		Command: strings.HasPrefix(text, "/"), At: time.Unix(e.Time, 0),
	}, nil
}

func extractMedia(raw json.RawMessage, rawMessage string) []messaging.Media {
	var media []messaging.Media
	var value any
	if json.Unmarshal(raw, &value) == nil {
		media = append(media, mediaFromValue(value)...)
	}
	if len(media) == 0 {
		media = append(media, mediaFromCQ(rawMessage)...)
	}
	if len(media) == 0 {
		// Last-resort compatibility for adapters that embed CQ markup inside
		// a custom JSON message envelope.
		media = append(media, mediaFromCQ(string(raw))...)
	}
	return media
}

func mediaFromValue(value any) []messaging.Media {
	var media []messaging.Media
	switch current := value.(type) {
	case []any:
		for _, item := range current {
			media = append(media, mediaFromValue(item)...)
		}
	case map[string]any:
		if typeName, _ := current["type"].(string); strings.EqualFold(strings.TrimSpace(typeName), "image") {
			item := messaging.Media{Kind: messaging.MediaImage}
			data, _ := current["data"].(map[string]any)
			for _, key := range []string{"url", "file", "path", "file_id", "fileId", "id"} {
				if candidate, ok := data[key].(string); ok {
					candidate = strings.TrimSpace(candidate)
					if item.URL == "" && isImageURL(candidate) {
						item.URL = candidate
					}
					if item.Name == "" && candidate != "" {
						item.Name = candidate
					}
				}
			}
			if item.URL != "" || item.Name != "" {
				media = append(media, item)
			}
		}
		for key, nested := range current {
			if key != "data" {
				media = append(media, mediaFromValue(nested)...)
			}
		}
	}
	return media
}

func mediaFromCQ(raw string) []messaging.Media {
	var media []messaging.Media
	for offset := 0; ; {
		relative := strings.Index(strings.ToLower(raw[offset:]), "[cq:image")
		start := relative
		if start < 0 {
			break
		}
		start += offset
		end := strings.IndexByte(raw[start:], ']')
		if end < 0 {
			break
		}
		segment := raw[start : start+end+1]
		imageURL := cqParameter(segment, "image", "url")
		if !isImageURL(imageURL) {
			imageURL = cqParameter(segment, "image", "file")
		}
		if decoded, err := url.QueryUnescape(imageURL); err == nil {
			imageURL = decoded
		}
		if isImageURL(imageURL) {
			media = append(media, messaging.Media{Kind: messaging.MediaImage, URL: imageURL})
		} else if file := cqParameter(segment, "image", "file"); strings.TrimSpace(file) != "" {
			media = append(media, messaging.Media{Kind: messaging.MediaImage, Name: strings.TrimSpace(file)})
		}
		offset = start + end + 1
	}
	return media
}

func isImageURL(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "data:image/")
}

func truncateForLog(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func promptSuffix(connector, group string) string {
	connector = strings.TrimSpace(connector)
	group = strings.TrimSpace(group)
	if connector == "" {
		return group
	}
	if group == "" {
		return connector
	}
	return connector + "\n\n" + group
}

func scalar(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	}
	return ""
}

func numericOrString(value string) any {
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		return n
	}
	return value
}

func extractText(raw json.RawMessage) string {
	var segments []struct {
		Type string `json:"type"`
		Data struct {
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &segments); err != nil {
		var encoded string
		if json.Unmarshal(raw, &encoded) == nil {
			return stripCQ(strings.TrimSpace(encoded))
		}
		return ""
	}
	var b strings.Builder
	for _, segment := range segments {
		if segment.Type == "text" {
			b.WriteString(segment.Data.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

func replyToMessageID(raw json.RawMessage, rawMessage string) string {
	var segments []struct {
		Type string `json:"type"`
		Data struct {
			ID json.RawMessage `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &segments) == nil {
		for _, segment := range segments {
			if segment.Type == "reply" {
				if id := scalar(segment.Data.ID); id != "" {
					return id
				}
			}
		}
	}
	return cqParameter(rawMessage, "reply", "id")
}

func cqParameter(message, segmentType, parameter string) string {
	prefix := "[CQ:" + segmentType
	for offset := 0; ; {
		start := strings.Index(message[offset:], prefix)
		if start < 0 {
			return ""
		}
		start += offset
		end := strings.IndexByte(message[start:], ']')
		if end < 0 {
			return ""
		}
		fields := strings.Split(message[start+len(prefix):start+end], ",")
		for _, field := range fields {
			key, value, ok := strings.Cut(field, "=")
			if ok && strings.TrimSpace(key) == parameter {
				return strings.TrimSpace(value)
			}
		}
		offset = start + end + 1
	}
}

func stripCQ(text string) string {
	for {
		start := strings.Index(text, "[CQ:")
		if start < 0 {
			break
		}
		end := strings.IndexByte(text[start:], ']')
		if end < 0 {
			break
		}
		text = text[:start] + text[start+end+1:]
	}
	return strings.TrimSpace(text)
}

func mentions(raw json.RawMessage, rawMessage, selfID string) bool {
	if selfID == "" {
		return false
	}
	var segments []struct {
		Type string `json:"type"`
		Data struct {
			QQ string `json:"qq"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &segments) == nil {
		for _, segment := range segments {
			if segment.Type == "at" && segment.Data.QQ == selfID {
				return true
			}
		}
	}
	return strings.Contains(rawMessage, "[CQ:at,qq="+selfID+"]")
}

func validateConfig(cfg Config) error {
	var problems []string
	if strings.TrimSpace(cfg.ID) == "" {
		problems = append(problems, "onebot connector id is required")
	}
	if strings.TrimSpace(cfg.APIURL) == "" {
		problems = append(problems, "onebot api_url is required")
	}
	if strings.TrimSpace(cfg.WebhookAddr) == "" {
		problems = append(problems, "onebot webhook_addr is required")
	}
	if !strings.HasPrefix(strings.TrimSpace(cfg.WebhookPath), "/") {
		problems = append(problems, "onebot webhook_path must start with /")
	}
	if cfg.RequestLimit <= 0 {
		problems = append(problems, "onebot request_limit must be positive")
	}
	if cfg.Timeout <= 0 {
		problems = append(problems, "onebot timeout must be positive")
	}
	if cfg.ReplayTTL <= 0 {
		problems = append(problems, "onebot replay_ttl must be positive")
	}
	if cfg.ReplayMax <= 0 {
		problems = append(problems, "onebot replay_max must be positive")
	}
	if len(cfg.Groups) == 0 {
		problems = append(problems, "onebot connector must bind at least one group")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func writeResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
