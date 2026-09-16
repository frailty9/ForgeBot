package searchdocs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	searchcontract "github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/tooling"
)

const Name = "search_docs"

type Tool struct {
	retriever       searchcontract.Retriever
	defaultResults  int
	maxResults      int
	maxContextChars int
}

func New(retriever searchcontract.Retriever, defaultResults, maxResults, maxContextChars int) *Tool {
	return &Tool{retriever: retriever, defaultResults: defaultResults, maxResults: maxResults, maxContextChars: maxContextChars}
}

func (t *Tool) Definition() tooling.Definition {
	return tooling.Definition{
		Name:        Name,
		Description: "在本地项目文档中搜索与问题相关的可靠资料。仅在回答依赖项目文档事实时调用。",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "具体、简短的文档关键词或问题"},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": t.maxResults},
			},
			"required": []string{"query"},
		},
	}
}

func (t *Tool) Execute(ctx context.Context, call tooling.Call) tooling.Result {
	if t == nil || t.retriever == nil || call.Name != Name {
		return tooling.Result{Code: "tool_not_allowed"}
	}
	var arguments struct {
		Query string `json:"query"`
		Limit int    `json:"limit,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewBufferString(call.Arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return tooling.Result{Code: "invalid_arguments"}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return tooling.Result{Code: "invalid_arguments"}
	}
	arguments.Query = strings.TrimSpace(arguments.Query)
	if arguments.Query == "" || arguments.Limit < 0 {
		return tooling.Result{Code: "invalid_arguments"}
	}
	if arguments.Limit == 0 {
		arguments.Limit = t.defaultResults
	}
	if arguments.Limit > t.maxResults {
		arguments.Limit = t.maxResults
	}
	response, err := t.retriever.Search(ctx, searchcontract.Query{Text: arguments.Query, Limit: arguments.Limit})
	if err != nil {
		code := "search_failed"
		if errors.Is(err, context.DeadlineExceeded) {
			code = "search_timeout"
		} else if errors.Is(err, searchcontract.ErrInvalidQuery) {
			code = "invalid_arguments"
		} else if errors.Is(err, searchcontract.ErrDisabled) || errors.Is(err, searchcontract.ErrNoIndex) {
			code = "search_unavailable"
		}
		return tooling.Result{Code: code}
	}
	contextText, _ := searchcontract.BuildContext(response, t.maxContextChars)
	if contextText == "" {
		return tooling.Result{OK: true, Code: "no_results", Payload: `{"results":[]}`}
	}
	payload, _ := json.Marshal(map[string]any{"query": response.Query, "context": contextText})
	return tooling.Result{OK: true, Payload: string(payload)}
}
