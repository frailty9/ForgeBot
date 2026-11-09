// Package config loads, normalizes, validates, and redacts ForgeBot startup
// and runtime configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/agent"
	"github.com/soleapi/forgebot/internal/conversation"
	"github.com/soleapi/forgebot/internal/history"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/platform"
	"gopkg.in/yaml.v3"
)

const (
	envConfigPath      = "FORGEBOT_CONFIG"
	envHTTPAddr        = "FORGEBOT_HTTP_ADDR"
	envRuntimeConfig   = "FORGEBOT_RUNTIME_CONFIG"
	envSecretStorePath = "FORGEBOT_SECRET_STORE"
	envAdminToken      = "FORGEBOT_ADMIN_TOKEN"
	envSecretKey       = "FORGEBOT_SECRET_KEY"
	envLogLevel        = "FORGEBOT_LOG_LEVEL"
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
	Storage           StorageConfig `json:"storage" yaml:"storage"`
	Server            ServerConfig  `json:"server" yaml:"server"`
	RuntimeConfigPath string        `json:"runtime_config_path" yaml:"runtime_config_path"`
	SecretStorePath   string        `json:"secret_store_path" yaml:"secret_store_path"`
	AdminToken        string        `json:"-" yaml:"admin_token"`
	// SecretKey 是 SecretStore 主密钥（Base64 编码的 32 字节）。与 admin_token
	// 同属进程级敏感项：可写在 YAML，环境变量优先；任何配置查询都不输出。
	SecretKey string `json:"-" yaml:"secret_key"`
	// Log 属于进程级运维配置：输出位置在启动时确定，修改需重启。
	Log LogConfig `json:"log" yaml:"log"`
}

// StorageConfig 是进程级单选项，不允许运行时混用多个权威后端。
// 各领域可以有独立文件，但它们必须由同一个被选中的 Backend 构建。
type StorageConfig struct {
	Backend string `json:"backend" yaml:"backend"`
}

const FileBackend = "file"

func (c StorageConfig) Kind() string {
	if c.Backend == "" {
		return FileBackend
	}
	return c.Backend
}

// RuntimeConfig contains hot-reloadable application behavior.
// 字段顺序即 init 生成和管理接口保存的 JSON 顺序，按配置流程排列：
// 身份与模型 → 接入的群 → 功能 → 各项上限。
type RuntimeConfig struct {
	Bot       BotConfig      `json:"bot"`
	LLM       LLMConfig      `json:"llm"`
	Decision  DecisionConfig `json:"decision"`
	Platforms PlatformConfig `json:"platforms"`
	// Operator 是唯一的运维群；不配置则不开放运维命令与故障通知。
	Operator           *OperatorConfig           `json:"operator,omitempty"`
	Modules            map[string]ModuleConfig   `json:"modules,omitempty"`
	Extensions         ExtensionsConfig          `json:"extensions"`
	ConversationMemory conversation.MemoryPolicy `json:"conversation_memory"`
	// MessageHistory 是核心近期消息容器的上限；容器始终启用，没有开关。
	MessageHistory history.Policy  `json:"message_history"`
	Agent          agent.Limits    `json:"agent"`
	Admission      AdmissionConfig `json:"admission"`
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
	SchemaVersion int               `json:"schema_version"`
	ID            string            `json:"id"`
	Version       uint64            `json:"version"`
	UpdatedAt     time.Time         `json:"updated_at"`
	Source        string            `json:"source,omitempty"`
	Runtime       RuntimeConfig     `json:"runtime"`
	History       []RuntimeRevision `json:"history,omitempty"`
}

const RuntimeSchemaVersion = 1
const RuntimeDocumentID = "runtime/default"

var ErrRuntimeSchemaUnsupported = errors.New("unsupported runtime document schema or identity")

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

// LogConfig 把控制台（stdout）与文件分成两路输出，各自设置开关、等级和格式；
// 至少开启一路。两路都开启时，一条日志按各自等级分别写入。
type LogConfig struct {
	Console LogConsoleConfig `json:"console" yaml:"console"`
	File    LogFileConfig    `json:"file" yaml:"file"`
}

type LogConsoleConfig struct {
	Enabled bool   `json:"enabled" yaml:"enabled"`
	Level   string `json:"level" yaml:"level"`
	Format  string `json:"format" yaml:"format"`
}

// LogFileConfig 关闭时仍保留路径与轮转设置，便于临时停用后再开启。
type LogFileConfig struct {
	Enabled bool   `json:"enabled" yaml:"enabled"`
	Level   string `json:"level" yaml:"level"`
	Format  string `json:"format" yaml:"format"`
	Path    string `json:"path" yaml:"path"`
	// MaxSizeMB 是单个日志文件上限；写满即轮转。
	MaxSizeMB int `json:"max_size_mb" yaml:"max_size_mb"`
	// RotateAfterDays 是单个文件最长写入天数，0 表示只按大小轮转。
	RotateAfterDays int `json:"rotate_after_days" yaml:"rotate_after_days"`
	// MaxAgeDays 按备份的轮转时间清理，0 表示永久保留。
	MaxAgeDays int `json:"max_age_days" yaml:"max_age_days"`
	// Compress 决定轮转出的备份是否压缩为 .gz，当前文件始终不压缩。
	Compress bool `json:"compress" yaml:"compress"`
}

// raw* 用指针区分“未填写”和“填了零值”，未填写的字段保留默认值。
// 旧的平铺写法（log.level 等）不在其中，严格解析会直接报未知字段。
type rawLogConfig struct {
	Console *rawLogConsoleConfig `yaml:"console"`
	File    *rawLogFileConfig    `yaml:"file"`
}

type rawLogConsoleConfig struct {
	Enabled *bool  `yaml:"enabled"`
	Level   string `yaml:"level"`
	Format  string `yaml:"format"`
}

type rawLogFileConfig struct {
	Enabled         *bool  `yaml:"enabled"`
	Level           string `yaml:"level"`
	Format          string `yaml:"format"`
	Path            string `yaml:"path"`
	MaxSizeMB       *int   `yaml:"max_size_mb"`
	RotateAfterDays *int   `yaml:"rotate_after_days"`
	MaxAgeDays      *int   `yaml:"max_age_days"`
	Compress        *bool  `yaml:"compress"`
}

type BotConfig struct {
	Name         string `json:"name" yaml:"name"`
	SystemPrompt string `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
}

// LLM 认证方式。匿名本地服务必须显式声明 none，缺少 Key 不能被当作匿名。
const (
	AuthenticationAPIKey = "api_key"
	AuthenticationNone   = "none"
)

// LLMConfig selects one supported protocol family and model settings.
type LLMConfig struct {
	Authentication     string        `json:"authentication,omitempty" yaml:"authentication,omitempty"`
	Protocol           llm.Protocol  `json:"protocol" yaml:"protocol"`
	BaseURL            string        `json:"base_url" yaml:"base_url"`
	APIKeyRef          string        `json:"api_key_ref" yaml:"api_key_ref"`
	Model              string        `json:"model" yaml:"model"`
	AnthropicVersion   string        `json:"anthropic_version,omitempty" yaml:"anthropic_version,omitempty"`
	Timeout            time.Duration `json:"timeout" yaml:"timeout"`
	ImageUnderstanding bool          `json:"image_understanding" yaml:"image_understanding"`
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
	ID           string          `json:"id" yaml:"id"`
	Type         string          `json:"type" yaml:"type"`
	Enabled      bool            `json:"enabled" yaml:"enabled"`
	PromptSuffix string          `json:"prompt_suffix,omitempty" yaml:"prompt_suffix,omitempty"`
	Options      json.RawMessage `json:"options,omitempty" yaml:"options,omitempty"`
}

// GroupBindingConfig maps an external target to a logical conversation.
type GroupBindingConfig struct {
	ID           string `json:"id" yaml:"id"`
	ConnectorID  string `json:"connector_id" yaml:"connector_id"`
	Target       string `json:"target" yaml:"target"`
	PromptSuffix string `json:"prompt_suffix,omitempty" yaml:"prompt_suffix,omitempty"`
}

// OperatorConfig 指定唯一的运维群。群内所有成员都可执行允许的运维命令，
// 因此该群应只包含可信的管理人员；多人管理请通过这个群进行。
type OperatorConfig struct {
	ConversationID string   `json:"conversation_id" yaml:"conversation_id"`
	Notifications  bool     `json:"notifications" yaml:"notifications"`
	Commands       []string `json:"commands" yaml:"commands"`
}

func DefaultStatic() StaticConfig {
	return StaticConfig{Storage: StorageConfig{Backend: FileBackend}, Server: ServerConfig{
		Addr: "127.0.0.1:8080", ReadTimeout: 10 * time.Second,
		WriteTimeout: 100 * time.Second, ShutdownTimeout: 15 * time.Second,
	}, Log: LogConfig{
		Console: LogConsoleConfig{Enabled: true, Level: "info", Format: "text"},
		File:    LogFileConfig{Level: "info", Format: "json", MaxSizeMB: 10, RotateAfterDays: 7, MaxAgeDays: 60},
	}}
}

func DefaultRuntime() RuntimeConfig {
	return RuntimeConfig{
		Bot:       BotConfig{Name: "ForgeBot"},
		LLM:       LLMConfig{Protocol: llm.OpenAIResponses, APIKeyRef: defaultAPIKeyRef, Timeout: 60 * time.Second},
		Admission: AdmissionConfig{DedupTTL: 60 * time.Second, DedupMax: 10000},
		Platforms: PlatformConfig{
			Connectors: []ConnectorConfig{{ID: "echo", Type: "echo", Enabled: true}},
			Groups:     []GroupBindingConfig{{ID: "local", ConnectorID: "echo", Target: "local"}},
		},
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
	if static.Log.File.Path != "" && !filepath.IsAbs(static.Log.File.Path) && staticPath != "" {
		static.Log.File.Path = filepath.Join(filepath.Dir(staticPath), static.Log.File.Path)
	}
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
	if errors.Is(err, ErrRuntimeSchemaUnsupported) {
		return RuntimeDocument{}, false, err
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
		return RuntimeDocument{SchemaVersion: RuntimeSchemaVersion, ID: RuntimeDocumentID, Version: 1, UpdatedAt: now().UTC(), Source: "legacy", Runtime: runtime}, nil
	}
	// 先检查信封版本，再严格解析字段；未来版本可能引入未知字段，不能因
	// DisallowUnknownFields 提前失败而误把正常的新版本当损坏文件回退。
	var identity struct {
		SchemaVersion int    `json:"schema_version"`
		ID            string `json:"id"`
	}
	if err := json.Unmarshal(data, &identity); err != nil {
		return RuntimeDocument{}, err
	}
	if (identity.SchemaVersion != 0 && identity.SchemaVersion != RuntimeSchemaVersion) || (identity.ID != "" && identity.ID != RuntimeDocumentID) {
		return RuntimeDocument{}, ErrRuntimeSchemaUnsupported
	}
	var document RuntimeDocument
	if err := decodeStrictJSON(data, &document); err != nil {
		return RuntimeDocument{}, err
	}
	// 仅在读取时补齐旧元数据；首次更新保留原文件备份，不启动即改写部署数据。
	if document.SchemaVersion == 0 {
		document.SchemaVersion = RuntimeSchemaVersion
	}
	if document.ID == "" {
		document.ID = RuntimeDocumentID
	}
	if document.SchemaVersion != RuntimeSchemaVersion || document.ID != RuntimeDocumentID {
		return RuntimeDocument{}, ErrRuntimeSchemaUnsupported
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
		Storage           *StorageConfig   `yaml:"storage"`
		Server            *rawServerConfig `yaml:"server"`
		RuntimeConfigPath string           `yaml:"runtime_config_path"`
		SecretStorePath   string           `yaml:"secret_store_path"`
		AdminToken        string           `yaml:"admin_token"`
		SecretKey         string           `yaml:"secret_key"`
		Log               *rawLogConfig    `yaml:"log"`
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
	if raw.Storage != nil {
		out.Storage = *raw.Storage
	}
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
	out.SecretKey = strings.TrimSpace(raw.SecretKey)
	if raw.Log != nil {
		mergeLog(&out.Log, *raw.Log)
	}
	*target = out
	return nil
}

// mergeLog 只覆盖 YAML 中填写的字段；等级统一转为小写，DEBUG 与 debug 等价。
func mergeLog(out *LogConfig, raw rawLogConfig) {
	if c := raw.Console; c != nil {
		if c.Enabled != nil {
			out.Console.Enabled = *c.Enabled
		}
		if c.Level != "" {
			out.Console.Level = strings.ToLower(strings.TrimSpace(c.Level))
		}
		if c.Format != "" {
			out.Console.Format = strings.ToLower(strings.TrimSpace(c.Format))
		}
	}
	f := raw.File
	if f == nil {
		return
	}
	if f.Enabled != nil {
		out.File.Enabled = *f.Enabled
	}
	if f.Level != "" {
		out.File.Level = strings.ToLower(strings.TrimSpace(f.Level))
	}
	if f.Format != "" {
		out.File.Format = strings.ToLower(strings.TrimSpace(f.Format))
	}
	out.File.Path = strings.TrimSpace(f.Path)
	if f.MaxSizeMB != nil {
		out.File.MaxSizeMB = *f.MaxSizeMB
	}
	if f.RotateAfterDays != nil {
		out.File.RotateAfterDays = *f.RotateAfterDays
	}
	if f.MaxAgeDays != nil {
		out.File.MaxAgeDays = *f.MaxAgeDays
	}
	if f.Compress != nil {
		out.File.Compress = *f.Compress
	}
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
	if value := strings.TrimSpace(lookupEnv(envSecretKey)); value != "" {
		cfg.SecretKey = value
	}
	// 排障时临时调整等级，同时作用于两路输出，避免只改一边造成困惑。
	if value := strings.ToLower(strings.TrimSpace(lookupEnv(envLogLevel))); value != "" {
		cfg.Log.Console.Level = value
		cfg.Log.File.Level = value
	}
}

func (c StaticConfig) Validate() error {
	var problems []string
	if c.Storage.Kind() != FileBackend {
		problems = append(problems, "storage.backend must be file; SQL/NoSQL backends are not implemented")
	}
	if err := c.Log.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
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
	if err := c.Decision.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if err := c.ConversationMemory.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if err := c.MessageHistory.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if err := c.Agent.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(c.Bot.Name) == "" {
		problems = append(problems, "bot.name is required")
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
	if c.Operator != nil {
		if err := c.Operator.Validate(c.Platforms); err != nil {
			problems = append(problems, err.Error())
		}
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

func (c OperatorConfig) Validate(platforms PlatformConfig) error {
	conversationID := strings.TrimSpace(c.ConversationID)
	bound := false
	for _, binding := range platforms.Groups {
		if strings.TrimSpace(binding.ID) == conversationID {
			bound = true
			break
		}
	}
	var problems []string
	if conversationID == "" {
		problems = append(problems, "operator.conversation_id is required")
	} else if !bound {
		problems = append(problems, "operator.conversation_id must reference a bound conversation")
	}
	commands := make(map[string]struct{}, len(c.Commands))
	for index, command := range c.Commands {
		command = strings.ToLower(strings.TrimSpace(command))
		prefix := fmt.Sprintf("operator.commands[%d]", index)
		if !isOperatorCommand(command) {
			problems = append(problems, prefix+" must be one of status, restart, pause, resume, proactive, reload_docs")
		} else if _, exists := commands[command]; exists {
			problems = append(problems, prefix+" is duplicated")
		} else {
			commands[command] = struct{}{}
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
		if !platform.ValidConnectorType(connector.Type) {
			problems = append(problems, prefix+".type is invalid")
		}
		if value := bytes.TrimSpace(connector.Options); len(value) > 0 && (value[0] != '{' || !json.Valid(value)) {
			problems = append(problems, prefix+".options must be a JSON object")
		}
	}
	groups := make(map[string]struct{}, len(c.Groups))
	targets := make(map[string]struct{}, len(c.Groups))
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
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (c Snapshot) Validate() error { return errors.Join(c.Static.Validate(), c.Runtime.Validate()) }

func (c LLMConfig) Validate() error {
	if c.Authentication != "" && c.Authentication != AuthenticationAPIKey && c.Authentication != AuthenticationNone {
		return errors.New("llm.authentication must be api_key or none")
	}
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
	if !c.Console.Enabled && !c.File.Enabled {
		return errors.New("log.console and log.file cannot both be disabled")
	}
	// 关闭的一路也校验，避免重新开启时才发现拼写错误。
	for _, sink := range []struct{ name, level, format string }{
		{"log.console", c.Console.Level, c.Console.Format},
		{"log.file", c.File.Level, c.File.Format},
	} {
		switch sink.level {
		case "debug", "info", "warn", "error":
		default:
			return fmt.Errorf("%s.level must be one of debug, info, warn, error", sink.name)
		}
		switch sink.format {
		case "text", "json":
		default:
			return fmt.Errorf("%s.format must be text or json", sink.name)
		}
	}
	f := c.File
	if f.Enabled && f.Path == "" {
		return errors.New("log.file.path is required when log.file is enabled")
	}
	if f.MaxSizeMB < 1 || f.MaxSizeMB > 10240 {
		return errors.New("log.file.max_size_mb must be 1..10240")
	}
	if f.RotateAfterDays < 0 || f.RotateAfterDays > 3650 {
		return errors.New("log.file.rotate_after_days must be 0..3650")
	}
	if f.MaxAgeDays < 0 || f.MaxAgeDays > 36500 {
		return errors.New("log.file.max_age_days must be 0..36500")
	}
	// 保留期短于轮转周期时，刚轮转出的备份会立刻被清理，等于没有历史。
	if f.MaxAgeDays > 0 && f.RotateAfterDays > 0 && f.MaxAgeDays < f.RotateAfterDays {
		return errors.New("log.file.max_age_days must be 0 or at least log.file.rotate_after_days")
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
			"storage": map[string]any{"backend": c.Static.Storage.Kind()},
			"server": map[string]any{
				"addr":             c.Static.Server.Addr,
				"read_timeout":     c.Static.Server.ReadTimeout.String(),
				"write_timeout":    c.Static.Server.WriteTimeout.String(),
				"shutdown_timeout": c.Static.Server.ShutdownTimeout.String(),
			},
			"runtime_config_path": c.Static.RuntimeConfigPath,
			"log":                 c.Static.Log,
		},
		"runtime": map[string]any{
			"decision":            c.Runtime.Decision.Public(),
			"conversation_memory": c.Runtime.ConversationMemory.Defaults(),
			"message_history":     c.Runtime.MessageHistory.Defaults(),
			"agent":               c.Runtime.Agent.Defaults(),
			"bot":                 c.Runtime.Bot,
			"llm": map[string]any{
				"protocol": c.Runtime.LLM.Protocol, "base_url": c.Runtime.LLM.BaseURL,
				"model": c.Runtime.LLM.Model, "anthropic_version": c.Runtime.LLM.AnthropicVersion, "image_understanding": c.Runtime.LLM.ImageUnderstanding, "authentication": c.Runtime.LLM.Authentication,
				"timeout": c.Runtime.LLM.Timeout.String(), "api_key_configured": apiKeyConfigured,
			},
			"admission": c.Runtime.Admission,
			"platforms": c.Runtime.Platforms.Public(),
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
			"operator": c.Runtime.Operator,
		},
	}
}

func (c PlatformConfig) Public() map[string]any {
	connectors := make([]any, 0, len(c.Connectors))
	for _, connector := range c.Connectors {
		item := map[string]any{"id": connector.ID, "type": connector.Type, "enabled": connector.Enabled,
			"prompt_suffix": connector.PromptSuffix}
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
