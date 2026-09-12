package structured

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParserExtractsMarkdownAndToleratesMDX(t *testing.T) {
	source := []byte(`---
title: API 指南
description: 身份认证参考
tags: [api, key]
---
import Tabs from '@theme/Tabs'

# 身份验证

使用 API Key。

## 请求示例

~~~typescript
const heading = "# 这不是标题"
~~~

<Tabs>
可检索的 MDX 内容
</Tabs>
`)
	document, err := newParser(8000).Parse("guide/auth.mdx", source)
	if err != nil {
		t.Fatal(err)
	}
	if document.Title != "API 指南" || !reflect.DeepEqual(document.Tags, []string{"api", "key"}) {
		t.Fatalf("document metadata = %+v", document)
	}
	if len(document.Sections) != 2 {
		t.Fatalf("sections = %+v", document.Sections)
	}
	if got := document.Sections[1].Headings; !reflect.DeepEqual(got, []string{"身份验证", "请求示例"}) {
		t.Fatalf("headings = %#v", got)
	}
	if len(document.Sections[1].Code) != 1 || document.Sections[1].Code[0].Language != "typescript" {
		t.Fatalf("code = %+v", document.Sections[1].Code)
	}
	if !strings.Contains(document.Sections[1].Body, "可检索的 MDX 内容") || strings.Contains(document.Sections[0].Body, "import Tabs") {
		t.Fatalf("MDX body = %q, preamble = %q", document.Sections[1].Body, document.Sections[0].Body)
	}
	if document.Sections[0].StartLine != 8 {
		t.Fatalf("heading line = %d", document.Sections[0].StartLine)
	}
}

func TestParserSplitsSectionsWithinRuneBudgetAndProducesStableIDs(t *testing.T) {
	source := []byte("# 标题\n\n第一段内容\n\n第二段内容\n")
	parser := newParser(6)
	first, err := parser.Parse("guide/a.md", source)
	if err != nil {
		t.Fatal(err)
	}
	second, err := parser.Parse("guide/a.md", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Sections) < 2 || len(first.Sections) != len(second.Sections) {
		t.Fatalf("sections = %+v", first.Sections)
	}
	for i := range first.Sections {
		if utf8.RuneCountInString(first.Sections[i].Body) > 6 {
			t.Fatalf("section exceeds budget: %q", first.Sections[i].Body)
		}
		if first.Sections[i].ID != second.Sections[i].ID {
			t.Fatal("stable ID changed across identical parses")
		}
	}
}

func TestParserRejectsInvalidUTF8AndFrontmatter(t *testing.T) {
	parser := newParser(100)
	if _, err := parser.Parse("bad.md", []byte{0xff}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	if _, err := parser.Parse("bad.md", []byte("---\ntitle: [bad\n---\n# H\n")); err == nil {
		t.Fatal("invalid frontmatter accepted")
	}
}

func TestParserTreatsTXTAsPlainText(t *testing.T) {
	document, err := newParser(8000).Parse("extra/answer.txt", []byte("---\ntitle: not frontmatter\n---\n# not a heading\n\nAPI Key 使用说明"))
	if err != nil {
		t.Fatal(err)
	}
	if document.Title != "answer" || len(document.Sections) != 1 {
		t.Fatalf("document = %+v", document)
	}
	section := document.Sections[0]
	if len(section.Headings) != 0 || !strings.Contains(section.Body, "title: not frontmatter") || !strings.Contains(section.Body, "# not a heading") {
		t.Fatalf("plain text section = %+v", section)
	}
}
