package pipeline_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/knowledge/markdown"
	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/messaging"
	"github.com/soleapi/forgebot/internal/reflection/pipeline"
	"github.com/soleapi/forgebot/internal/reflection/reflector"
	"github.com/soleapi/forgebot/internal/reflection/session"
	"github.com/soleapi/forgebot/internal/search"
	"github.com/soleapi/forgebot/internal/search/structured"
)

type documentClient struct{ result reflector.Result }

func (c documentClient) Complete(context.Context, llm.Request) (llm.Response, error) {
	data, err := json.Marshal(c.result)
	return llm.Response{Text: string(data)}, err
}

func TestAcceptedDocumentIsPublishedAndSearchableWithoutSpecialParser(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	retriever, err := structured.New(ctx, structured.Options{
		Root: root, Extensions: []string{".md"}, DefaultResults: 5, MaxResults: 8, MinMatchRatio: .35,
		QueryTimeout: time.Second, BuildTimeout: time.Second, MaxSectionChars: 8000,
		MaxFileBytes: 100000, MaxTotalBytes: 1000000, MaxFiles: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := markdown.New(markdown.Config{Root: root, ExtraDirectory: "extra", MaxBytes: 100000})
	if err != nil {
		t.Fatal(err)
	}
	result := reflector.Result{
		Accept: true, Title: "API Key 轮换", Summary: "使用控制台完成凭据更新，避免服务中断。",
		Body: "## 创建替换 Key\n\n在控制台创建新 Key。\n\n## 更新客户端\n\n将新 Key 写入环境变量，确认正常后撤销旧 Key。\n\n```sh\nexport API_KEY=REPLACEMENT\n```",
		Tags: []string{"credential-lifecycle"}, Confidence: .99, Reason: "reusable procedure",
	}
	client := &documentClient{result: result}
	editor, err := reflector.New(client, "model")
	if err != nil {
		t.Fatal(err)
	}
	reloads := 0
	processor, err := pipeline.New(pipeline.Config{Reflector: editor, Publisher: publisher, MinConfidence: .9, Reload: func(ctx context.Context) error {
		reloads++
		_, err := retriever.Reload(ctx)
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
	candidate := session.AnswerSession{
		Question:  messaging.InboundMessage{ConversationID: "source-group", MessageID: "source-question", Text: "如何轮换 API Key？"},
		Answers:   []messaging.InboundMessage{{MessageID: "source-answer", Text: "在控制台创建新 Key，写入环境变量 export API_KEY=REPLACEMENT，确认服务正常后撤销旧 Key。"}},
		StartedAt: time.Now(),
	}
	errs := processor.ProcessBatch(ctx, []session.AnswerSession{candidate})
	if len(errs) != 1 || errs[0] != nil || reloads != 1 || processor.Stats().Published != 1 || retriever.Status().Documents != 1 {
		t.Fatalf("errs=%v reloads=%d pipeline=%+v search=%+v", errs, reloads, processor.Stats(), retriever.Status())
	}
	for _, query := range []string{"API Key 轮换", "创建替换", "凭据更新", "credential-lifecycle", "API_KEY"} {
		t.Run(query, func(t *testing.T) {
			response, err := retriever.Search(ctx, search.Query{Text: query})
			if err != nil || len(response.Results) == 0 {
				t.Fatalf("response=%+v err=%v", response, err)
			}
			for _, hit := range response.Results {
				if hit.Title != result.Title || len(hit.Tags) != 1 || hit.Tags[0] != result.Tags[0] || strings.Contains(hit.Snippet, "source-group") || strings.Contains(hit.Snippet, "managed_by") {
					t.Fatalf("hit=%+v", hit)
				}
			}
			if query == "API_KEY" && (response.Results[0].CodeLang != "sh" || strings.Join(response.Results[0].Headings, " > ") != "API Key 轮换 > 更新客户端") {
				t.Fatalf("code hit=%+v", response.Results[0])
			}
		})
	}
	files, err := filepath.Glob(filepath.Join(root, "extra", "*.md"))
	if err != nil || len(files) != 1 {
		t.Fatalf("files=%v err=%v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil || !strings.HasSuffix(string(data), "# "+result.Title+"\n\n"+result.Summary+"\n\n"+result.Body+"\n") {
		t.Fatalf("document=%s err=%v", data, err)
	}
	// A later wording change from the same source still preserves the first file.
	client.result.Body += "\n\n确认完成后结束轮换。"
	errs = processor.ProcessBatch(ctx, []session.AnswerSession{candidate})
	if errs[0] != nil || reloads != 1 || processor.Stats().Duplicates != 1 {
		t.Fatalf("duplicate errs=%v reloads=%d stats=%+v", errs, reloads, processor.Stats())
	}
	unchanged, err := os.ReadFile(files[0])
	if err != nil || string(unchanged) != string(data) {
		t.Fatalf("published document changed: %v", err)
	}
}
