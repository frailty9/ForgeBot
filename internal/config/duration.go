package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/soleapi/forgebot/internal/llm"
)

func (g GateConfig) MarshalJSON() ([]byte, error) {
	type encodedGateConfig struct {
		Enabled        bool   `json:"enabled"`
		RequireMention bool   `json:"require_mention"`
		Silence        string `json:"silence"`
		MaxCount       int    `json:"max_count"`
		MaxWindow      string `json:"max_window"`
		DedupTTL       string `json:"dedup_ttl"`
		DedupMax       int    `json:"dedup_max"`
		MaxBuffered    int    `json:"max_buffered"`
		MaxTextLen     int    `json:"max_text_len"`
		MaxConcurrent  int    `json:"max_concurrent"`
	}
	return json.Marshal(encodedGateConfig{
		Enabled: g.Enabled, RequireMention: g.RequireMention, Silence: g.Silence.String(),
		MaxCount: g.MaxCount, MaxWindow: g.MaxWindow.String(), DedupTTL: g.DedupTTL.String(),
		DedupMax: g.DedupMax, MaxBuffered: g.MaxBuffered, MaxTextLen: g.MaxTextLen,
		MaxConcurrent: g.MaxConcurrent,
	})
}

func (l LLMConfig) MarshalJSON() ([]byte, error) {
	type encodedLLMConfig struct {
		Protocol         llm.Protocol `json:"protocol"`
		BaseURL          string       `json:"base_url"`
		APIKeyRef        string       `json:"api_key_ref"`
		Model            string       `json:"model"`
		AnthropicVersion string       `json:"anthropic_version,omitempty"`
		Timeout          string       `json:"timeout"`
	}
	return json.Marshal(encodedLLMConfig{
		Protocol: l.Protocol, BaseURL: l.BaseURL, APIKeyRef: l.APIKeyRef, Model: l.Model,
		AnthropicVersion: l.AnthropicVersion, Timeout: l.Timeout.String(),
	})
}

func (d *ServerConfig) UnmarshalJSON(data []byte) error {
	type rawServerConfig struct {
		Addr            string          `json:"addr"`
		ReadTimeout     json.RawMessage `json:"read_timeout"`
		WriteTimeout    json.RawMessage `json:"write_timeout"`
		ShutdownTimeout json.RawMessage `json:"shutdown_timeout"`
	}
	defaults := DefaultStatic().Server
	out := rawServerConfig{
		Addr: defaults.Addr,
	}
	if err := decodeStrictJSON(data, &out); err != nil {
		return err
	}
	d.Addr = out.Addr
	var err error
	if d.ReadTimeout, err = decodeDuration(out.ReadTimeout, defaults.ReadTimeout); err != nil {
		return fmt.Errorf("read_timeout: %w", err)
	}
	if d.WriteTimeout, err = decodeDuration(out.WriteTimeout, defaults.WriteTimeout); err != nil {
		return fmt.Errorf("write_timeout: %w", err)
	}
	if d.ShutdownTimeout, err = decodeDuration(out.ShutdownTimeout, defaults.ShutdownTimeout); err != nil {
		return fmt.Errorf("shutdown_timeout: %w", err)
	}
	return nil
}

func (g *GateConfig) UnmarshalJSON(data []byte) error {
	type rawGateConfig struct {
		Enabled        *bool           `json:"enabled"`
		RequireMention *bool           `json:"require_mention"`
		Silence        json.RawMessage `json:"silence"`
		MaxCount       *int            `json:"max_count"`
		MaxWindow      json.RawMessage `json:"max_window"`
		DedupTTL       json.RawMessage `json:"dedup_ttl"`
		DedupMax       *int            `json:"dedup_max"`
		MaxBuffered    *int            `json:"max_buffered"`
		MaxTextLen     *int            `json:"max_text_len"`
		MaxConcurrent  *int            `json:"max_concurrent"`
	}
	defaults := DefaultRuntime().Gate
	var raw rawGateConfig
	if err := decodeStrictJSON(data, &raw); err != nil {
		return err
	}
	*g = defaults
	if raw.Enabled != nil {
		g.Enabled = *raw.Enabled
	}
	if raw.RequireMention != nil {
		g.RequireMention = *raw.RequireMention
	}
	if raw.MaxCount != nil {
		g.MaxCount = *raw.MaxCount
	}
	if raw.DedupMax != nil {
		g.DedupMax = *raw.DedupMax
	}
	if raw.MaxBuffered != nil {
		g.MaxBuffered = *raw.MaxBuffered
	}
	if raw.MaxTextLen != nil {
		g.MaxTextLen = *raw.MaxTextLen
	}
	if raw.MaxConcurrent != nil {
		g.MaxConcurrent = *raw.MaxConcurrent
	}
	var err error
	if g.Silence, err = decodeDuration(raw.Silence, defaults.Silence); err != nil {
		return fmt.Errorf("silence: %w", err)
	}
	if g.MaxWindow, err = decodeDuration(raw.MaxWindow, defaults.MaxWindow); err != nil {
		return fmt.Errorf("max_window: %w", err)
	}
	if g.DedupTTL, err = decodeDuration(raw.DedupTTL, defaults.DedupTTL); err != nil {
		return fmt.Errorf("dedup_ttl: %w", err)
	}
	return nil
}

func (l *LLMConfig) UnmarshalJSON(data []byte) error {
	type rawLLMConfig struct {
		Protocol         string          `json:"protocol"`
		BaseURL          string          `json:"base_url"`
		APIKeyRef        string          `json:"api_key_ref"`
		Model            string          `json:"model"`
		AnthropicVersion string          `json:"anthropic_version"`
		Timeout          json.RawMessage `json:"timeout"`
	}
	defaults := DefaultRuntime().LLM
	var raw rawLLMConfig
	if err := decodeStrictJSON(data, &raw); err != nil {
		return err
	}
	protocol := defaults.Protocol
	if raw.Protocol != "" {
		parsed, err := llm.ParseProtocol(raw.Protocol)
		if err != nil {
			return err
		}
		protocol = parsed
	}
	timeout, err := decodeDuration(raw.Timeout, defaults.Timeout)
	if err != nil {
		return fmt.Errorf("timeout: %w", err)
	}
	apiKeyRef := raw.APIKeyRef
	if apiKeyRef == "" {
		apiKeyRef = defaults.APIKeyRef
	}
	*l = LLMConfig{
		Protocol:         protocol,
		BaseURL:          raw.BaseURL,
		APIKeyRef:        apiKeyRef,
		Model:            raw.Model,
		AnthropicVersion: raw.AnthropicVersion,
		Timeout:          timeout,
	}
	return nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func decodeDuration(raw json.RawMessage, fallback time.Duration) (time.Duration, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return fallback, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		value, parseErr := time.ParseDuration(text)
		if parseErr != nil {
			return 0, parseErr
		}
		return value, nil
	}
	var seconds float64
	if err := json.Unmarshal(raw, &seconds); err != nil {
		return 0, err
	}
	return time.Duration(seconds * float64(time.Second)), nil
}
