package main

import (
	"bytes"
	"encoding/json"

	"github.com/soleapi/forgebot/internal/config"
)

// initialRuntime 仅替换示例的 LLM 对象，其他字段和顶层顺序直接沿用唯一示例。
// 合并原始键，保留 omitempty 会省略的空字段，使 init 也完整列出可配置项。
func initialRuntime(example []byte, llm config.LLMConfig) ([]byte, error) {
	var root struct {
		LLM json.RawMessage `json:"llm"`
	}
	if err := json.Unmarshal(example, &root); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(root.LLM, &fields); err != nil {
		return nil, err
	}
	for key, value := range map[string]any{
		"authentication": llm.Authentication, "protocol": llm.Protocol,
		"model": llm.Model, "base_url": llm.BaseURL, "image_understanding": llm.ImageUnderstanding,
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		fields[key] = encoded
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	updated := bytes.Replace(example, root.LLM, encoded, 1)
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, updated, "", "  "); err != nil {
		return nil, err
	}
	return formatted.Bytes(), nil
}
