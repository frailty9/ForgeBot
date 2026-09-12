package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/llm"
	"gopkg.in/yaml.v3"
)

const (
	envConfigPath      = "FORGEBOT_CONFIG"
	envHTTPAddr        = "FORGEBOT_HTTP_ADDR"
	envRuntimeConfig   = "FORGEBOT_RUNTIME_CONFIG"
	envSecretStorePath = "FORGEBOT_SECRET_STORE"
	envAdminToken      = "FORGEBOT_ADMIN_TOKEN"
	defaultAPIKeyRef   = "llm/default"
)

type LoadOptions struct {
	Path        string
	RuntimePath string
	LookupEnv   func(string) string
	Now         func() time.Time
}

type LoadReport struct {
	StaticSource  string
	StaticPath    string
	RuntimeSource string
	RuntimePath   string
}

type Snapshot struct {
	Version   uint64        `json:"version"`
	UpdatedAt time.Time     `json:"updated_at"`
	Static    StaticConfig  `json:"static"`
	Runtime   RuntimeConfig `json:"runtime"`
}

type StaticConfig struct {
	Server            ServerConfig `json:"server" yaml:"server"`
	RuntimeConfigPath string       `json:"runtime_config_path" yaml:"runtime_config_path"`
	SecretStorePath   string       `json:"secret_store_path" yaml:"secret_store_path"`
	AdminToken        string       `json:"-" yaml:"admin_token"`
}

type RuntimeConfig struct {
	Log        LogConfig        `json:"log"`
	Bot        BotConfig        `json:"bot"`
	Gate       GateConfig       `json:"gate"`
	LLM        LLMConfig        `json:"llm"`
	Platforms  PlatformConfig   `json:"platforms"`
	Operations OperationsConfig `json:"operations"`
}

type RuntimeDocument struct {
	Version   uint64            `json:"version"`
	UpdatedAt time.Time         `json:"updated_at"`
	Source    string            `json:"source,omitempty"`
	Runtime   RuntimeConfig     `json:"runtime"`
	History   []RuntimeRevision `json:"history,omitempty"`
}

type RuntimeRevision struct {
	Version   uint64        `json:"version"`
	UpdatedAt time.Time     `json:"updated_at"`
	Source    string        `json:"source,omitempty"`
	Runtime   RuntimeConfig `json:"runtime"`
}

type ServerConfig struct {
	Addr            string        `json:"addr" yaml:"addr"`
	ReadTimeout     time.Duration `json:"read_timeout" yaml:"read_timeout"`
	WriteTimeout    time.Duration `json:"write_timeout" yaml:"write_timeout"`
	ShutdownTimeout time.Duration `json:"shutdown_timeout" yaml:"shutdown_timeout"`
}

type LogConfig struct {
	Level  string `json:"level" yaml:"level"`
	Format string `json:"format" yaml:"format"`
}

type BotConfig struct {
	Name         string `json:"name" yaml:"name"`
	SystemPrompt string `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
}

type LLMConfig struct {
	Protocol         llm.Protocol  `json:"protocol" yaml:"protocol"`
	BaseURL          string        `json:"base_url" yaml:"base_url"`
	APIKeyRef        string        `json:"api_key_ref" yaml:"api_key_ref"`
	Model            string        `json:"model" yaml:"model"`
	AnthropicVersion string        `json:"anthropic_version,omitempty" yaml:"anthropic_version,omitempty"`
	Timeout          time.Duration `json:"timeout" yaml:"timeout"`
}

// ClientConfig keeps secret resolution outside the configuration model.
func (c LLMConfig) ClientConfig(apiKey string) llm.ClientConfig {
	return llm.ClientConfig{
		Protocol: c.Protocol, BaseURL: c.BaseURL, APIKey: apiKey, Model: c.Model,
		AnthropicVersion: c.AnthropicVersion, Timeout: c.Timeout,
	}
}

type GateConfig struct {
	Enabled        bool          `json:"enabled" yaml:"enabled"`
	RequireMention bool          `json:"require_mention" yaml:"require_mention"`
	Silence        time.Duration `json:"silence" yaml:"silence"`
	MaxCount       int           `json:"max_count" yaml:"max_count"`
	MaxWindow      time.Duration `json:"max_window" yaml:"max_window"`
	DedupTTL       time.Duration `json:"dedup_ttl" yaml:"dedup_ttl"`
	DedupMax       int           `json:"dedup_max" yaml:"dedup_max"`
	MaxBuffered    int           `json:"max_buffered" yaml:"max_buffered"`
	MaxTextLen     int           `json:"max_text_len" yaml:"max_text_len"`
	MaxConcurrent  int           `json:"max_concurrent" yaml:"max_concurrent"`
}

type PlatformConfig struct {
	Connectors []ConnectorConfig    `json:"connectors" yaml:"connectors"`
	Groups     []GroupBindingConfig `json:"groups" yaml:"groups"`
}

type ConnectorConfig struct {
	ID      string                 `json:"id" yaml:"id"`
	Type    string                 `json:"type" yaml:"type"`
	Enabled bool                   `json:"enabled" yaml:"enabled"`
	OneBot  *OneBotConnectorConfig `json:"onebot,omitempty" yaml:"onebot,omitempty"`
}

type GroupBindingConfig struct {
	ID          string `json:"id" yaml:"id"`
	ConnectorID string `json:"connector_id" yaml:"connector_id"`
	Target      string `json:"target" yaml:"target"`
}

type OperationsConfig struct {
	Groups []OperatorGroupConfig `json:"groups" yaml:"groups"`
}

// OperatorGroupConfig grants a logical conversation a deliberately small
// operations surface, separate from the complete Web administration API.
type OperatorGroupConfig struct {
	ConversationID string   `json:"conversation_id" yaml:"conversation_id"`
	Notifications  bool     `json:"notifications" yaml:"notifications"`
	Commands       []string `json:"commands" yaml:"commands"`
}

// OneBotConnectorConfig contains application-level connection settings. The
// protocol credentials are runtime-configurable so a management service
// can replace an adapter generation without changing startup configuration.
type OneBotConnectorConfig struct {
	APIURL        string        `json:"api_url" yaml:"api_url"`
	SendToken     string        `json:"send_token,omitempty" yaml:"send_token,omitempty"`
	ReceiveSecret string        `json:"receive_secret,omitempty" yaml:"receive_secret,omitempty"`
	WebhookAddr   string        `json:"webhook_addr" yaml:"webhook_addr"`
	WebhookPath   string        `json:"webhook_path" yaml:"webhook_path"`
	RequestLimit  int64         `json:"request_limit" yaml:"request_limit"`
	Timeout       time.Duration `json:"timeout" yaml:"timeout"`
	ReplayTTL     time.Duration `json:"replay_ttl" yaml:"replay_ttl"`
	ReplayMax     int           `json:"replay_max" yaml:"replay_max"`
}

func DefaultStatic() StaticConfig {
	return StaticConfig{Server: ServerConfig{
		Addr: "127.0.0.1:8080", ReadTimeout: 10 * time.Second,
		WriteTimeout: 30 * time.Second, ShutdownTimeout: 15 * time.Second,
	}}
}

func DefaultRuntime() RuntimeConfig {
	return RuntimeConfig{
		Log: LogConfig{Level: "info", Format: "text"},
		Bot: BotConfig{Name: "ForgeBot"},
		LLM: LLMConfig{Protocol: llm.OpenAIResponses, APIKeyRef: defaultAPIKeyRef, Timeout: 60 * time.Second},
		Gate: GateConfig{
			Enabled: false, RequireMention: true, Silence: 4 * time.Second, MaxCount: 8,
			MaxWindow: 25 * time.Second, DedupTTL: 60 * time.Second, DedupMax: 10000,
			MaxBuffered: 50, MaxTextLen: 500, MaxConcurrent: 4,
		},
		Platforms: PlatformConfig{
			Connectors: []ConnectorConfig{{ID: "echo", Type: "echo", Enabled: true}},
			Groups:     []GroupBindingConfig{{ID: "local", ConnectorID: "echo", Target: "local"}},
		},
		Operations: OperationsConfig{},
	}
}

func DefaultSnapshot() Snapshot {
	return Snapshot{Version: 1, UpdatedAt: time.Now().UTC(), Static: DefaultStatic(), Runtime: DefaultRuntime()}
}

func Load(opts LoadOptions) (Snapshot, LoadReport, error) {
	lookupEnv := opts.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	static := DefaultStatic()
	report := LoadReport{StaticSource: "defaults", RuntimeSource: "defaults"}
	staticPath := strings.TrimSpace(opts.Path)
	if staticPath == "" {
		staticPath = strings.TrimSpace(lookupEnv(envConfigPath))
	}
	if staticPath != "" {
		if err := loadStaticYAML(staticPath, &static); err != nil {
			return Snapshot{}, report, fmt.Errorf("load static config: %w", err)
		}
		report.StaticSource, report.StaticPath = "yaml", staticPath
	}

	applyStaticEnv(&static, lookupEnv)
	if err := static.Validate(); err != nil {
		return Snapshot{}, report, err
	}

	runtimePath := strings.TrimSpace(opts.RuntimePath)
	runtimePathFromYAML := false
	if runtimePath == "" {
		runtimePath = strings.TrimSpace(lookupEnv(envRuntimeConfig))
	}
	if runtimePath == "" {
		runtimePath = strings.TrimSpace(static.RuntimeConfigPath)
		runtimePathFromYAML = runtimePath != ""
	}
	if runtimePathFromYAML && !filepath.IsAbs(runtimePath) && staticPath != "" {
		runtimePath = filepath.Join(filepath.Dir(staticPath), runtimePath)
	}
	static.RuntimeConfigPath = runtimePath
	secretPathOverridden := strings.TrimSpace(lookupEnv(envSecretStorePath)) != ""
	if static.SecretStorePath != "" && !secretPathOverridden && !filepath.IsAbs(static.SecretStorePath) && staticPath != "" {
		static.SecretStorePath = filepath.Join(filepath.Dir(staticPath), static.SecretStorePath)
	}

	runtime := DefaultRuntime()
	version := uint64(1)
	updatedAt := now().UTC()
	if runtimePath != "" {
		document, recovered, err := ReadRuntimeDocumentWithRecovery(runtimePath, now)
		if err != nil {
			return Snapshot{}, report, fmt.Errorf("load runtime config: %w", err)
		}
		runtime, version, updatedAt = document.Runtime, document.Version, document.UpdatedAt
		report.RuntimeSource, report.RuntimePath = "json", runtimePath
		if recovered {
			report.RuntimeSource = "json_backup"
		}
	}
	if err := runtime.Validate(); err != nil {
		return Snapshot{}, report, err
	}

	return Snapshot{Version: version, UpdatedAt: updatedAt, Static: static, Runtime: runtime}, report, nil
}

func ReadRuntimeDocument(path string, now func() time.Time) (RuntimeDocument, error) {
	document, _, err := ReadRuntimeDocumentWithRecovery(path, now)
	return document, err
}

func ReadRuntimeDocumentWithRecovery(path string, now func() time.Time) (RuntimeDocument, bool, error) {
	document, err := readRuntimeDocumentFile(path, now)
	if err == nil {
		return document, false, nil
	}
	backup, backupErr := readRuntimeDocumentFile(path+".bak", now)
	if backupErr == nil {
		return backup, true, nil
	}
	return RuntimeDocument{}, false, err
}

func readRuntimeDocumentFile(path string, now func() time.Time) (RuntimeDocument, error) {
	if now == nil {
		now = time.Now
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return RuntimeDocument{}, err
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(data, &shape); err != nil {
		return RuntimeDocument{}, err
	}
	if _, versioned := shape["runtime"]; !versioned {
		runtime := DefaultRuntime()
		if err := decodeStrictJSON(data, &runtime); err != nil {
			return RuntimeDocument{}, err
		}
		if err := runtime.Validate(); err != nil {
			return RuntimeDocument{}, err
		}
		return RuntimeDocument{Version: 1, UpdatedAt: now().UTC(), Source: "legacy", Runtime: runtime}, nil
	}
	var document RuntimeDocument
	if err := decodeStrictJSON(data, &document); err != nil {
		return RuntimeDocument{}, err
	}
	if document.Version == 0 {
		return RuntimeDocument{}, errors.New("runtime document version must be positive")
	}
	if document.UpdatedAt.IsZero() {
		return RuntimeDocument{}, errors.New("runtime document updated_at is required")
	}
	if err := document.Runtime.Validate(); err != nil {
		return RuntimeDocument{}, err
	}
	for _, revision := range document.History {
		if revision.Version == 0 || revision.UpdatedAt.IsZero() {
			return RuntimeDocument{}, errors.New("runtime history metadata is invalid")
		}
		if err := revision.Runtime.Validate(); err != nil {
			return RuntimeDocument{}, fmt.Errorf("runtime history version %d: %w", revision.Version, err)
		}
	}
	return document, nil
}

func loadStaticYAML(path string, target *StaticConfig) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	type rawServerConfig struct {
		Addr            string `yaml:"addr"`
		ReadTimeout     string `yaml:"read_timeout"`
		WriteTimeout    string `yaml:"write_timeout"`
		ShutdownTimeout string `yaml:"shutdown_timeout"`
	}
	type rawStaticConfig struct {
		Server            *rawServerConfig `yaml:"server"`
		RuntimeConfigPath string           `yaml:"runtime_config_path"`
		SecretStorePath   string           `yaml:"secret_store_path"`
		AdminToken        string           `yaml:"admin_token"`
	}
	var raw rawStaticConfig
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple YAML documents are not allowed")
		}
		return err
	}
	out := *target
	if raw.Server != nil {
		if raw.Server.Addr != "" {
			out.Server.Addr = raw.Server.Addr
		}
		var parseErr error
		if raw.Server.ReadTimeout != "" {
			out.Server.ReadTimeout, parseErr = time.ParseDuration(raw.Server.ReadTimeout)
			if parseErr != nil {
				return fmt.Errorf("server.read_timeout: %w", parseErr)
			}
		}
		if raw.Server.WriteTimeout != "" {
			out.Server.WriteTimeout, parseErr = time.ParseDuration(raw.Server.WriteTimeout)
			if parseErr != nil {
				return fmt.Errorf("server.write_timeout: %w", parseErr)
			}
		}
		if raw.Server.ShutdownTimeout != "" {
			out.Server.ShutdownTimeout, parseErr = time.ParseDuration(raw.Server.ShutdownTimeout)
			if parseErr != nil {
				return fmt.Errorf("server.shutdown_timeout: %w", parseErr)
			}
		}
	}
	out.RuntimeConfigPath = raw.RuntimeConfigPath
	out.SecretStorePath = raw.SecretStorePath
	out.AdminToken = raw.AdminToken
	*target = out
	return nil
}

func decodeJSONFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func applyStaticEnv(cfg *StaticConfig, lookupEnv func(string) string) {
	if value := strings.TrimSpace(lookupEnv(envHTTPAddr)); value != "" {
		cfg.Server.Addr = value
	}
	if value := strings.TrimSpace(lookupEnv(envRuntimeConfig)); value != "" {
		cfg.RuntimeConfigPath = value
	}
	if value := strings.TrimSpace(lookupEnv(envSecretStorePath)); value != "" {
		cfg.SecretStorePath = value
	}
	if value := strings.TrimSpace(lookupEnv(envAdminToken)); value != "" {
		cfg.AdminToken = value
	}
}

func (c StaticConfig) Validate() error {
	var problems []string
	if strings.TrimSpace(c.Server.Addr) == "" {
		problems = append(problems, "server.addr is required")
	}
	if c.Server.ReadTimeout <= 0 {
		problems = append(problems, "server.read_timeout must be positive")
	}
	if c.Server.WriteTimeout <= 0 {
		problems = append(problems, "server.write_timeout must be positive")
	}
	if c.Server.ShutdownTimeout <= 0 {
		problems = append(problems, "server.shutdown_timeout must be positive")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (c RuntimeConfig) Validate() error {
	var problems []string
	if strings.TrimSpace(c.Bot.Name) == "" {
		problems = append(problems, "bot.name is required")
	}
	if err := c.Log.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if err := c.Gate.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if err := c.LLM.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if err := c.Platforms.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if err := c.Operations.Validate(c.Platforms); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (c OperationsConfig) Validate(platforms PlatformConfig) error {
	conversations := make(map[string]struct{}, len(platforms.Groups))
	for _, binding := range platforms.Groups {
		conversations[strings.TrimSpace(binding.ID)] = struct{}{}
	}
	configured := make(map[string]struct{}, len(c.Groups))
	var problems []string
	for index, group := range c.Groups {
		conversationID := strings.TrimSpace(group.ConversationID)
		prefix := fmt.Sprintf("operations.groups[%d]", index)
		if conversationID == "" {
			problems = append(problems, prefix+".conversation_id is required")
		} else if _, exists := conversations[conversationID]; !exists {
			problems = append(problems, prefix+".conversation_id must reference a bound conversation")
		} else if _, exists := configured[conversationID]; exists {
			problems = append(problems, prefix+".conversation_id is duplicated")
		} else {
			configured[conversationID] = struct{}{}
		}
		commands := make(map[string]struct{}, len(group.Commands))
		for commandIndex, command := range group.Commands {
			command = strings.ToLower(strings.TrimSpace(command))
			commandPrefix := fmt.Sprintf("%s.commands[%d]", prefix, commandIndex)
			if !isOperatorCommand(command) {
				problems = append(problems, commandPrefix+" must be one of status, restart, pause, resume, gate, reload_docs")
			} else if _, exists := commands[command]; exists {
				problems = append(problems, commandPrefix+" is duplicated")
			} else {
				commands[command] = struct{}{}
			}
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func isOperatorCommand(command string) bool {
	switch command {
	case "status", "restart", "pause", "resume", "gate", "reload_docs":
		return true
	default:
		return false
	}
}

func (c PlatformConfig) Validate() error {
	var problems []string
	connectors := make(map[string]ConnectorConfig, len(c.Connectors))
	for index, connector := range c.Connectors {
		connector.ID = strings.TrimSpace(connector.ID)
		connector.Type = strings.TrimSpace(connector.Type)
		prefix := fmt.Sprintf("platforms.connectors[%d]", index)
		if connector.ID == "" {
			problems = append(problems, prefix+".id is required")
			continue
		}
		if _, exists := connectors[connector.ID]; exists {
			problems = append(problems, prefix+".id is duplicated")
		}
		connectors[connector.ID] = connector
		if connector.Type != "echo" && connector.Type != "onebot" {
			problems = append(problems, prefix+".type must be echo or onebot")
		}
		if connector.Type == "onebot" && connector.Enabled {
			if connector.OneBot == nil {
				problems = append(problems, prefix+".onebot is required")
				continue
			}
			if err := connector.OneBot.Validate(prefix + ".onebot"); err != nil {
				problems = append(problems, err.Error())
			}
		}
	}
	groups := make(map[string]struct{}, len(c.Groups))
	targets := make(map[string]struct{}, len(c.Groups))
	bindingsPerConnector := make(map[string]int, len(c.Connectors))
	for index, group := range c.Groups {
		group.ID, group.ConnectorID, group.Target = strings.TrimSpace(group.ID), strings.TrimSpace(group.ConnectorID), strings.TrimSpace(group.Target)
		prefix := fmt.Sprintf("platforms.groups[%d]", index)
		if group.ID == "" {
			problems = append(problems, prefix+".id is required")
		} else if _, exists := groups[group.ID]; exists {
			problems = append(problems, prefix+".id is duplicated")
		} else {
			groups[group.ID] = struct{}{}
		}
		connector, exists := connectors[group.ConnectorID]
		if !exists || !connector.Enabled {
			problems = append(problems, prefix+".connector_id must reference an enabled connector")
		}
		if group.Target == "" {
			problems = append(problems, prefix+".target is required")
		}
		key := group.ConnectorID + "\x00" + group.Target
		if _, exists := targets[key]; exists {
			problems = append(problems, prefix+" duplicates a connector target")
		} else {
			targets[key] = struct{}{}
		}
		bindingsPerConnector[group.ConnectorID]++
	}
	for _, connector := range c.Connectors {
		if connector.Enabled && connector.Type == "onebot" && bindingsPerConnector[connector.ID] == 0 {
			problems = append(problems, fmt.Sprintf("platforms connector %s must bind at least one group", connector.ID))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (c OneBotConnectorConfig) Validate(prefix string) error {
	var problems []string
	if strings.TrimSpace(c.APIURL) == "" {
		problems = append(problems, prefix+".api_url is required")
	}
	if strings.TrimSpace(c.WebhookAddr) == "" {
		problems = append(problems, prefix+".webhook_addr is required")
	}
	if path := strings.TrimSpace(c.WebhookPath); path == "" || !strings.HasPrefix(path, "/") {
		problems = append(problems, prefix+".webhook_path must start with /")
	}
	if c.RequestLimit <= 0 {
		problems = append(problems, prefix+".request_limit must be positive")
	}
	if c.Timeout <= 0 {
		problems = append(problems, prefix+".timeout must be positive")
	}
	if c.ReplayTTL <= 0 {
		problems = append(problems, prefix+".replay_ttl must be positive")
	}
	if c.ReplayMax <= 0 {
		problems = append(problems, prefix+".replay_max must be positive")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (c Snapshot) Validate() error { return errors.Join(c.Static.Validate(), c.Runtime.Validate()) }

func (c LLMConfig) Validate() error {
	if !c.Protocol.Valid() {
		return fmt.Errorf("llm.protocol must be one of %s, %s, %s", llm.AnthropicMessages, llm.OpenAIResponses, llm.OpenAICompletions)
	}
	if strings.TrimSpace(c.APIKeyRef) == "" {
		return errors.New("llm.api_key_ref is required")
	}
	if c.Timeout <= 0 {
		return errors.New("llm.timeout must be positive")
	}
	return nil
}

func (c LogConfig) Validate() error {
	switch strings.ToLower(c.Level) {
	case "debug", "info", "warn", "error":
	default:
		return errors.New("log.level must be one of debug, info, warn, error")
	}
	switch strings.ToLower(c.Format) {
	case "text", "json":
	default:
		return errors.New("log.format must be text or json")
	}
	return nil
}

func (c GateConfig) Validate() error {
	var problems []string
	if c.Silence < 0 {
		problems = append(problems, "gate.silence must not be negative")
	}
	if c.MaxCount < 0 {
		problems = append(problems, "gate.max_count must not be negative")
	}
	if c.MaxWindow < 0 {
		problems = append(problems, "gate.max_window must not be negative")
	}
	if c.DedupTTL <= 0 {
		problems = append(problems, "gate.dedup_ttl must be positive")
	}
	if c.DedupMax <= 0 {
		problems = append(problems, "gate.dedup_max must be positive")
	}
	if c.MaxBuffered < 0 {
		problems = append(problems, "gate.max_buffered must not be negative")
	}
	if c.MaxTextLen < 0 {
		problems = append(problems, "gate.max_text_len must not be negative")
	}
	if c.MaxConcurrent <= 0 {
		problems = append(problems, "gate.max_concurrent must be positive")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (c Snapshot) Public(apiKeyConfigured bool) map[string]any {
	return map[string]any{
		"version": c.Version, "updated_at": c.UpdatedAt,
		"static": map[string]any{
			"server": map[string]any{
				"addr":             c.Static.Server.Addr,
				"read_timeout":     c.Static.Server.ReadTimeout.String(),
				"write_timeout":    c.Static.Server.WriteTimeout.String(),
				"shutdown_timeout": c.Static.Server.ShutdownTimeout.String(),
			},
			"runtime_config_path": c.Static.RuntimeConfigPath,
		},
		"runtime": map[string]any{
			"log": c.Runtime.Log, "bot": c.Runtime.Bot,
			"llm": map[string]any{
				"protocol": c.Runtime.LLM.Protocol, "base_url": c.Runtime.LLM.BaseURL,
				"model": c.Runtime.LLM.Model, "anthropic_version": c.Runtime.LLM.AnthropicVersion,
				"timeout": c.Runtime.LLM.Timeout.String(), "api_key_configured": apiKeyConfigured,
			},
			"gate": map[string]any{
				"enabled": c.Runtime.Gate.Enabled, "require_mention": c.Runtime.Gate.RequireMention,
				"silence": c.Runtime.Gate.Silence.String(), "max_count": c.Runtime.Gate.MaxCount,
				"max_window": c.Runtime.Gate.MaxWindow.String(), "dedup_ttl": c.Runtime.Gate.DedupTTL.String(),
				"dedup_max": c.Runtime.Gate.DedupMax, "max_buffered": c.Runtime.Gate.MaxBuffered,
				"max_text_len": c.Runtime.Gate.MaxTextLen, "max_concurrent": c.Runtime.Gate.MaxConcurrent,
			},
			"platforms":  c.Runtime.Platforms.Public(),
			"operations": c.Runtime.Operations,
		},
	}
}

func (c PlatformConfig) Public() map[string]any {
	connectors := make([]any, 0, len(c.Connectors))
	for _, connector := range c.Connectors {
		item := map[string]any{"id": connector.ID, "type": connector.Type, "enabled": connector.Enabled}
		if connector.OneBot != nil {
			item["onebot"] = map[string]any{
				"api_url": connector.OneBot.APIURL, "webhook_addr": connector.OneBot.WebhookAddr,
				"webhook_path": connector.OneBot.WebhookPath, "request_limit": connector.OneBot.RequestLimit,
				"timeout":                   connector.OneBot.Timeout.String(),
				"replay_ttl":                connector.OneBot.ReplayTTL.String(),
				"replay_max":                connector.OneBot.ReplayMax,
				"send_token_configured":     strings.TrimSpace(connector.OneBot.SendToken) != "",
				"receive_secret_configured": strings.TrimSpace(connector.OneBot.ReceiveSecret) != "",
			}
		}
		connectors = append(connectors, item)
	}
	return map[string]any{"connectors": connectors, "groups": c.Groups}
}

func BoolEnv(value string) (bool, bool) {
	if strings.TrimSpace(value) == "" {
		return false, false
	}
	parsed, err := strconv.ParseBool(value)
	return parsed, err == nil
}
