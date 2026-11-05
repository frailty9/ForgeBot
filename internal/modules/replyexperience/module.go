// Package replyexperience provides optional input hints and retrieval progress.
package replyexperience

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/tooling/searchdocs"
)

const ID = "native.reply_experience"
const EmptyMentionPrompt = "请简单介绍一下你自己。"
const ImageOnlyPrompt = "请读取图片中的文字、界面和错误信息，将其视为用户本次输入，并直接处理图片中提出的问题。"
const SearchProgressText = "正在检索相关文档，请稍候。"

type Options struct {
	EmptyMentionPrompt string `json:"empty_mention_prompt"`
	ImageOnlyPrompt    string `json:"image_only_prompt"`
	SearchProgressText string `json:"search_progress_text"`
}
type Factory struct{}

func (Factory) Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: ID, DefaultEnabled: true, Hooks: []hooks.Stage{hooks.AgentInput, hooks.BeforeTool}}
}
func parse(raw json.RawMessage) (Options, error) {
	result := Options{EmptyMentionPrompt, ImageOnlyPrompt, SearchProgressText}
	if len(raw) != 0 {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return Options{}, err
		}
		if fields == nil {
			return Options{}, errors.New("options must be an object")
		}
		for _, value := range fields {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return Options{}, errors.New("option must be a string, not null")
			}
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&result); err != nil {
			return Options{}, err
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return Options{}, errors.New("invalid options suffix")
		}
	}
	for _, text := range []string{result.EmptyMentionPrompt, result.ImageOnlyPrompt, result.SearchProgressText} {
		if strings.TrimSpace(text) == "" || len(text) > 4096 {
			return Options{}, errors.New("reply option must contain 1..4096 bytes of nonblank text")
		}
	}
	return result, nil
}
func (Factory) ValidateOptions(raw json.RawMessage) error      { _, err := parse(raw); return err }
func (Factory) PublicOptions(raw json.RawMessage) (any, error) { return parse(raw) }
func (Factory) Build(ctx context.Context, raw json.RawMessage) (modules.Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	options, err := parse(raw)
	if err != nil {
		return nil, err
	}
	return &instance{options}, nil
}

type instance struct{ options Options }

func (i *instance) Close(context.Context) error { return nil }
func (i *instance) Registrations() hooks.Registrations {
	return hooks.Registrations{
		AgentInput: []hooks.Registration[hooks.Input, hooks.InputResult]{{ID: "input_defaults", Handle: i.prepare}},
		BeforeTool: []hooks.Registration[hooks.ToolInput, hooks.ToolResult]{{ID: "search_progress", Handle: i.tool}},
	}
}
func (i *instance) prepare(_ context.Context, _ hooks.Info, input hooks.Input) (hooks.InputResult, error) {
	if strings.TrimSpace(input.Text) != "" {
		return hooks.InputResult{}, nil
	}
	var text string
	if input.Images > 0 {
		text = i.options.ImageOnlyPrompt
	} else if input.Mentioned && input.MediaCount == 0 && !input.HasQuotedText {
		text = i.options.EmptyMentionPrompt
	} else {
		return hooks.InputResult{}, nil
	}
	return hooks.InputResult{Text: &text}, nil
}
func (i *instance) tool(_ context.Context, _ hooks.Info, input hooks.ToolInput) (hooks.ToolResult, error) {
	if input.Name != searchdocs.Name {
		return hooks.ToolResult{}, nil
	}
	return hooks.ToolResult{Progress: &hooks.ProgressEffect{Text: i.options.SearchProgressText}}, nil
}
