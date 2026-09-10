package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/llm"
)

const (
	envConfigPath = "FORGEBOT_CONFIG"
	envHTTPAddr   = "FORGEBOT_HTTP_ADDR"
	envLogLevel   = "FORGEBOT_LOG_LEVEL"
)

type LoadOptions struct {
	Path      string
	LookupEnv func(string) string
}

type LoadReport struct {
	Source string
	Path   string
}

type Config struct {
	Server    ServerConfig   `json:"server"`
	Log       LogConfig      `json:"log"`
	Bot       BotConfig      `json:"bot"`
	Gate      GateConfig     `json:"gate"`
	LLM       LLMConfig      `json:"llm"`
	Platforms PlatformConfig `json:"platforms"`
}

type ServerConfig struct {
	Addr            string        `json:"addr"`
	ReadTimeout     time.Duration `json:"read_timeout"`
	WriteTimeout    time.Duration `json:"write_timeout"`
	ShutdownTimeout time.Duration `json:"shutdown_timeout"`
}

type LogConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

type BotConfig struct {
	Name string `json:"name"`
}

type LLMConfig struct {
	Protocol         llm.Protocol  `json:"protocol"`
	BaseURL          string        `json:"base_url"`
	APIKey           string        `json:"api_key"`
	Model            string        `json:"model"`
	AnthropicVersion string        `json:"anthropic_version,omitempty"`
	Timeout          time.Duration `json:"timeout"`
}

func (c LLMConfig) ClientConfig() llm.ClientConfig {
	return llm.ClientConfig{
		Protocol:         c.Protocol,
		BaseURL:          c.BaseURL,
		APIKey:           c.APIKey,
		Model:            c.Model,
		AnthropicVersion: c.AnthropicVersion,
		Timeout:          c.Timeout,
	}
}

type GateConfig struct {
	Enabled        bool          `json:"enabled"`
	RequireMention bool          `json:"require_mention"`
	Silence        time.Duration `json:"silence"`
	MaxCount       int           `json:"max_count"`
	MaxWindow      time.Duration `json:"max_window"`
	DedupTTL       time.Duration `json:"dedup_ttl"`
	DedupMax       int           `json:"dedup_max"`
	MaxBuffered    int           `json:"max_buffered"`
	MaxTextLen     int           `json:"max_text_len"`
	MaxConcurrent  int           `json:"max_concurrent"`
}

type PlatformConfig struct {
	Echo EchoPlatformConfig `json:"echo"`
}

type EchoPlatformConfig struct {
	Enabled bool `json:"enabled"`
}

func Default() Config {
	return Config{
		Server: ServerConfig{
			Addr:            "127.0.0.1:8080",
			ReadTimeout:     10 * time.Second,
			WriteTimeout:    30 * time.Second,
			ShutdownTimeout: 15 * time.Second,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		Bot: BotConfig{
			Name: "ForgeBot",
		},
		LLM: LLMConfig{
			Protocol: llm.OpenAIResponses,
			Timeout:  60 * time.Second,
		},
		Gate: GateConfig{
			Enabled:        false,
			RequireMention: true,
			Silence:        4 * time.Second,
			MaxCount:       8,
			MaxWindow:      25 * time.Second,
			DedupTTL:       60 * time.Second,
			DedupMax:       10000,
			MaxBuffered:    50,
			MaxTextLen:     500,
			MaxConcurrent:  4,
		},
		Platforms: PlatformConfig{
			Echo: EchoPlatformConfig{Enabled: true},
		},
	}
}

func Load(opts LoadOptions) (Config, LoadReport, error) {
	lookupEnv := opts.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}

	cfg := Default()
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		path = strings.TrimSpace(lookupEnv(envConfigPath))
	}

	report := LoadReport{Source: "defaults"}
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return Config{}, report, err
		}
		defer file.Close()

		decoder := json.NewDecoder(file)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, report, err
		}
		report.Source = "file"
		report.Path = path
	}

	applyEnv(&cfg, lookupEnv)
	if err := cfg.Validate(); err != nil {
		return Config{}, report, err
	}
	return cfg, report, nil
}

func applyEnv(cfg *Config, lookupEnv func(string) string) {
	if value := strings.TrimSpace(lookupEnv(envHTTPAddr)); value != "" {
		cfg.Server.Addr = value
	}
	if value := strings.TrimSpace(lookupEnv(envLogLevel)); value != "" {
		cfg.Log.Level = value
	}
}

func (c Config) Validate() error {
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
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (c LLMConfig) Validate() error {
	if !c.Protocol.Valid() {
		return fmt.Errorf("llm.protocol must be one of %s, %s, %s",
			llm.AnthropicMessages, llm.OpenAIResponses, llm.OpenAICompletions)
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("llm.timeout must be positive")
	}
	return nil
}

func (c LogConfig) Validate() error {
	switch strings.ToLower(c.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log.level must be one of debug, info, warn, error")
	}
	switch strings.ToLower(c.Format) {
	case "text", "json":
	default:
		return fmt.Errorf("log.format must be text or json")
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

func (c Config) Public() map[string]any {
	return map[string]any{
		"server": map[string]any{
			"addr":             c.Server.Addr,
			"read_timeout":     c.Server.ReadTimeout.String(),
			"write_timeout":    c.Server.WriteTimeout.String(),
			"shutdown_timeout": c.Server.ShutdownTimeout.String(),
		},
		"log": map[string]any{
			"level":  c.Log.Level,
			"format": c.Log.Format,
		},
		"bot": map[string]any{
			"name": c.Bot.Name,
		},
		"llm": map[string]any{
			"protocol":          c.LLM.Protocol,
			"base_url":          c.LLM.BaseURL,
			"model":             c.LLM.Model,
			"anthropic_version": c.LLM.AnthropicVersion,
			"timeout":           c.LLM.Timeout.String(),
		},
		"gate": map[string]any{
			"enabled":         c.Gate.Enabled,
			"require_mention": c.Gate.RequireMention,
			"silence":         c.Gate.Silence.String(),
			"max_count":       c.Gate.MaxCount,
			"max_window":      c.Gate.MaxWindow.String(),
			"dedup_ttl":       c.Gate.DedupTTL.String(),
			"dedup_max":       c.Gate.DedupMax,
			"max_buffered":    c.Gate.MaxBuffered,
			"max_text_len":    c.Gate.MaxTextLen,
			"max_concurrent":  c.Gate.MaxConcurrent,
		},
		"platforms": c.Platforms,
	}
}

func BoolEnv(value string) (bool, bool) {
	if strings.TrimSpace(value) == "" {
		return false, false
	}
	parsed, err := strconv.ParseBool(value)
	return parsed, err == nil
}
