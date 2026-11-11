// Package telegram 注册 Telegram 群聊连接器及其私有配置。
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/platform"
	adapter "github.com/soleapi/forgebot/internal/platform/telegram"
)

const ID = "native.telegram"

type Factory struct{}

func (Factory) Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: ID, DefaultEnabled: false, Registrations: []modules.RegistrationKind{modules.RegistrationConnectorProviders}}
}

func (Factory) ValidateOptions(raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var options map[string]json.RawMessage
	if err := json.Unmarshal(raw, &options); err != nil {
		return err
	}
	if options == nil || len(options) != 0 {
		return errors.New("telegram module options must be an empty object; configure each connector instance")
	}
	return nil
}

func (f Factory) PublicOptions(raw json.RawMessage) (any, error) {
	return map[string]any{}, f.ValidateOptions(raw)
}

func (Factory) Build(context.Context, json.RawMessage) (modules.Instance, error) {
	return instance{}, nil
}

type instance struct{}

func (instance) Registrations() hooks.Registrations { return hooks.Registrations{} }
func (instance) Close(context.Context) error        { return nil }
func (instance) ExtendedRegistrations() modules.ExtendedRegistrations {
	return modules.ExtendedRegistrations{ConnectorProviders: []platform.ConnectorProvider{provider{}}}
}

type options struct {
	TokenRef         string `json:"token_ref"`
	Mode             string `json:"mode"`
	Timeout          string `json:"timeout,omitempty"`
	WebhookURL       string `json:"webhook_url,omitempty"`
	WebhookAddr      string `json:"webhook_addr,omitempty"`
	WebhookPath      string `json:"webhook_path,omitempty"`
	WebhookSecretRef string `json:"webhook_secret_ref,omitempty"`
}

func validRef(ref string) bool {
	return strings.TrimSpace(ref) != "" && !strings.ContainsAny(ref, "\\\r\n\t") && !strings.Contains(ref, "..") && !strings.HasPrefix(ref, "/")
}

func parse(raw json.RawMessage) (options, error) {
	o := options{Timeout: "15s"}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&o); err != nil {
		return o, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return o, errors.New("invalid telegram options suffix")
	}
	if !validRef(o.TokenRef) {
		return o, errors.New("telegram requires a valid token_ref")
	}
	if duration, err := time.ParseDuration(o.Timeout); err != nil || duration <= 0 || duration > time.Duration(1<<63-1)-30*time.Second {
		return o, errors.New("telegram timeout must be positive and allow a 30s poll budget")
	}
	// 长轮询可保留已填写的 webhook 示例值，但填了就必须结构合法，避免切换时才发现错误。
	webhookSet := o.WebhookURL != "" || o.WebhookAddr != "" || o.WebhookPath != "" || o.WebhookSecretRef != ""
	switch o.Mode {
	case adapter.ModeLongPolling:
		if !webhookSet {
			return o, nil
		}
	case adapter.ModeWebhook:
	default:
		return o, errors.New("telegram mode must be long_polling or webhook")
	}
	if !adapter.ValidWebhookURL(o.WebhookURL) {
		return o, errors.New("telegram webhook_url must be a public HTTPS URL on port 443, 80, 88 or 8443")
	}
	if _, _, err := net.SplitHostPort(o.WebhookAddr); err != nil {
		return o, errors.New("invalid telegram webhook_addr")
	}
	if !strings.HasPrefix(o.WebhookPath, "/") || strings.ContainsAny(o.WebhookPath, "?# \r\n") {
		return o, errors.New("telegram webhook_path must be an absolute path")
	}
	if !validRef(o.WebhookSecretRef) || o.WebhookSecretRef == o.TokenRef {
		return o, errors.New("telegram requires a separate valid webhook_secret_ref")
	}
	return o, nil
}

type provider struct{}

func (provider) Type() string { return "telegram" }
func (provider) ValidateOptions(moduleRaw, raw json.RawMessage, bindings []platform.ConnectorBinding) error {
	if err := (Factory{}).ValidateOptions(moduleRaw); err != nil {
		return err
	}
	if _, err := parse(raw); err != nil {
		return err
	}
	if len(bindings) == 0 {
		return errors.New("telegram requires at least one group binding")
	}
	for _, binding := range bindings {
		id, err := strconv.ParseInt(binding.Target, 10, 64)
		if err != nil || id == 0 || strconv.FormatInt(id, 10) != binding.Target {
			return errors.New("telegram target must be a nonzero canonical decimal chat_id")
		}
	}
	return nil
}

func (provider) PublicOptions(_ json.RawMessage, raw json.RawMessage) (any, error) {
	o, err := parse(raw)
	if err != nil {
		return nil, err
	}
	return map[string]any{"token_configured": o.TokenRef != "", "mode": o.Mode, "timeout": o.Timeout,
		"webhook_url": o.WebhookURL, "webhook_addr": o.WebhookAddr, "webhook_path": o.WebhookPath, "webhook_secret_configured": o.WebhookSecretRef != ""}, nil
}

// CredentialRefs 只声明当前模式需要的凭据：长轮询不要求 webhook 认证文本已写入。
func (provider) CredentialRefs(_ json.RawMessage, raw json.RawMessage) ([]string, error) {
	o, err := parse(raw)
	if err != nil {
		return nil, err
	}
	if o.Mode == adapter.ModeWebhook {
		return []string{o.TokenRef, o.WebhookSecretRef}, nil
	}
	return []string{o.TokenRef}, nil
}

func (p provider) Build(_ context.Context, r platform.ConnectorBuildRequest) (platform.Adapter, error) {
	if err := p.ValidateOptions(r.ModuleOptions, r.ConnectorOptions, r.Bindings); err != nil {
		return nil, err
	}
	o, _ := parse(r.ConnectorOptions)
	timeout, _ := time.ParseDuration(o.Timeout)
	groups := make(map[int64]adapter.Group, len(r.Bindings))
	for _, binding := range r.Bindings {
		id, _ := strconv.ParseInt(binding.Target, 10, 64)
		groups[id] = adapter.Group{ConversationID: binding.ConversationID, PromptSuffix: binding.PromptSuffix}
	}
	cfg := adapter.Config{ID: r.ConnectorID, Token: r.Credentials[o.TokenRef], Mode: o.Mode, Timeout: timeout, Groups: groups, PromptSuffix: r.PromptSuffix}
	if o.Mode == adapter.ModeWebhook {
		cfg.WebhookURL, cfg.WebhookAddr, cfg.WebhookPath, cfg.WebhookSecret = o.WebhookURL, o.WebhookAddr, o.WebhookPath, r.Credentials[o.WebhookSecretRef]
	}
	return adapter.New(cfg, r.Logger)
}
