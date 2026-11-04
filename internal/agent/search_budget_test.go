package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/soleapi/forgebot/internal/llm"
	searchcontract "github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/tooling"
	"github.com/soleapi/forgebot/internal/tooling/searchdocs"
)

type chineseRetriever struct {
	response searchcontract.Response
	calls    int
}

func (r *chineseRetriever) Search(_ context.Context, q searchcontract.Query) (searchcontract.Response, error) {
	r.calls++
	r.response.Query = q.Text
	return r.response, nil
}
func (*chineseRetriever) Reload(context.Context) (searchcontract.ReloadResult, error) {
	return searchcontract.ReloadResult{}, nil
}
func (*chineseRetriever) Status() searchcontract.Status { return searchcontract.Status{Enabled: true} }

func TestDefaultBudgetPreservesChineseSearchEvidence(t *testing.T) {
	for _, tc := range []struct {
		name          string
		chars, budget int
		exhausted     bool
	}{
		{"6000-chinese", 6000, 0, false}, {"12000-context", 14000, 0, false}, {"escaped-content", 14000, 0, false}, {"explicit-16k", 6000, 16 << 10, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Repeat("资料", tc.chars/2)
			if tc.name == "escaped-content" {
				body = strings.Repeat("中文\n\"路径\\示例", 2000)
			}
			r := &chineseRetriever{response: searchcontract.Response{Results: []searchcontract.Result{
				{Path: "中文/指南.md", Headings: []string{"配置说明"}, Score: 1},
				{Path: "中文/补充.md", Score: .9},
			}}}
			// 按 rune 分节，避免中文 UTF-8 字符在字节切片处被拆开。
			runes := []rune(body)
			r.response.Results[0].Snippet = string(runes[:len(runes)/2])
			r.response.Results[1].Snippet = string(runes[len(runes)/2:])
			tool := searchdocs.New(r, 5, 8, 12000)
			turns := 0
			loop := Loop{Limits: Limits{MaxResultBytes: tc.budget}, ToolBudgets: map[string]int{searchdocs.Name: 1},
				Execute: func(ctx context.Context, c tooling.Call) (tooling.Result, error) { return tool.Execute(ctx, c), nil },
				Client: modelFunc(func(_ context.Context, req llm.Request) (llm.Response, error) {
					turns++
					if turns == 1 {
						return toolResponse("search", searchdocs.Name, `{"query":"配置"}`), nil
					}
					if len(req.Tools) != 0 {
						t.Fatal("single search budget not enforced")
					}
					var result tooling.Result
					if err := json.Unmarshal([]byte(req.Messages[len(req.Messages)-1].Text()), &result); err != nil {
						t.Fatal(err)
					}
					if tc.exhausted {
						if result.Code != "budget_exhausted" {
							t.Fatal(result)
						}
					} else {
						if !result.OK {
							t.Fatal(result)
						}
						var payload struct{ Context string }
						if err := json.Unmarshal([]byte(result.Payload), &payload); err != nil {
							t.Fatal(err)
						}
						count := utf8.RuneCountInString(payload.Context)
						if count < 6000 || count > 12000 || !strings.Contains(payload.Context, "[source:") {
							t.Fatal("evidence lost", count)
						}
						if tc.chars > 12000 && count < 11900 {
							t.Fatal("full default context not exercised", count)
						}
					}
					return llm.Response{Text: "answer"}, nil
				}),
			}
			req := loopRequest()
			req.Tools = []llm.ToolDefinition{{Name: searchdocs.Name}}
			response, err := loop.Run(context.Background(), req)
			if err != nil || response.Text != "answer" || r.calls != 1 || turns != 2 {
				t.Fatal(response, err, r.calls, turns)
			}
		})
	}
}
