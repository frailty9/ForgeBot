package history

import (
	"encoding/json"
	"fmt"
	"time"
)

// Policy 是近期消息容器的上限。容器始终启用，这里只调整大小，不提供开关。
type Policy struct {
	MaxMessages  int           `json:"max_messages"`
	TTL          time.Duration `json:"-"`
	MaxTextChars int           `json:"max_text_chars"`
}

func (p Policy) Defaults() Policy {
	if p.MaxMessages == 0 {
		p.MaxMessages = 50
	}
	if p.TTL == 0 {
		p.TTL = 30 * time.Minute
	}
	if p.MaxTextChars == 0 {
		p.MaxTextChars = 1000
	}
	return p
}

func (p Policy) Validate() error {
	p = p.Defaults()
	if p.MaxMessages < 1 || p.MaxMessages > 1000 {
		return fmt.Errorf("message_history.max_messages must be 1..1000")
	}
	if p.TTL <= 0 || p.TTL > 24*time.Hour {
		return fmt.Errorf("message_history.ttl must be positive and at most 24h")
	}
	if p.MaxTextChars < 1 || p.MaxTextChars > 8000 {
		return fmt.Errorf("message_history.max_text_chars must be 1..8000")
	}
	return nil
}

func (p Policy) MarshalJSON() ([]byte, error) {
	type alias Policy
	p = p.Defaults()
	return json.Marshal(struct {
		alias
		TTL string `json:"ttl"`
	}{alias(p), p.TTL.String()})
}

func (p *Policy) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("message_history must be an object")
	}
	for name, value := range fields {
		switch name {
		case "max_messages", "ttl", "max_text_chars":
		default:
			return fmt.Errorf("unknown message_history option %q", name)
		}
		if string(value) == "null" {
			return fmt.Errorf("message_history option %s cannot be null", name)
		}
	}
	type alias Policy
	raw := struct {
		alias
		TTL string `json:"ttl"`
	}{alias: alias(Policy{})}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.TTL != "" {
		ttl, err := time.ParseDuration(raw.TTL)
		if err != nil {
			return fmt.Errorf("message_history.ttl: %w", err)
		}
		if ttl <= 0 {
			return fmt.Errorf("message_history.ttl must be positive and at most 24h")
		}
		raw.alias.TTL = ttl
	}
	*p = Policy(raw.alias).Defaults()
	return p.Validate()
}
