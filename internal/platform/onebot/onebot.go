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

type Config struct {
	ID            string
	APIURL        string
	SendToken     string
	ReceiveSecret string
	WebhookAddr   string
	WebhookPath   string
	RequestLimit  int64
	Timeout       time.Duration
	ReplayTTL     time.Duration
	ReplayMax     int
	Groups        map[string]string
}

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

func New(cfg Config, logger *slog.Logger) (*Adapter, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	a := &Adapter{
		cfg: cfg, logger: logger, errors: make(chan error, 1),
		replay: newReplayCache(cfg.ReplayTTL, cfg.ReplayMax),
	}
	a.client = &http.Client{Timeout: cfg.Timeout}
	a.sender = &Sender{cfg: cfg, client: a.client, logger: logger}
	return a, nil
}

func (a *Adapter) ID() string { return a.cfg.ID }

func (a *Adapter) Sender() platform.ConnectorSender { return a.sender }

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
			writeResponse(w, http.StatusUnauthorized, map[string]string{"status": "failed", "retcode": "1403"})
			return
		}
		var event rawEvent
		decoder := json.NewDecoder(bytes.NewReader(body))
		if err := decoder.Decode(&event); err != nil {
			writeResponse(w, http.StatusBadRequest, map[string]string{"status": "failed", "retcode": "1001"})
			return
		}
		msg, err := event.message(a.cfg.Groups)
		if err != nil {
			if errors.Is(err, ErrNotMessageEvent) {
				writeResponse(w, http.StatusOK, map[string]string{"status": "ok", "retcode": "0"})
				return
			}
			writeResponse(w, http.StatusBadRequest, map[string]string{"status": "failed", "retcode": "1002"})
			return
		}
		if a.replay.seenOrAdd(event.identity(), time.Now()) {
			writeResponse(w, http.StatusOK, map[string]string{"status": "ok", "retcode": "0"})
			return
		}
		result, err := handler.HandleInbound(r.Context(), msg)
		if err != nil {
			a.logger.ErrorContext(r.Context(), "onebot inbound message failed", "err", err, "message_id", msg.MessageID)
		}
		writeResponse(w, http.StatusOK, map[string]any{"status": "ok", "retcode": "0", "data": result})
	}
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
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
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
		return fmt.Errorf("onebot API failed: retcode=%d status=%s", result.Retcode, result.Status)
	}
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

func (e rawEvent) message(groups map[string]string) (messaging.InboundMessage, error) {
	if e.PostType != "message" || e.MessageType != "group" {
		return messaging.InboundMessage{}, ErrNotMessageEvent
	}
	externalGroupID := scalar(e.GroupID)
	groupID := groups[externalGroupID]
	if groupID == "" {
		return messaging.InboundMessage{}, ErrNotMessageEvent
	}
	if externalGroupID == "" || scalar(e.UserID) == "" || scalar(e.MessageID) == "" {
		return messaging.InboundMessage{}, errors.New("onebot message identifiers are required")
	}
	text := extractText(e.Message)
	if text == "" {
		text = stripCQ(strings.TrimSpace(e.RawMessage))
	}
	return messaging.InboundMessage{ConversationID: groupID, UserID: scalar(e.UserID), UserName: e.Sender.Nickname, MessageID: scalar(e.MessageID), Text: text, Mentioned: mentions(e.Message, e.RawMessage, scalar(e.SelfID)), Command: strings.HasPrefix(text, "/"), At: time.Unix(e.Time, 0)}, nil
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
	if json.Unmarshal(raw, &segments) != nil {
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
