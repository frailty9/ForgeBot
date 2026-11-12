package markdown

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/soleapi/forgebot/internal/knowledge"
)

func testEntry() knowledge.Entry {
	return knowledge.Entry{Title: "API Key 轮换", Summary: "通过控制台轮换 API Key，更新客户端后再撤销旧 Key。", Body: "## 创建替换 Key\n\n在控制台创建新 Key。\n\n## 更新客户端\n\n使用新 Key 更新客户端，确认正常后撤销旧 Key。", Tags: []string{"API-Key", "console"}, ConversationID: "support", SourceQuestionMessageID: "q1", SourceAnswerMessageID: "a1", CreatedAt: time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)}
}

func TestPublisherWritesManagedMarkdownAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	p, err := New(Config{Root: root, ExtraDirectory: "extra", MaxBytes: 100000})
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.Publish(testEntry())
	if err != nil || first.Status != knowledge.Published {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(first.Path)))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "managed_by: forgebot") || !strings.Contains(text, "api-key") || !strings.HasSuffix(text, "# "+testEntry().Title+"\n\n"+testEntry().Summary+"\n\n"+testEntry().Body+"\n") || strings.Contains(text, "## 问题") || strings.Contains(text, "## 答案") {
		t.Fatalf("document=%s", text)
	}
	var meta struct {
		Summary string `yaml:"summary"`
	}
	if !parseFrontmatter(data, &meta) || meta.Summary != testEntry().Summary {
		t.Fatalf("summary metadata=%+v", meta)
	}
	second, err := p.Publish(testEntry())
	if err != nil || second.Status != knowledge.Duplicate || second.Path != first.Path {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}

func TestPublisherRecognizesPreviouslyPublishedQuestionAnswerDocument(t *testing.T) {
	root := t.TempDir()
	p, err := New(Config{Root: root, ExtraDirectory: "extra", MaxBytes: 100000})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "extra", "old.md")
	old := "---\nmanaged_by: forgebot\nsource_conversation_id: support\nsource_question_message_id: q1\nsource_answer_message_id: a1\ncontent_hash: old-hash\n---\n\n# Old title\n\n## 问题\n\nHow?\n\n## 答案\n\nRotate it.\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := p.Publish(testEntry())
	if err != nil || result.Status != knowledge.Duplicate || result.Path != "extra/old.md" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != old {
		t.Fatalf("old document changed: %v", err)
	}
}

func TestPublisherDoesNotOverwriteUnmanagedCollision(t *testing.T) {
	root := t.TempDir()
	p, _ := New(Config{Root: root, ExtraDirectory: "extra", MaxBytes: 100000})
	entry := testEntry()
	_, hash, err := canonicalEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "extra", hash[:16]+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# human document\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Publish(entry); !errors.Is(err, ErrPathCollision) {
		t.Fatalf("err=%v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "# human document\n" {
		t.Fatal("human file was overwritten")
	}
}

func TestPublisherConstructionDoesNotCreateOutputDirectory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "extra")
	if _, err := New(Config{Root: root, ExtraDirectory: "extra", MaxBytes: 100000}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("constructor created output directory: %v", err)
	}
}

func TestPublisherRejectsInvalidEntriesAndOversizedOutput(t *testing.T) {
	root := t.TempDir()
	if _, err := New(Config{Root: root, ExtraDirectory: "", MaxBytes: 100}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty directory config err=%v", err)
	}
	pOther, err := New(Config{Root: root, ExtraDirectory: "generated", MaxBytes: 100000})
	if err != nil {
		t.Fatalf("relative config err=%v", err)
	}
	if _, err := pOther.Publish(testEntry()); err != nil {
		t.Fatalf("relative publish err=%v", err)
	}
	absolute := filepath.Join(t.TempDir(), "generated-knowledge")
	pAbsolute, err := New(Config{Root: root, ExtraDirectory: absolute, MaxBytes: 100000})
	if err != nil {
		t.Fatalf("absolute config err=%v", err)
	}
	result, err := pAbsolute.Publish(testEntry())
	if err != nil {
		t.Fatalf("absolute publish err=%v", err)
	}
	if !filepath.IsAbs(filepath.FromSlash(result.Path)) {
		t.Fatalf("absolute result path=%q", result.Path)
	}
	if _, err := os.Stat(filepath.FromSlash(result.Path)); err != nil {
		t.Fatalf("absolute output missing: %v", err)
	}
	fileTarget := filepath.Join(t.TempDir(), "output-file")
	if err := os.WriteFile(fileTarget, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{Root: root, ExtraDirectory: fileTarget, MaxBytes: 100000}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("file target config err=%v", err)
	}
	p, _ := New(Config{Root: root, ExtraDirectory: "extra", MaxBytes: 20})
	if _, err := p.Publish(knowledge.Entry{Title: "missing"}); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("entry err=%v", err)
	}
	if _, err := p.Publish(testEntry()); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("size err=%v", err)
	}
}

func TestWithinDirectoryRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if withinDirectory(root, filepath.Join(root, "..", "outside")) {
		t.Fatal("traversal accepted")
	}
}
