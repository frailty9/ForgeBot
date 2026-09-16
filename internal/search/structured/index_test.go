package structured

import (
	"context"
	"reflect"
	"strings"
	"testing"

	searchcontract "github.com/soleapi/forgebot/internal/search"
)

func TestIndexRanksStructuredFieldsAndTechnicalTerms(t *testing.T) {
	index := newIndex([]Document{
		{Path: "api/auth.md", Title: "身份验证", Tags: []string{"security"}, Sections: []Section{{ID: "auth", Path: "api/auth.md", Title: "身份验证", Headings: []string{"API Key"}, Body: "InvalidAPIKey 表示凭证无效。", Ordinal: 0}}},
		{Path: "notes/general.md", Title: "一般说明", Sections: []Section{{ID: "general", Path: "notes/general.md", Title: "一般说明", Body: "API 可能返回一般错误。", Ordinal: 0}}},
	})
	response, err := index.Search(context.Background(), searchcontract.Query{Text: "InvalidAPIKey", Limit: 5}, .35)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || response.Results[0].Path != "api/auth.md" || response.Results[0].MatchRatio != 1 {
		t.Fatalf("results = %+v", response.Results)
	}
	response, err = index.Search(context.Background(), searchcontract.Query{Text: "身份验证", Limit: 5}, .35)
	if err != nil || len(response.Results) == 0 || response.Results[0].Path != "api/auth.md" {
		t.Fatalf("Chinese results = %+v, err=%v", response.Results, err)
	}
}

func TestIndexFiltersWeakMatchesAndSortsDeterministically(t *testing.T) {
	index := newIndex([]Document{
		{Path: "b.md", Title: "Token", Sections: []Section{{ID: "b", Path: "b.md", Title: "Token", Body: "token", Ordinal: 0}}},
		{Path: "a.md", Title: "Token", Sections: []Section{{ID: "a", Path: "a.md", Title: "Token", Body: "token", Ordinal: 0}}},
	})
	response, err := index.Search(context.Background(), searchcontract.Query{Text: "token missing unrelated", Limit: 5}, .5)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 0 {
		t.Fatalf("weak match not filtered: %+v", response.Results)
	}
	response, err = index.Search(context.Background(), searchcontract.Query{Text: "token", Limit: 5}, .35)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{response.Results[0].Path, response.Results[1].Path}
	if !reflect.DeepEqual(paths, []string{"a.md", "b.md"}) {
		t.Fatalf("paths = %#v", paths)
	}
}

func TestIndexRejectsEmptyAndOversizedQueries(t *testing.T) {
	index := newIndex(nil)
	for _, query := range []string{"...", strings.Repeat("a", 1001)} {
		if _, err := index.Search(context.Background(), searchcontract.Query{Text: query, Limit: 1}, .35); err == nil {
			t.Fatalf("query %q accepted", query)
		}
	}
}
