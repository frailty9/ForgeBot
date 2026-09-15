package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/llm"
	runtimecfg "github.com/soleapi/forgebot/internal/runtime"
	"github.com/soleapi/forgebot/internal/secret"
)

// SecretAction describes an explicit write-only update to a sensitive field.
type SecretAction string

const (
	SecretKeep    SecretAction = "keep"
	SecretReplace SecretAction = "replace"
	SecretDelete  SecretAction = "delete"
)

// SecretField is safe in both read and write payloads. Reads expose only
// Configured; writes may request keep, replace, or delete and never echo Value.
type SecretField struct {
	Configured bool         `json:"configured"`
	Action     SecretAction `json:"action,omitempty"`
	Value      string       `json:"value,omitempty"`
}

type StaticConfigView struct {
	Server            StaticServerView `json:"server"`
	RuntimeConfigPath string           `json:"runtime_config_path"`
}

type StaticServerView struct {
	Addr            string `json:"addr"`
	ReadTimeout     string `json:"read_timeout"`
	WriteTimeout    string `json:"write_timeout"`
	ShutdownTimeout string `json:"shutdown_timeout"`
}

type RuntimeConfig struct {
	Log            config.LogConfig            `json:"log"`
	Bot            config.BotConfig            `json:"bot"`
	Gate           config.GateConfig           `json:"gate"`
	LLM            LLMConfig                   `json:"llm"`
	Search         config.SearchConfig         `json:"search"`
	Platforms      PlatformConfig              `json:"platforms"`
	Administration config.AdministrationConfig `json:"administration"`
	Reflection     config.ReflectionConfig     `json:"reflection"`
	Operations     config.OperationsConfig     `json:"operations"`
	Extensions     ExtensionsConfig            `json:"extensions"`
}

type LLMConfig struct {
	Protocol         llm.Protocol `json:"protocol"`
	BaseURL          string       `json:"base_url"`
	APIKeyRef        SecretField  `json:"api_key_ref"`
	Model            string       `json:"model"`
	AnthropicVersion string       `json:"anthropic_version,omitempty"`
	Timeout          Duration     `json:"timeout"`
}

type PlatformConfig struct {
	Connectors []ConnectorConfig           `json:"connectors"`
	Groups     []config.GroupBindingConfig `json:"groups"`
}

type ConnectorConfig struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	Enabled      bool          `json:"enabled"`
	PromptSuffix string        `json:"prompt_suffix,omitempty"`
	OneBot       *OneBotConfig `json:"onebot,omitempty"`
}

type OneBotConfig struct {
	APIURL        string      `json:"api_url"`
	SendToken     SecretField `json:"send_token"`
	ReceiveSecret SecretField `json:"receive_secret"`
	WebhookAddr   string      `json:"webhook_addr"`
	WebhookPath   string      `json:"webhook_path"`
	RequestLimit  int64       `json:"request_limit"`
	Timeout       Duration    `json:"timeout"`
	ReplayTTL     Duration    `json:"replay_ttl"`
	ReplayMax     int         `json:"replay_max"`
}

type ExtensionsConfig struct {
	MCP    []MCPServerConfig   `json:"mcp"`
	Skills config.SkillsConfig `json:"skills"`
}

type MCPServerConfig struct {
	ID               string                 `json:"id"`
	Enabled          bool                   `json:"enabled"`
	Transport        string                 `json:"transport"`
	Endpoint         string                 `json:"endpoint"`
	Command          string                 `json:"command,omitempty"`
	Args             []string               `json:"args,omitempty"`
	WorkingDirectory string                 `json:"working_directory,omitempty"`
	Environment      map[string]SecretField `json:"environment,omitempty"`
	APIKeyRef        SecretField            `json:"api_key_ref"`
	AllowedTools     []string               `json:"allowed_tools"`
	Timeout          Duration               `json:"timeout"`
	MaxResponseBytes int64                  `json:"max_response_bytes"`
}

type ConfigResponse struct {
	Version       uint64            `json:"version"`
	UpdatedAt     time.Time         `json:"updated_at"`
	Static        StaticConfigView  `json:"static"`
	Runtime       RuntimeConfig     `json:"runtime"`
	RuntimeStatus runtimecfg.Status `json:"runtime_status"`
	LLMAPIKey     secret.Status     `json:"llm_api_key"`
}

type ApplyResponse struct {
	Action runtimecfg.Action `json:"action"`
	Config ConfigResponse    `json:"config"`
}

type ValidateRequest struct {
	Runtime RuntimeConfig `json:"runtime"`
}

// Duration preserves the runtime document's human-readable duration strings.
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return errors.New("duration must be a string")
	}
	value, err := time.ParseDuration(text)
	if err != nil {
		return err
	}
	*d = Duration(value)
	return nil
}

// FieldError associates a stable JSON field path with a validation failure.
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

func runtimeView(value config.RuntimeConfig) RuntimeConfig {
	result := RuntimeConfig{
		Log: value.Log, Bot: value.Bot, Gate: value.Gate,
		LLM: LLMConfig{
			Protocol: value.LLM.Protocol, BaseURL: value.LLM.BaseURL,
			APIKeyRef: SecretField{Configured: strings.TrimSpace(value.LLM.APIKeyRef) != ""},
			Model:     value.LLM.Model, AnthropicVersion: value.LLM.AnthropicVersion, Timeout: Duration(value.LLM.Timeout),
		},
		Search: value.Search, Administration: value.Administration,
		Reflection: value.Reflection, Operations: value.Operations,
		Platforms:  PlatformConfig{Groups: value.Platforms.Groups},
		Extensions: ExtensionsConfig{Skills: value.Extensions.Skills},
	}
	result.Platforms.Connectors = make([]ConnectorConfig, 0, len(value.Platforms.Connectors))
	for _, connector := range value.Platforms.Connectors {
		item := ConnectorConfig{ID: connector.ID, Type: connector.Type, Enabled: connector.Enabled, PromptSuffix: connector.PromptSuffix}
		if connector.OneBot != nil {
			item.OneBot = &OneBotConfig{
				APIURL:        connector.OneBot.APIURL,
				SendToken:     SecretField{Configured: strings.TrimSpace(connector.OneBot.SendToken) != ""},
				ReceiveSecret: SecretField{Configured: strings.TrimSpace(connector.OneBot.ReceiveSecret) != ""},
				WebhookAddr:   connector.OneBot.WebhookAddr, WebhookPath: connector.OneBot.WebhookPath,
				RequestLimit: connector.OneBot.RequestLimit, Timeout: Duration(connector.OneBot.Timeout),
				ReplayTTL: Duration(connector.OneBot.ReplayTTL), ReplayMax: connector.OneBot.ReplayMax,
			}
		}
		result.Platforms.Connectors = append(result.Platforms.Connectors, item)
	}
	result.Extensions.MCP = make([]MCPServerConfig, 0, len(value.Extensions.MCP))
	for _, server := range value.Extensions.MCP {
		environment := make(map[string]SecretField, len(server.Environment))
		for key, entry := range server.Environment {
			environment[key] = SecretField{Configured: entry != ""}
		}
		result.Extensions.MCP = append(result.Extensions.MCP, MCPServerConfig{
			ID: server.ID, Enabled: server.Enabled, Transport: server.Transport, Endpoint: server.Endpoint,
			Command: server.Command, Args: server.Args, WorkingDirectory: server.WorkingDirectory,
			Environment: environment, APIKeyRef: SecretField{Configured: strings.TrimSpace(server.APIKeyRef) != ""},
			AllowedTools: server.AllowedTools, Timeout: Duration(server.Timeout), MaxResponseBytes: server.MaxResponseBytes,
		})
	}
	return result
}

func configResponse(snapshot config.Snapshot, status runtimecfg.Status, apiKeyStatus secret.Status) ConfigResponse {
	return ConfigResponse{
		Version: snapshot.Version, UpdatedAt: snapshot.UpdatedAt,
		Static: StaticConfigView{Server: StaticServerView{
			Addr: snapshot.Static.Server.Addr, ReadTimeout: snapshot.Static.Server.ReadTimeout.String(),
			WriteTimeout: snapshot.Static.Server.WriteTimeout.String(), ShutdownTimeout: snapshot.Static.Server.ShutdownTimeout.String(),
		}, RuntimeConfigPath: snapshot.Static.RuntimeConfigPath},
		Runtime: runtimeView(snapshot.Runtime), RuntimeStatus: status, LLMAPIKey: apiKeyStatus,
	}
}

func (value RuntimeConfig) resolve(current config.RuntimeConfig) (config.RuntimeConfig, error) {
	apiKeyRef, err := value.LLM.APIKeyRef.resolve(current.LLM.APIKeyRef)
	if err != nil {
		return config.RuntimeConfig{}, &FieldError{Field: "runtime.llm.api_key_ref", Err: err}
	}
	result := config.RuntimeConfig{
		Log: value.Log, Bot: value.Bot, Gate: value.Gate,
		LLM: config.LLMConfig{
			Protocol: value.LLM.Protocol, BaseURL: value.LLM.BaseURL, APIKeyRef: apiKeyRef,
			Model: value.LLM.Model, AnthropicVersion: value.LLM.AnthropicVersion, Timeout: time.Duration(value.LLM.Timeout),
		},
		Search: value.Search, Administration: value.Administration,
		Reflection: value.Reflection, Operations: value.Operations,
		Platforms:  config.PlatformConfig{Groups: value.Platforms.Groups},
		Extensions: config.ExtensionsConfig{Skills: value.Extensions.Skills},
	}
	connectors := make(map[string]config.ConnectorConfig, len(current.Platforms.Connectors))
	for _, connector := range current.Platforms.Connectors {
		connectors[connector.ID] = connector
	}
	result.Platforms.Connectors = make([]config.ConnectorConfig, 0, len(value.Platforms.Connectors))
	for index, connector := range value.Platforms.Connectors {
		item := config.ConnectorConfig{ID: connector.ID, Type: connector.Type, Enabled: connector.Enabled, PromptSuffix: connector.PromptSuffix}
		if connector.OneBot != nil {
			var prior config.OneBotConnectorConfig
			if existing, ok := connectors[connector.ID]; ok && existing.OneBot != nil {
				prior = *existing.OneBot
			}
			sendToken, err := connector.OneBot.SendToken.resolve(prior.SendToken)
			if err != nil {
				return config.RuntimeConfig{}, &FieldError{Field: fmt.Sprintf("runtime.platforms.connectors[%d].onebot.send_token", index), Err: err}
			}
			receiveSecret, err := connector.OneBot.ReceiveSecret.resolve(prior.ReceiveSecret)
			if err != nil {
				return config.RuntimeConfig{}, &FieldError{Field: fmt.Sprintf("runtime.platforms.connectors[%d].onebot.receive_secret", index), Err: err}
			}
			item.OneBot = &config.OneBotConnectorConfig{
				APIURL: connector.OneBot.APIURL, SendToken: sendToken, ReceiveSecret: receiveSecret,
				WebhookAddr: connector.OneBot.WebhookAddr, WebhookPath: connector.OneBot.WebhookPath,
				RequestLimit: connector.OneBot.RequestLimit, Timeout: time.Duration(connector.OneBot.Timeout),
				ReplayTTL: time.Duration(connector.OneBot.ReplayTTL), ReplayMax: connector.OneBot.ReplayMax,
			}
		}
		result.Platforms.Connectors = append(result.Platforms.Connectors, item)
	}
	mcpServers := make(map[string]config.MCPServerConfig, len(current.Extensions.MCP))
	for _, server := range current.Extensions.MCP {
		mcpServers[server.ID] = server
	}
	result.Extensions.MCP = make([]config.MCPServerConfig, 0, len(value.Extensions.MCP))
	for index, server := range value.Extensions.MCP {
		prior := mcpServers[server.ID]
		apiKeyRef, err := server.APIKeyRef.resolve(prior.APIKeyRef)
		if err != nil {
			return config.RuntimeConfig{}, &FieldError{Field: fmt.Sprintf("runtime.extensions.mcp[%d].api_key_ref", index), Err: err}
		}
		environment := make(map[string]string, len(prior.Environment)+len(server.Environment))
		for key, entry := range prior.Environment {
			environment[key] = entry
		}
		for key, field := range server.Environment {
			resolved, resolveErr := field.resolve(environment[key])
			if resolveErr != nil {
				return config.RuntimeConfig{}, &FieldError{Field: fmt.Sprintf("runtime.extensions.mcp[%d].environment.%s", index, key), Err: resolveErr}
			}
			if field.action() == SecretDelete {
				delete(environment, key)
			} else if resolved != "" || field.Configured || field.action() == SecretReplace {
				environment[key] = resolved
			}
		}
		result.Extensions.MCP = append(result.Extensions.MCP, config.MCPServerConfig{
			ID: server.ID, Enabled: server.Enabled, Transport: server.Transport, Endpoint: server.Endpoint,
			Command: server.Command, Args: server.Args, WorkingDirectory: server.WorkingDirectory,
			Environment: environment, APIKeyRef: apiKeyRef, AllowedTools: server.AllowedTools,
			Timeout: time.Duration(server.Timeout), MaxResponseBytes: server.MaxResponseBytes,
		})
	}
	return result, nil
}

func (field SecretField) action() SecretAction {
	if field.Action == "" {
		return SecretKeep
	}
	return field.Action
}

func (field SecretField) resolve(current string) (string, error) {
	switch field.action() {
	case SecretKeep:
		if field.Value != "" {
			return "", errors.New("value requires action replace")
		}
		if field.Configured && current == "" {
			return "", errors.New("cannot keep a secret that is not available")
		}
		return current, nil
	case SecretReplace:
		if strings.TrimSpace(field.Value) == "" {
			return "", errors.New("replacement value is required")
		}
		return field.Value, nil
	case SecretDelete:
		if field.Value != "" {
			return "", errors.New("delete action must not include value")
		}
		return "", nil
	default:
		return "", fmt.Errorf("action must be one of keep, replace, delete")
	}
}
