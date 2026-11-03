package agent

import (
	"encoding/json"
	"fmt"
	"time"
)

// Limits 是一次回答的硬预算，不是模型建议。零值采用缺省值，负值拒绝。
// 结果字节上限涵盖序列化信封；累计上限涵盖新增 assistant/tool 上下文。
type Limits struct {
	Timeout         time.Duration `json:"-"`
	MaxTurns        int           `json:"max_turns"`
	MaxToolCalls    int           `json:"max_tool_calls"`
	MaxToolsPerTurn int           `json:"max_tools_per_turn"`
	MaxResultBytes  int           `json:"max_result_bytes"`
	MaxContextBytes int           `json:"max_context_bytes"`
}

func (l Limits) Defaults() Limits {
	if l.Timeout == 0 {
		l.Timeout = 90 * time.Second
	}
	if l.MaxTurns == 0 {
		l.MaxTurns = 6
	}
	if l.MaxToolCalls == 0 {
		l.MaxToolCalls = 12
	}
	if l.MaxToolsPerTurn == 0 {
		l.MaxToolsPerTurn = 4
	}
	if l.MaxResultBytes == 0 {
		l.MaxResultBytes = 16 << 10
	}
	if l.MaxContextBytes == 0 {
		l.MaxContextBytes = 128 << 10
	}
	return l
}

func (l Limits) Validate() error {
	l = l.Defaults()
	if l.Timeout <= 0 || l.Timeout > 10*time.Minute || l.MaxTurns < 1 || l.MaxTurns > 32 || l.MaxToolCalls < 1 || l.MaxToolCalls > 128 || l.MaxToolsPerTurn < 1 || l.MaxToolsPerTurn > 32 || l.MaxResultBytes < 512 || l.MaxResultBytes > 1<<20 || l.MaxContextBytes < l.MaxResultBytes || l.MaxContextBytes > 8<<20 {
		return fmt.Errorf("invalid agent limits: timeout<=10m, turns<=32, calls<=128, tools/turn<=32, result=512..1048576 bytes, result<=context<=8388608 bytes required")
	}
	return nil
}

func (l Limits) MarshalJSON() ([]byte, error) {
	type alias Limits
	l = l.Defaults()
	return json.Marshal(struct {
		alias
		Timeout string `json:"timeout"`
	}{alias(l), l.Timeout.String()})
}

func (l *Limits) UnmarshalJSON(data []byte) error {
	type alias Limits
	raw := struct {
		alias
		Timeout string `json:"timeout"`
	}{alias: alias(l.Defaults())}
	// 严格校验由此类型自己负责，避免自定义反序列化绕过外层限制。
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("agent limits must be an object")
	}
	for name, value := range fields {
		switch name {
		case "timeout", "max_turns", "max_tool_calls", "max_tools_per_turn", "max_result_bytes", "max_context_bytes":
		default:
			return fmt.Errorf("unknown agent limit %q", name)
		}
		if string(value) == "null" {
			return fmt.Errorf("agent limit %s cannot be null", name)
		}
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Timeout != "" {
		timeout, err := time.ParseDuration(raw.Timeout)
		if err != nil {
			return err
		}
		raw.alias.Timeout = timeout
	}
	*l = Limits(raw.alias)
	return l.Validate()
}
