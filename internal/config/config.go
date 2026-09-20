// Package config loads, normalizes, validates, and redacts ForgeBot startup
// and runtime configuration.
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

// LoadOptions controls configuration source selection and environment overrides.
type LoadOptions struct {
	Path        string
	RuntimePath string
	LookupEnv   func(string) string
	Now         func() time.Time
}

// LoadReport describes the effective configuration sources selected at startup.
type LoadReport struct {
	StaticSource  string
	StaticPath    string
	RuntimeSource string
	RuntimePath   string
}

// Snapshot is the immutable configuration pair used to build a runtime generation.
type Snapshot struct {
	Version   uint64        `json:"version"`
	UpdatedAt time.Time     `json:"updated_at"`
	Static    StaticConfig  `json:"static"`
	Runtime   RuntimeConfig `json:"runtime"`
}

// StaticConfig contains process-level settings that require restart to change.
type StaticConfig struct {
	Server            ServerConfig `json:"server" yaml:"server"`
	RuntimeConfigPath string       `json:"runtime_config_path" yaml:"runtime_config_path"`
	SecretStorePath   string       `json:"secret_store_path" yaml:"secret_store_path"`
	AdminToken        string       `json:"-" yaml:"admin_token"`
}

// RuntimeConfig contains hot-reloadable application behavior.
type RuntimeConfig struct {
	Modules        map[string]ModuleConfig `json:"modules,omitempty"`
	Log            LogConfig               `json:"log"`
	Bot            BotConfig               `json:"bot"`
	Admission      AdmissionConfig         `json:"admission"`
	LLM            LLMConfig               `json:"llm"`
	Platforms      PlatformConfig          `json:"platforms"`
	Administration AdministrationConfig    `json:"administration"`
	Reflection     ReflectionConfig        `json:"reflection"`
	Operations     OperationsConfig        `json:"operations"`
	Extensions     ExtensionsConfig        `json:"extensions"`
}

// ExtensionsConfig configures MCP servers and text-only skills.
type ExtensionsConfig struct {
	MCP    []MCPServerConfig `json:"mcp"`
	Skills SkillsConfig      `json:"skills"`
}

// MCPServerConfig describes one allow-listed MCP server connection.
type MCPServerConfig struct {
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
	Timeout          time.Duration     `json:"timeout"`
	MaxResponseBytes int64             `json:"max_response_bytes"`
}

// SkillsConfig bounds local SKILL.md loading.
type SkillsConfig struct {
	Directories   []string `json:"directories"`
	MaxFileBytes  int64    `json:"max_file_bytes"`
	MaxTotalBytes int64    `json:"max_total_bytes"`
	MaxFiles      int      `json:"max_files"`
}

// RuntimeDocument is the versioned JSON envelope persisted by the settings store.
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

// ServerConfig controls the management and ingress HTTP listener.
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

// LLMConfig selects one supported protocol family and model settings.
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

// AdmissionConfig controls non-optional inbound deduplication limits.
type AdmissionConfig struct {
	DedupTTL time.Duration `json:"dedup_ttl" yaml:"dedup_ttl"`
	DedupMax int           `json:"dedup_max" yaml:"dedup_max"`
}

// PlatformConfig declares connectors and logical conversation bindings.
type PlatformConfig struct {
	Connectors []ConnectorConfig    `json:"connectors" yaml:"connectors"`
	Groups     []GroupBindingConfig `json:"groups" yaml:"groups"`
}

// ConnectorConfig selects one platform adapter instance.
type ConnectorConfig struct {
	ID           string                 `json:"id" yaml:"id"`
	Type         string                 `json:"type" yaml:"type"`
	Enabled      bool                   `json:"enabled" yaml:"enabled"`
	PromptSuffix string                 `json:"prompt_suffix,omitempty" yaml:"prompt_suffix,omitempty"`
	OneBot       *OneBotConnectorConfig `json:"onebot,omitempty" yaml:"onebot,omitempty"`
}

// GroupBindingConfig maps an external target to a logical conversation.
type GroupBindingConfig struct {
	ID           string `json:"id" yaml:"id"`
	ConnectorID  string `json:"connector_id" yaml:"connector_id"`
	Target       string `json:"target" yaml:"target"`
	PromptSuffix string `json:"prompt_suffix,omitempty" yaml:"prompt_suffix,omitempty"`
}

// OperationsConfig grants limited command access to conversations.
type OperationsConfig struct {
	Groups []OperatorGroupConfig `json:"groups" yaml:"groups"`
}

// AdministrationConfig lists platform identities treated as administrators.
type AdministrationConfig struct {
	Administrators []AdministratorConfig `json:"administrators" yaml:"administrators"`
}

// ReflectionConfig controls periodic conversion of approved answers into documents.
type ReflectionConfig struct {
	Enabled                    bool          `json:"enabled" yaml:"enabled"`
	Mode                       string        `json:"mode" yaml:"mode"`
	ConversationIDs            []string      `json:"conversation_ids" yaml:"conversation_ids"`
	ScanInterval               time.Duration `json:"scan_interval" yaml:"scan_interval"`
	MessageTTL                 time.Duration `json:"message_ttl" yaml:"message_ttl"`
	QuietWindow                time.Duration `json:"quiet_window" yaml:"quiet_window"`
	MaxSessionDuration         time.Duration `json:"max_session_duration" yaml:"max_session_duration"`
	MaxMessagesPerConversation int           `json:"max_messages_per_conversation" yaml:"max_messages_per_conversation"`
	MaxMessagesTotal           int           `json:"max_messages_total" yaml:"max_messages_total"`
	MaxMessageChars            int           `json:"max_message_chars" yaml:"max_message_chars"`
	MaxPairsPerScan            int           `json:"max_pairs_per_scan" yaml:"max_pairs_per_scan"`
	MinConfidence              float64       `json:"min_confidence" yaml:"min_confidence"`
	ExtraDirectory             string        `json:"extra_directory" yaml:"extra_directory"`
}

// AdministratorConfig scopes a platform user ID to a logical conversation.
// User IDs are not assumed to be globally unique across connectors or accounts.
type AdministratorConfig struct {
	ConversationID string `json:"conversation_id" yaml:"conversation_id"`
	UserID         string `json:"user_id" yaml:"user_id"`
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
		Log:       LogConfig{Level: "info", Format: "text"},
		Bot:       BotConfig{Name: "ForgeBot"},
		LLM:       LLMConfig{Protocol: llm.OpenAIResponses, APIKeyRef: defaultAPIKeyRef, Timeout: 60 * time.Second},
		Admission: AdmissionConfig{DedupTTL: 60 * time.Second, DedupMax: 10000},
		Platforms: PlatformConfig{
			Connectors: []ConnectorConfig{{ID: "echo", Type: "echo", Enabled: true}},
			Groups:     []GroupBindingConfig{{ID: "local", ConnectorID: "echo", Target: "local"}},
		},
		Reflection: ReflectionConfig{
			Enabled: false, Mode: "off", ScanInterval: 15 * time.Minute,
			MessageTTL: 24 * time.Hour, QuietWindow: 3 * time.Minute,
			MaxSessionDuration: 30 * time.Minute, MaxMessagesPerConversation: 1000,
			MaxMessagesTotal: 10000, MaxMessageChars: 4000, MaxPairsPerScan: 50,
			MinConfidence: 0.9, ExtraDirectory: "extra",
		},
		Operations: OperationsConfig{},
		Extensions: ExtensionsConfig{Skills: SkillsConfig{MaxFileBytes: 128 << 10, MaxTotalBytes: 1 << 20, MaxFiles: 64}},
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
		normalizeExtensionsDefaults(&runtime)
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
		normalizeExtensionsDefaults(&runtime)
		if err := runtime.Validate(); err != nil {
			return RuntimeDocument{}, err
		}
		return RuntimeDocument{Version: 1, UpdatedAt: now().UTC(), Source: "legacy", Runtime: runtime}, nil
	}
	var document RuntimeDocument
	if err := decodeStrictJSON(data, &document); err != nil {
		return RuntimeDocument{}, err
	}
	normalizeExtensionsDefaults(&document.Runtime)
	if document.Version == 0 {
		return RuntimeDocument{}, errors.New("runtime document version must be positive")
	}
	if document.UpdatedAt.IsZero() {
		return RuntimeDocument{}, errors.New("runtime document updated_at is required")
	}
	if err := document.Runtime.Validate(); err != nil {
		return RuntimeDocument{}, err
	}
	for index := range document.History {
		revision := &document.History[index]
		normalizeExtensionsDefaults(&revision.Runtime)
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
	if err := c.Admission.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if err := c.LLM.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if err := c.Platforms.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if err := c.Administration.Validate(c.Platforms); err != nil {
		problems = append(problems, err.Error())
	}
	if err := c.Reflection.Validate(c.Platforms); err != nil {
		problems = append(problems, err.Error())
	}
	if err := c.Operations.Validate(c.Platforms); err != nil {
		problems = append(problems, err.Error())
	}
	if err := c.Extensions.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if err := ValidateModules(c.Modules); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func normalizeExtensionsDefaults(runtime *RuntimeConfig) {
	if runtime.Extensions.Skills.MaxFileBytes == 0 && runtime.Extensions.Skills.MaxTotalBytes == 0 && runtime.Extensions.Skills.MaxFiles == 0 {
		runtime.Extensions.Skills = DefaultRuntime().Extensions.Skills
	}
}

func (c ExtensionsConfig) Validate() error {
	var problems []string
	seen := map[string]struct{}{}
	for i, server := range c.MCP {
		prefix := fmt.Sprintf("extensions.mcp[%d]", i)
		id := strings.TrimSpace(server.ID)
		if id == "" {
			problems = append(problems, prefix+".id is required")
		} else if _, ok := seen[id]; ok {
			problems = append(problems, prefix+".id is duplicated")
		} else {
			seen[id] = struct{}{}
		}
		if !server.Enabled {
			continue
		}
		switch server.Transport {
		case "streamable_http":
			endpoint := strings.TrimSpace(server.Endpoint)
			if endpoint == "" {
				problems = append(problems, prefix+".endpoint is required")
			} else if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
				problems = append(problems, prefix+".endpoint must use http or https")
			}
		case "stdio":
			if strings.TrimSpace(server.Command) == "" {
				problems = append(problems, prefix+".command is required")
			}
		default:
			problems = append(problems, prefix+".transport must be streamable_http or stdio")
		}
		if server.Timeout <= 0 {
			problems = append(problems, prefix+".timeout must be positive")
		}
		if server.MaxResponseBytes <= 0 {
			problems = append(problems, prefix+".max_response_bytes must be positive")
		}
		if len(server.AllowedTools) == 0 {
			problems = append(problems, prefix+".allowed_tools must not be empty")
		}
		allowed := map[string]struct{}{}
		for j, name := range server.AllowedTools {
			name = strings.TrimSpace(name)
			if name == "" {
				problems = append(problems, fmt.Sprintf("%s.allowed_tools[%d] is required", prefix, j))
			} else if _, ok := allowed[name]; ok {
				problems = append(problems, fmt.Sprintf("%s.allowed_tools[%d] is duplicated", prefix, j))
			} else {
				allowed[name] = struct{}{}
			}
		}
	}
	s := c.Skills
	if s.MaxFileBytes == 0 && s.MaxTotalBytes == 0 && s.MaxFiles == 0 {
		return nil
	}
	if s.MaxFileBytes <= 0 {
		problems = append(problems, "extensions.skills.max_file_bytes must be positive")
	}
	if s.MaxTotalBytes <= 0 {
		problems = append(problems, "extensions.skills.max_total_bytes must be positive")
	}
	if s.MaxFiles <= 0 {
		problems = append(problems, "extensions.skills.max_files must be positive")
	}
	if s.MaxFileBytes > s.MaxTotalBytes {
		problems = append(problems, "extensions.skills.max_file_bytes must not exceed max_total_bytes")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (c ReflectionConfig) Validate(platforms PlatformConfig) error {
	if !c.Enabled && c.Mode == "" {
		return nil
	}
	var problems []string
	if c.Mode != "off" && c.Mode != "auto" {
		problems = append(problems, "reflection.mode must be off or auto")
	}
	if c.Enabled && c.Mode == "off" {
		problems = append(problems, "reflection.mode must be auto when reflection is enabled")
	}
	if c.Enabled && strings.TrimSpace(c.ExtraDirectory) == "" {
		problems = append(problems, "reflection.extra_directory is required when reflection is enabled")
	}
	knownConversations := make(map[string]struct{}, len(platforms.Groups))
	for _, group := range platforms.Groups {
		knownConversations[strings.TrimSpace(group.ID)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(c.ConversationIDs))
	for index, conversationID := range c.ConversationIDs {
		conversationID = strings.TrimSpace(conversationID)
		if conversationID == "" {
			problems = append(problems, fmt.Sprintf("reflection.conversation_ids[%d] is required", index))
		} else if _, ok := knownConversations[conversationID]; !ok {
			problems = append(problems, fmt.Sprintf("reflection.conversation_ids[%d] must reference a bound conversation", index))
		}
		if _, ok := seen[conversationID]; ok {
			problems = append(problems, fmt.Sprintf("reflection.conversation_ids[%d] is duplicated", index))
		}
		seen[conversationID] = struct{}{}
	}
	if c.Enabled && len(c.ConversationIDs) == 0 {
		problems = append(problems, "reflection.conversation_ids must not be empty when reflection is enabled")
	}
	positiveDurations := []struct {
		name  string
		value time.Duration
	}{
		{"scan_interval", c.ScanInterval}, {"message_ttl", c.MessageTTL},
		{"quiet_window", c.QuietWindow}, {"max_session_duration", c.MaxSessionDuration},
	}
	for _, item := range positiveDurations {
		if item.value <= 0 {
			problems = append(problems, "reflection."+item.name+" must be positive")
		}
	}
	positiveInts := []struct {
		name  string
		value int
	}{
		{"max_messages_per_conversation", c.MaxMessagesPerConversation}, {"max_messages_total", c.MaxMessagesTotal},
		{"max_message_chars", c.MaxMessageChars}, {"max_pairs_per_scan", c.MaxPairsPerScan},
	}
	for _, item := range positiveInts {
		if item.value <= 0 {
			problems = append(problems, "reflection."+item.name+" must be positive")
		}
	}
	if c.MinConfidence <= 0 || c.MinConfidence > 1 {
		problems = append(problems, "reflection.min_confidence must be greater than 0 and at most 1")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (c AdministrationConfig) Validate(platforms PlatformConfig) error {
	conversations := make(map[string]struct{}, len(platforms.Groups))
	for _, group := range platforms.Groups {
		conversations[strings.TrimSpace(group.ID)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(c.Administrators))
	var problems []string
	for index, administrator := range c.Administrators {
		conversationID := strings.TrimSpace(administrator.ConversationID)
		userID := strings.TrimSpace(administrator.UserID)
		prefix := fmt.Sprintf("administration.administrators[%d]", index)
		if conversationID == "" {
			problems = append(problems, prefix+".conversation_id is required")
		} else if _, exists := conversations[conversationID]; !exists {
			problems = append(problems, prefix+".conversation_id must reference a bound conversation")
		}
		if userID == "" {
			problems = append(problems, prefix+".user_id is required")
		}
		key := conversationID + "\x00" + userID
		if _, exists := seen[key]; exists {
			problems = append(problems, prefix+" is duplicated")
		} else {
			seen[key] = struct{}{}
		}
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
				problems = append(problems, commandPrefix+" must be one of status, restart, pause, resume, proactive, reload_docs")
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
	case "status", "restart", "pause", "resume", "proactive", "reload_docs":
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

func (c AdmissionConfig) Validate() error {
	var problems []string
	if c.DedupTTL <= 0 {
		problems = append(problems, "admission.dedup_ttl must be positive")
	}
	if c.DedupMax <= 0 {
		problems = append(problems, "admission.dedup_max must be positive")
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
			"admission":      c.Runtime.Admission,
			"platforms":      c.Runtime.Platforms.Public(),
			"administration": c.Runtime.Administration,
			"reflection":     c.Runtime.Reflection,
			"extensions": map[string]any{
				"mcp": func() []map[string]any {
					result := make([]map[string]any, 0, len(c.Runtime.Extensions.MCP))
					for _, server := range c.Runtime.Extensions.MCP {
						result = append(result, map[string]any{"id": server.ID, "enabled": server.Enabled, "transport": server.Transport, "endpoint": server.Endpoint, "command": server.Command, "args": server.Args, "working_directory": server.WorkingDirectory, "allowed_tools": server.AllowedTools, "timeout": server.Timeout.String(), "max_response_bytes": server.MaxResponseBytes, "api_key_configured": server.APIKeyRef != ""})
					}
					return result
				}(),
				"skills": c.Runtime.Extensions.Skills,
			},
			"operations": c.Runtime.Operations,
		},
	}
}

func (c PlatformConfig) Public() map[string]any {
	connectors := make([]any, 0, len(c.Connectors))
	for _, connector := range c.Connectors {
		item := map[string]any{"id": connector.ID, "type": connector.Type, "enabled": connector.Enabled,
			"prompt_suffix": connector.PromptSuffix}
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
