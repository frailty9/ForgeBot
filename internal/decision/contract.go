// Package decision 提供有界、协议无关的语义判断。结果只供宿主策略消费，
// 不携带执行权限，不能直接发送消息或修改群状态。
package decision

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/llm"
)

var ServiceKey = capability.NewKey[Decider]("decision.service.v1")

const Unknown = "unknown"

// Choice 是首版可移植的有限枚举，不是任意供应商 Schema 的透传接口。
// Schema 从已注册的题型生成；LLM 和专用 DM 必须符合相同内部契约。
type Choice struct {
	Instructions string            `json:"instructions"`
	Options      map[string]string `json:"options"`
}
type Task struct {
	ID        string            `json:"id"`
	Version   int               `json:"version"`
	Questions map[string]Choice `json:"questions"`
}
type Request struct {
	TaskID  string            `json:"task_id"`
	Version int               `json:"version"`
	State   json.RawMessage   `json:"state"`
	Media   []llm.ContentPart `json:"media,omitempty"`
}
type ProviderRequest struct {
	Task  Task
	State json.RawMessage
	Media []llm.ContentPart
}
type Answer map[string]string

func (t Task) Validate() error {
	if strings.TrimSpace(t.ID) == "" || len(t.ID) > 128 || t.Version < 1 || len(t.Questions) < 1 || len(t.Questions) > 8 {
		return fmt.Errorf("invalid decision task")
	}
	for name, q := range t.Questions {
		if strings.TrimSpace(name) == "" || len(name) > 64 || len(q.Instructions) == 0 || len(q.Instructions) > 8192 || len(q.Options) < 2 || len(q.Options) > 32 {
			return fmt.Errorf("invalid decision question %q", name)
		}
		if _, ok := q.Options[Unknown]; !ok {
			return fmt.Errorf("decision question %q requires unknown option", name)
		}
		for option, description := range q.Options {
			if option == "" || len(option) > 64 || len(description) > 2048 {
				return fmt.Errorf("invalid choice option")
			}
		}
	}
	data, _ := json.Marshal(t)
	if len(data) > 32768 {
		return fmt.Errorf("decision task exceeds 32768 bytes")
	}
	return nil
}

// Schema 返回受限题型对应的 JSON Schema。结构正确仍不代表语义正确或已获授权。
func (t Task) Schema() map[string]any {
	properties := map[string]any{}
	required := make([]string, 0, len(t.Questions))
	for name, q := range t.Questions {
		values := make([]string, 0, len(q.Options))
		for option := range q.Options {
			values = append(values, option)
		}
		properties[name] = map[string]any{"type": "string", "enum": values}
		required = append(required, name)
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func (t Task) ValidateAnswer(answer Answer) error {
	if len(answer) != len(t.Questions) {
		return fmt.Errorf("decision answer fields do not match task")
	}
	for name, q := range t.Questions {
		value, ok := answer[name]
		if !ok {
			return fmt.Errorf("missing decision field %q", name)
		}
		if _, ok = q.Options[value]; !ok {
			return fmt.Errorf("invalid decision option for %q", name)
		}
	}
	return nil
}
func (t Task) Unknown() Answer {
	a := Answer{}
	for name := range t.Questions {
		a[name] = Unknown
	}
	return a
}
func cloneAnswer(a Answer) Answer {
	result := Answer{}
	for k, v := range a {
		result[k] = v
	}
	return result
}
