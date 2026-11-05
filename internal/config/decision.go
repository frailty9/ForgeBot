package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// DecisionConfig 是共享决策服务配置；未启用 DM 时使用 LLM，每次最多一个模型请求。
type DecisionConfig struct {
	DM            *DecisionModelConfig `json:"dm,omitempty"`
	DMTimeout     string               `json:"dm_timeout,omitempty"`
	LLMTimeout    string               `json:"llm_timeout,omitempty"`
	MaxConcurrent int                  `json:"max_concurrent,omitempty"`
}
type DecisionModelConfig struct {
	// Disabled 允许保留供应商参数而不加载 DM；关闭后直接使用 LLM。
	Disabled      bool     `json:"disabled"`
	Provider      string   `json:"provider"`
	Endpoint      string   `json:"endpoint"`
	Model         string   `json:"model"`
	APIKeyRef     string   `json:"api_key_ref"`
	MinConfidence *float64 `json:"min_confidence,omitempty"`
}

func (c DecisionConfig) DMEnabled() bool { return c.DM != nil && !c.DM.Disabled }

func (c DecisionConfig) Timeouts() (time.Duration, time.Duration, error) {
	dm, llm := 3*time.Second, 8*time.Second
	var err error
	if c.DMTimeout != "" {
		dm, err = time.ParseDuration(c.DMTimeout)
		if err != nil {
			return 0, 0, err
		}
	}
	if c.LLMTimeout != "" {
		llm, err = time.ParseDuration(c.LLMTimeout)
		if err != nil {
			return 0, 0, err
		}
	}
	if dm <= 0 || dm > time.Minute*2 || llm <= 0 || llm > time.Minute*2 {
		return 0, 0, fmt.Errorf("decision timeout must be positive and at most 2m")
	}
	return dm, llm, nil
}
func (c DecisionConfig) Validate() error {
	if _, _, err := c.Timeouts(); err != nil {
		return err
	}
	if c.MaxConcurrent < 0 || c.MaxConcurrent > 256 {
		return fmt.Errorf("decision.max_concurrent must be 0..256")
	}
	if c.DM == nil {
		return nil
	}
	dm := c.DM
	if dm.Provider != "jev" {
		return fmt.Errorf("decision.dm.provider must be jev")
	}
	if strings.TrimSpace(dm.APIKeyRef) == "" {
		return fmt.Errorf("decision.dm.api_key_ref is required")
	}
	if dm.Endpoint != "" {
		u, err := url.Parse(dm.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("decision.dm.endpoint must be an HTTP API root without credentials, query or fragment")
		}
	}
	if dm.MinConfidence != nil && (*dm.MinConfidence < 0 || *dm.MinConfidence > 1) {
		return fmt.Errorf("decision.dm.min_confidence must be 0..1")
	}
	return nil
}

func (c DecisionConfig) Public() map[string]any {
	dmTimeout, llmTimeout, _ := c.Timeouts()
	result := map[string]any{"dm_timeout": dmTimeout.String(), "llm_timeout": llmTimeout.String(), "max_concurrent": c.MaxConcurrent}
	if c.DM != nil {
		result["dm"] = map[string]any{"disabled": c.DM.Disabled, "provider": c.DM.Provider, "endpoint": c.DM.Endpoint, "model": c.DM.Model, "min_confidence": c.DM.MinConfidence}
	}
	return result
}
