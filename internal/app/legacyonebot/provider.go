// Package legacyonebot temporarily adapts the pre-provider OneBot config to
// the generic connector builder. It is intentionally confined to the app
// composition layer and is removed when native.onebot owns the options model.
package legacyonebot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/platform/onebot"
)

func Options(connector config.ConnectorConfig) (json.RawMessage, error) {
	if connector.Type != "onebot" {
		return nil, nil
	}
	if connector.OneBot == nil {
		return nil, errors.New("onebot configuration is required")
	}
	return json.Marshal(connector.OneBot)
}

type Provider struct{}

func (Provider) Type() string { return "onebot" }
func (Provider) ValidateOptions(_ json.RawMessage, raw json.RawMessage, bindings []platform.ConnectorBinding) error {
	var options config.OneBotConnectorConfig
	if err := json.Unmarshal(raw, &options); err != nil {
		return err
	}
	if err := options.Validate("options"); err != nil {
		return err
	}
	if len(bindings) == 0 {
		return errors.New("must bind at least one group")
	}
	return nil
}
func (Provider) PublicOptions(json.RawMessage, json.RawMessage) (any, error) {
	return nil, errors.New("legacy OneBot provider does not expose configuration")
}
func (Provider) Build(_ context.Context, request platform.ConnectorBuildRequest) (platform.Adapter, error) {
	var options config.OneBotConnectorConfig
	if err := json.Unmarshal(request.ConnectorOptions, &options); err != nil {
		return nil, err
	}
	groups := make(map[string]onebot.Group, len(request.Bindings))
	for _, binding := range request.Bindings {
		groups[binding.Target] = onebot.Group{ConversationID: binding.ConversationID, PromptSuffix: binding.PromptSuffix}
	}
	adapter, err := onebot.New(onebot.Config{
		ID: request.ConnectorID, APIURL: options.APIURL, SendToken: options.SendToken,
		ReceiveSecret: options.ReceiveSecret, PromptSuffix: request.PromptSuffix,
		WebhookAddr: options.WebhookAddr, WebhookPath: options.WebhookPath,
		RequestLimit: options.RequestLimit, Timeout: options.Timeout,
		ReplayTTL: options.ReplayTTL, ReplayMax: options.ReplayMax, Groups: groups,
	}, request.Logger)
	if err != nil {
		return nil, fmt.Errorf("create adapter: %w", err)
	}
	return adapter, nil
}
