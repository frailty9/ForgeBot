package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/soleapi/forgebot/internal/llm"
)

func (r ReflectionConfig) MarshalJSON() ([]byte, error) {
	type encoded struct {
		Enabled                    bool     `json:"enabled"`
		Mode                       string   `json:"mode"`
		ConversationIDs            []string `json:"conversation_ids"`
		ScanInterval               string   `json:"scan_interval"`
		MessageTTL                 string   `json:"message_ttl"`
		QuietWindow                string   `json:"quiet_window"`
		MaxSessionDuration         string   `json:"max_session_duration"`
		MaxMessagesPerConversation int      `json:"max_messages_per_conversation"`
		MaxMessagesTotal           int      `json:"max_messages_total"`
		MaxMessageChars            int      `json:"max_message_chars"`
		MaxPairsPerScan            int      `json:"max_pairs_per_scan"`
		MinConfidence              float64  `json:"min_confidence"`
		ExtraDirectory             string   `json:"extra_directory"`
	}
	return json.Marshal(encoded{r.Enabled, r.Mode, r.ConversationIDs, r.ScanInterval.String(), r.MessageTTL.String(), r.QuietWindow.String(), r.MaxSessionDuration.String(), r.MaxMessagesPerConversation, r.MaxMessagesTotal, r.MaxMessageChars, r.MaxPairsPerScan, r.MinConfidence, r.ExtraDirectory})
}

func (c MCPServerConfig) MarshalJSON() ([]byte, error) {
	type encoded struct {
		ID               string            `json:"id"`
		Enabled          bool              `json:"enabled"`
		Transport        string            `json:"transport"`
		Endpoint         string            `json:"endpoint"`
		Command          string            `json:"command,omitempty"`
		Args             []string          `json:"args,omitempty"`
		WorkingDirectory string            `json:"working_directory,omitempty"`
		Environment      map[string]string `json:"environment,omitempty"`
		APIKeyRef        string            `json:"api_key_ref,omitempty"`
		AllowedTools     []string          `json:"allowed_tools"`
		Timeout          string            `json:"timeout"`
		MaxResponseBytes int64             `json:"max_response_bytes"`
	}
	return json.Marshal(encoded{c.ID, c.Enabled, c.Transport, c.Endpoint, c.Command, c.Args, c.WorkingDirectory, c.Environment, c.APIKeyRef, c.AllowedTools, c.Timeout.String(), c.MaxResponseBytes})
}

func (c *MCPServerConfig) UnmarshalJSON(data []byte) error {
	type raw struct {
		ID               string            `json:"id"`
		Enabled          bool              `json:"enabled"`
		Transport        string            `json:"transport"`
		Endpoint         string            `json:"endpoint"`
		Command          string            `json:"command"`
		Args             []string          `json:"args"`
		WorkingDirectory string            `json:"working_directory"`
		Environment      map[string]string `json:"environment"`
		APIKeyRef        string            `json:"api_key_ref"`
		AllowedTools     []string          `json:"allowed_tools"`
		Timeout          json.RawMessage   `json:"timeout"`
		MaxResponseBytes int64             `json:"max_response_bytes"`
	}
	var value raw
	if err := decodeStrictJSON(data, &value); err != nil {
		return err
	}
	defaults := DefaultRuntime().Extensions.MCP
	timeout, err := decodeDuration(value.Timeout, 10*time.Second)
	if err != nil {
		return fmt.Errorf("timeout: %w", err)
	}
	*c = MCPServerConfig{ID: value.ID, Enabled: value.Enabled, Transport: value.Transport, Endpoint: value.Endpoint, Command: value.Command, Args: value.Args, WorkingDirectory: value.WorkingDirectory, Environment: value.Environment, APIKeyRef: value.APIKeyRef, AllowedTools: value.AllowedTools, Timeout: timeout, MaxResponseBytes: value.MaxResponseBytes}
	if c.Transport == "" {
		c.Transport = "streamable_http"
	}
	if c.MaxResponseBytes == 0 {
		c.MaxResponseBytes = 1 << 20
	}
	_ = defaults
	return nil
}

func (r *ReflectionConfig) UnmarshalJSON(data []byte) error {
	type raw struct {
		Enabled                    *bool           `json:"enabled"`
		Mode                       *string         `json:"mode"`
		ConversationIDs            *[]string       `json:"conversation_ids"`
		ScanInterval               json.RawMessage `json:"scan_interval"`
		MessageTTL                 json.RawMessage `json:"message_ttl"`
		QuietWindow                json.RawMessage `json:"quiet_window"`
		MaxSessionDuration         json.RawMessage `json:"max_session_duration"`
		MaxMessagesPerConversation *int            `json:"max_messages_per_conversation"`
		MaxMessagesTotal           *int            `json:"max_messages_total"`
		MaxMessageChars            *int            `json:"max_message_chars"`
		MaxPairsPerScan            *int            `json:"max_pairs_per_scan"`
		MinConfidence              *float64        `json:"min_confidence"`
		ExtraDirectory             *string         `json:"extra_directory"`
	}
	defaults := DefaultRuntime().Reflection
	var value raw
	if err := decodeStrictJSON(data, &value); err != nil {
		return err
	}
	*r = defaults
	if value.Enabled != nil {
		r.Enabled = *value.Enabled
	}
	if value.Mode != nil {
		r.Mode = *value.Mode
	}
	if value.ConversationIDs != nil {
		r.ConversationIDs = append([]string(nil), (*value.ConversationIDs)...)
	}
	var err error
	if r.ScanInterval, err = decodeDuration(value.ScanInterval, defaults.ScanInterval); err != nil {
		return fmt.Errorf("scan_interval: %w", err)
	}
	if r.MessageTTL, err = decodeDuration(value.MessageTTL, defaults.MessageTTL); err != nil {
		return fmt.Errorf("message_ttl: %w", err)
	}
	if r.QuietWindow, err = decodeDuration(value.QuietWindow, defaults.QuietWindow); err != nil {
		return fmt.Errorf("quiet_window: %w", err)
	}
	if r.MaxSessionDuration, err = decodeDuration(value.MaxSessionDuration, defaults.MaxSessionDuration); err != nil {
		return fmt.Errorf("max_session_duration: %w", err)
	}
	if value.MaxMessagesPerConversation != nil {
		r.MaxMessagesPerConversation = *value.MaxMessagesPerConversation
	}
	if value.MaxMessagesTotal != nil {
		r.MaxMessagesTotal = *value.MaxMessagesTotal
	}
	if value.MaxMessageChars != nil {
		r.MaxMessageChars = *value.MaxMessageChars
	}
	if value.MaxPairsPerScan != nil {
		r.MaxPairsPerScan = *value.MaxPairsPerScan
	}
	if value.MinConfidence != nil {
		r.MinConfidence = *value.MinConfidence
	}
	if value.ExtraDirectory != nil {
		r.ExtraDirectory = *value.ExtraDirectory
	}
	return nil
}

func (a AdmissionConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		DedupTTL string `json:"dedup_ttl"`
		DedupMax int    `json:"dedup_max"`
	}{DedupTTL: a.DedupTTL.String(), DedupMax: a.DedupMax})
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

func (s SearchConfig) MarshalJSON() ([]byte, error) {
	type encoded SearchConfig
	return json.Marshal(struct {
		encoded
		QueryTimeout string `json:"query_timeout"`
		BuildTimeout string `json:"build_timeout"`
	}{encoded: encoded(s), QueryTimeout: s.QueryTimeout.String(), BuildTimeout: s.BuildTimeout.String()})
}

func (s *SearchConfig) UnmarshalJSON(data []byte) error {
	type rawConfig struct {
		Enabled           *bool           `json:"enabled"`
		Policy            *string         `json:"policy"`
		Backend           *string         `json:"backend"`
		DocumentRoot      *string         `json:"document_root"`
		Extensions        *[]string       `json:"extensions"`
		DefaultResults    *int            `json:"default_results"`
		MaxResults        *int            `json:"max_results"`
		MinMatchRatio     *float64        `json:"min_match_ratio"`
		QueryTimeout      json.RawMessage `json:"query_timeout"`
		BuildTimeout      json.RawMessage `json:"build_timeout"`
		MaxQueriesPerTurn *int            `json:"max_queries_per_turn"`
		MaxContextChars   *int            `json:"max_context_chars"`
		MaxSectionChars   *int            `json:"max_section_chars"`
		MaxFileBytes      *int64          `json:"max_file_bytes"`
		MaxTotalBytes     *int64          `json:"max_total_bytes"`
		MaxFiles          *int            `json:"max_files"`
	}
	defaults := DefaultRuntime().Search
	var raw rawConfig
	if err := decodeStrictJSON(data, &raw); err != nil {
		return err
	}
	*s = defaults
	if raw.Enabled != nil {
		s.Enabled = *raw.Enabled
	}
	if raw.Policy != nil {
		s.Policy = *raw.Policy
	}
	if raw.Backend != nil {
		s.Backend = *raw.Backend
	}
	if raw.DocumentRoot != nil {
		s.DocumentRoot = *raw.DocumentRoot
	}
	if raw.Extensions != nil {
		s.Extensions = append([]string(nil), (*raw.Extensions)...)
	}
	if raw.DefaultResults != nil {
		s.DefaultResults = *raw.DefaultResults
	}
	if raw.MaxResults != nil {
		s.MaxResults = *raw.MaxResults
	}
	if raw.MinMatchRatio != nil {
		s.MinMatchRatio = *raw.MinMatchRatio
	}
	if raw.MaxQueriesPerTurn != nil {
		s.MaxQueriesPerTurn = *raw.MaxQueriesPerTurn
	}
	if raw.MaxContextChars != nil {
		s.MaxContextChars = *raw.MaxContextChars
	}
	if raw.MaxSectionChars != nil {
		s.MaxSectionChars = *raw.MaxSectionChars
	}
	if raw.MaxFileBytes != nil {
		s.MaxFileBytes = *raw.MaxFileBytes
	}
	if raw.MaxTotalBytes != nil {
		s.MaxTotalBytes = *raw.MaxTotalBytes
	}
	if raw.MaxFiles != nil {
		s.MaxFiles = *raw.MaxFiles
	}
	var err error
	if s.QueryTimeout, err = decodeDuration(raw.QueryTimeout, defaults.QueryTimeout); err != nil {
		return fmt.Errorf("query_timeout: %w", err)
	}
	if s.BuildTimeout, err = decodeDuration(raw.BuildTimeout, defaults.BuildTimeout); err != nil {
		return fmt.Errorf("build_timeout: %w", err)
	}
	return nil
}

func (c OneBotConnectorConfig) MarshalJSON() ([]byte, error) {
	type encoded struct {
		APIURL        string `json:"api_url"`
		SendToken     string `json:"send_token,omitempty"`
		ReceiveSecret string `json:"receive_secret,omitempty"`
		WebhookAddr   string `json:"webhook_addr"`
		WebhookPath   string `json:"webhook_path"`
		RequestLimit  int64  `json:"request_limit"`
		Timeout       string `json:"timeout"`
		ReplayTTL     string `json:"replay_ttl"`
		ReplayMax     int    `json:"replay_max"`
	}
	return json.Marshal(encoded{
		APIURL: c.APIURL, SendToken: c.SendToken, ReceiveSecret: c.ReceiveSecret,
		WebhookAddr: c.WebhookAddr, WebhookPath: c.WebhookPath, RequestLimit: c.RequestLimit,
		Timeout: c.Timeout.String(), ReplayTTL: c.ReplayTTL.String(), ReplayMax: c.ReplayMax,
	})
}

func (c *OneBotConnectorConfig) UnmarshalJSON(data []byte) error {
	type rawConfig struct {
		APIURL        string          `json:"api_url"`
		SendToken     string          `json:"send_token"`
		ReceiveSecret string          `json:"receive_secret"`
		WebhookAddr   string          `json:"webhook_addr"`
		WebhookPath   string          `json:"webhook_path"`
		RequestLimit  int64           `json:"request_limit"`
		Timeout       json.RawMessage `json:"timeout"`
		ReplayTTL     json.RawMessage `json:"replay_ttl"`
		ReplayMax     int             `json:"replay_max"`
	}
	var raw rawConfig
	if err := decodeStrictJSON(data, &raw); err != nil {
		return err
	}
	timeout, err := decodeDuration(raw.Timeout, 15*time.Second)
	if err != nil {
		return fmt.Errorf("timeout: %w", err)
	}
	replayTTL, err := decodeDuration(raw.ReplayTTL, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("replay_ttl: %w", err)
	}
	replayMax := raw.ReplayMax
	if replayMax == 0 {
		replayMax = 10000
	}
	*c = OneBotConnectorConfig{
		APIURL: raw.APIURL, SendToken: raw.SendToken, ReceiveSecret: raw.ReceiveSecret,
		WebhookAddr: raw.WebhookAddr, WebhookPath: raw.WebhookPath, RequestLimit: raw.RequestLimit,
		Timeout: timeout, ReplayTTL: replayTTL, ReplayMax: replayMax,
	}
	return nil
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

func (a *AdmissionConfig) UnmarshalJSON(data []byte) error {
	type rawAdmissionConfig struct {
		DedupTTL json.RawMessage `json:"dedup_ttl"`
		DedupMax *int            `json:"dedup_max"`
	}
	defaults := DefaultRuntime().Admission
	var raw rawAdmissionConfig
	if err := decodeStrictJSON(data, &raw); err != nil {
		return err
	}
	*a = defaults
	if raw.DedupMax != nil {
		a.DedupMax = *raw.DedupMax
	}
	var err error
	if a.DedupTTL, err = decodeDuration(raw.DedupTTL, defaults.DedupTTL); err != nil {
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
