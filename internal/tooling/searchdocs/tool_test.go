package searchdocs

import (
	"context"
	"testing"

	searchcontract "github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/tooling"
)

type retriever struct {
	calls int
	query searchcontract.Query
}

func (r *retriever) Search(_ context.Context, query searchcontract.Query) (searchcontract.Response, error) {
	r.calls++
	r.query = query
	return searchcontract.Response{Query: query.Text, Results: []searchcontract.Result{{Path: "a.md", Snippet: "content", Score: 1, MatchRatio: 1}}}, nil
}
func (*retriever) Reload(context.Context) (searchcontract.ReloadResult, error) {
	return searchcontract.ReloadResult{}, nil
}
func (*retriever) Status() searchcontract.Status { return searchcontract.Status{Enabled: true} }

func TestSearchDocsStrictArgumentsAndLimit(t *testing.T) {
	r := &retriever{}
	tool := New(r, 5, 8, 1000)
	for _, arguments := range []string{`{}`, `{"query":""}`, `{"query":"x","unknown":true}`, `broken`} {
		result := tool.Execute(context.Background(), tooling.Call{Name: Name, Arguments: arguments})
		if result.Code != "invalid_arguments" {
			t.Fatalf("arguments=%q result=%+v", arguments, result)
		}
	}
	result := tool.Execute(context.Background(), tooling.Call{Name: Name, Arguments: `{"query":"auth","limit":99}`})
	if !result.OK || r.calls != 1 || r.query.Limit != 8 {
		t.Fatalf("result=%+v calls=%d query=%+v", result, r.calls, r.query)
	}
}
