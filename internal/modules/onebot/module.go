// Package onebot exposes OneBot 11 as a native connector provider. Protocol
// options, credentials, and group routing stay here rather than in core config.
package onebot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/platform"
	adapter "github.com/soleapi/forgebot/internal/platform/onebot"
)

const ID = "native.onebot"

// Factory contributes the OneBot provider when the module is enabled.
type Factory struct{}

func (Factory) Descriptor() modules.Descriptor {
	return modules.Descriptor{
		ID: ID, DefaultEnabled: false,
		Registrations: []modules.RegistrationKind{modules.RegistrationConnectorProviders},
	}
}

func (Factory) ValidateOptions(raw json.RawMessage) error {
	_, err := parseOptions(raw)
	return err
}
func (Factory) PublicOptions(raw json.RawMessage) (any, error) {
	options, err := parseOptions(raw)
	if err != nil {
		return nil, err
	}
	return options.apply(rawOptions{}).public(), nil
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

type provider struct{}

func (provider) Type() string { return "onebot" }
func (provider) ValidateOptions(moduleRaw, connectorRaw json.RawMessage, bindings []platform.ConnectorBinding) error {
	if len(bindings) == 0 {
		return errors.New("must bind at least one group")
	}
	_, err := merge(moduleRaw, connectorRaw)
	return err
}
func (provider) PublicOptions(moduleRaw, connectorRaw json.RawMessage) (any, error) {
	options, err := merge(moduleRaw, connectorRaw)
	if err != nil {
		return nil, err
	}
	return options.public(), nil
}
func (provider) Build(_ context.Context, request platform.ConnectorBuildRequest) (platform.Adapter, error) {
	options, err := merge(request.ModuleOptions, request.ConnectorOptions)
	if err != nil {
		return nil, err
	}
	if len(request.Bindings) == 0 {
		return nil, errors.New("must bind at least one group")
	}
	groups := make(map[string]adapter.Group, len(request.Bindings))
	for _, binding := range request.Bindings {
		groups[binding.Target] = adapter.Group{ConversationID: binding.ConversationID, PromptSuffix: binding.PromptSuffix}
	}
	return adapter.New(adapter.Config{
		ID: request.ConnectorID, APIURL: options.APIURL, SendToken: options.SendToken,
		ReceiveSecret: options.ReceiveSecret, PromptSuffix: request.PromptSuffix,
		WebhookAddr: options.WebhookAddr, WebhookPath: options.WebhookPath,
		RequestLimit: options.RequestLimit, Timeout: options.Timeout,
		ReplayTTL: options.ReplayTTL, ReplayMax: options.ReplayMax, Groups: groups,
	}, request.Logger)
}

type options struct {
	APIURL        string
	SendToken     string
	ReceiveSecret string
	WebhookAddr   string
	WebhookPath   string
	RequestLimit  int64
	Timeout       time.Duration
	ReplayTTL     time.Duration
	ReplayMax     int
}

type rawOptions struct {
	APIURL        *string         `json:"api_url"`
	SendToken     *string         `json:"send_token"`
	ReceiveSecret *string         `json:"receive_secret"`
	WebhookAddr   *string         `json:"webhook_addr"`
	WebhookPath   *string         `json:"webhook_path"`
	RequestLimit  *int64          `json:"request_limit"`
	Timeout       json.RawMessage `json:"timeout"`
	ReplayTTL     json.RawMessage `json:"replay_ttl"`
	ReplayMax     *int            `json:"replay_max"`
}

func merge(moduleRaw, connectorRaw json.RawMessage) (options, error) {
	module, err := parseOptions(moduleRaw)
	if err != nil {
		return options{}, fmt.Errorf("module options: %w", err)
	}
	connector, err := parseOptions(connectorRaw)
	if err != nil {
		return options{}, fmt.Errorf("connector options: %w", err)
	}
	result := module.apply(connector)
	if result.Timeout == 0 {
		result.Timeout = 15 * time.Second
	}
	if result.ReplayTTL == 0 {
		result.ReplayTTL = 5 * time.Minute
	}
	if result.ReplayMax == 0 {
		result.ReplayMax = 10000
	}
	if err := result.validate(); err != nil {
		return options{}, err
	}
	return result, nil
}

func parseOptions(raw json.RawMessage) (rawOptions, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return rawOptions{}, nil
	}
	var decoded rawOptions
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return rawOptions{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return rawOptions{}, errors.New("multiple JSON values are not allowed")
	} else if !errors.Is(err, io.EOF) {
		return rawOptions{}, err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return rawOptions{}, errors.New("options must be an object")
	}
	if _, err := parseDuration(decoded.Timeout); err != nil {
		return rawOptions{}, fmt.Errorf("timeout: %w", err)
	}
	if _, err := parseDuration(decoded.ReplayTTL); err != nil {
		return rawOptions{}, fmt.Errorf("replay_ttl: %w", err)
	}
	return decoded, nil
}

func parseDuration(raw json.RawMessage) (time.Duration, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, err
	}
	return time.ParseDuration(text)
}

func (o rawOptions) apply(override rawOptions) options {
	if override.APIURL != nil {
		o.APIURL = override.APIURL
	}
	if override.SendToken != nil {
		o.SendToken = override.SendToken
	}
	if override.ReceiveSecret != nil {
		o.ReceiveSecret = override.ReceiveSecret
	}
	if override.WebhookAddr != nil {
		o.WebhookAddr = override.WebhookAddr
	}
	if override.WebhookPath != nil {
		o.WebhookPath = override.WebhookPath
	}
	if override.RequestLimit != nil {
		o.RequestLimit = override.RequestLimit
	}
	if len(override.Timeout) > 0 {
		o.Timeout = override.Timeout
	}
	if len(override.ReplayTTL) > 0 {
		o.ReplayTTL = override.ReplayTTL
	}
	if override.ReplayMax != nil {
		o.ReplayMax = override.ReplayMax
	}
	result := options{}
	if o.APIURL != nil {
		result.APIURL = *o.APIURL
	}
	if o.SendToken != nil {
		result.SendToken = *o.SendToken
	}
	if o.ReceiveSecret != nil {
		result.ReceiveSecret = *o.ReceiveSecret
	}
	if o.WebhookAddr != nil {
		result.WebhookAddr = *o.WebhookAddr
	}
	if o.WebhookPath != nil {
		result.WebhookPath = *o.WebhookPath
	}
	if o.RequestLimit != nil {
		result.RequestLimit = *o.RequestLimit
	}
	result.Timeout, _ = parseDuration(o.Timeout)
	result.ReplayTTL, _ = parseDuration(o.ReplayTTL)
	if o.ReplayMax != nil {
		result.ReplayMax = *o.ReplayMax
	}
	return result
}

func (o options) validate() error {
	var problems []string
	if strings.TrimSpace(o.APIURL) == "" {
		problems = append(problems, "api_url is required")
	}
	if strings.TrimSpace(o.WebhookAddr) == "" {
		problems = append(problems, "webhook_addr is required")
	}
	if path := strings.TrimSpace(o.WebhookPath); path == "" || !strings.HasPrefix(path, "/") {
		problems = append(problems, "webhook_path must start with /")
	}
	if o.RequestLimit <= 0 {
		problems = append(problems, "request_limit must be positive")
	}
	if o.Timeout <= 0 {
		problems = append(problems, "timeout must be positive")
	}
	if o.ReplayTTL <= 0 {
		problems = append(problems, "replay_ttl must be positive")
	}
	if o.ReplayMax <= 0 {
		problems = append(problems, "replay_max must be positive")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (o options) public() map[string]any {
	return map[string]any{
		"api_url": o.APIURL, "webhook_addr": o.WebhookAddr, "webhook_path": o.WebhookPath,
		"request_limit": o.RequestLimit, "timeout": o.Timeout.String(), "replay_ttl": o.ReplayTTL.String(),
		"replay_max": o.ReplayMax, "send_token_configured": strings.TrimSpace(o.SendToken) != "",
		"receive_secret_configured": strings.TrimSpace(o.ReceiveSecret) != "",
	}
}
