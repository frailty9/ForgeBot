// Package qqbot 通过独立 Provider 注册 QQ 官方群聊 Webhook 连接器。
package qqbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/platform"
	adapter "github.com/soleapi/forgebot/internal/platform/qqbot"
)

const ID = "native.qqbot"

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
		return errors.New("qqbot module options must be an empty object; configure each connector instance")
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
	AppID        string `json:"app_id"`
	SecretRef    string `json:"secret_ref"`
	WebhookAddr  string `json:"webhook_addr"`
	WebhookPath  string `json:"webhook_path"`
	Timeout      string `json:"timeout,omitempty"`
	RequestLimit int64  `json:"request_limit,omitempty"`
	ReplayTTL    string `json:"replay_ttl,omitempty"`
	ReplayMax    int    `json:"replay_max,omitempty"`
}

func parse(raw json.RawMessage) (options, error) {
	o := options{Timeout: "15s", RequestLimit: 1 << 20, ReplayTTL: "5m", ReplayMax: 10000}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&o); err != nil {
		return o, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return o, errors.New("invalid qqbot options suffix")
	}
	if strings.TrimSpace(o.AppID) == "" || strings.TrimSpace(o.SecretRef) == "" || strings.TrimSpace(o.WebhookAddr) == "" || !strings.HasPrefix(o.WebhookPath, "/") || strings.ContainsAny(o.WebhookPath, "?# \r\n") {
		return o, errors.New("qqbot requires app_id, secret_ref, webhook_addr and an absolute webhook_path")
	}
	if _, _, err := net.SplitHostPort(o.WebhookAddr); err != nil {
		return o, errors.New("invalid qqbot webhook_addr")
	}
	for _, duration := range []string{o.Timeout, o.ReplayTTL} {
		if value, err := time.ParseDuration(duration); err != nil || value <= 0 {
			return o, errors.New("qqbot durations must be positive")
		}
	}
	if o.RequestLimit < 1 || o.RequestLimit > 8<<20 || o.ReplayMax < 1 || o.ReplayMax > 100000 {
		return o, errors.New("qqbot request_limit or replay_max exceeds bounds")
	}
	return o, nil
}

type provider struct{}

func (provider) Type() string { return "qqbot" }
func (provider) ValidateOptions(moduleRaw, raw json.RawMessage, bindings []platform.ConnectorBinding) error {
	if err := (Factory{}).ValidateOptions(moduleRaw); err != nil {
		return err
	}
	if _, err := parse(raw); err != nil {
		return err
	}
	if len(bindings) == 0 {
		return errors.New("qqbot requires at least one group binding")
	}
	for _, binding := range bindings {
		if strings.TrimSpace(binding.Target) == "" {
			return errors.New("qqbot target must be a group_openid")
		}
	}
	return nil
}
func (provider) PublicOptions(_ json.RawMessage, raw json.RawMessage) (any, error) {
	o, err := parse(raw)
	if err != nil {
		return nil, err
	}
	return map[string]any{"app_id": o.AppID, "secret_configured": o.SecretRef != "", "webhook_addr": o.WebhookAddr, "webhook_path": o.WebhookPath, "timeout": o.Timeout, "request_limit": o.RequestLimit, "replay_ttl": o.ReplayTTL, "replay_max": o.ReplayMax}, nil
}
func (provider) CredentialRefs(_ json.RawMessage, raw json.RawMessage) ([]string, error) {
	o, err := parse(raw)
	return []string{o.SecretRef}, err
}
func (p provider) Build(_ context.Context, r platform.ConnectorBuildRequest) (platform.Adapter, error) {
	if err := p.ValidateOptions(r.ModuleOptions, r.ConnectorOptions, r.Bindings); err != nil {
		return nil, err
	}
	o, _ := parse(r.ConnectorOptions)
	timeout, _ := time.ParseDuration(o.Timeout)
	ttl, _ := time.ParseDuration(o.ReplayTTL)
	groups := map[string]adapter.Group{}
	for _, b := range r.Bindings {
		groups[b.Target] = adapter.Group{ConversationID: b.ConversationID, PromptSuffix: b.PromptSuffix}
	}
	return adapter.New(adapter.Config{ID: r.ConnectorID, AppID: o.AppID, Secret: r.Credentials[o.SecretRef], WebhookAddr: o.WebhookAddr, WebhookPath: o.WebhookPath, Timeout: timeout, RequestLimit: o.RequestLimit, ReplayTTL: ttl, ReplayMax: o.ReplayMax, Groups: groups, PromptSuffix: r.PromptSuffix}, r.Logger)
}
