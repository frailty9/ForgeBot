package structured

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

type CodeBlock struct {
	Language string
	Content  string
}

type Section struct {
	ID        string
	Path      string
	Ordinal   int
	Title     string
	Headings  []string
	Tags      []string
	Summary   string
	Body      string
	Code      []CodeBlock
	StartLine int
	EndLine   int
}

type Document struct {
	Path     string
	Title    string
	Tags     []string
	Summary  string
	Sections []Section
}

type frontmatter struct {
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Summary     string   `yaml:"summary"`
	Tags        []string `yaml:"tags"`
}

type parser struct {
	markdown        goldmark.Markdown
	maxSectionChars int
}

func newParser(maxSectionChars int) *parser {
	return &parser{markdown: goldmark.New(), maxSectionChars: maxSectionChars}
}

func (p *parser) Parse(path string, source []byte) (Document, error) {
	if !utf8.Valid(source) {
		return Document{}, fmt.Errorf("document is not valid UTF-8")
	}
	path = filepath.ToSlash(filepath.Clean(path))
	content := bytes.TrimPrefix(source, []byte{0xef, 0xbb, 0xbf})
	meta, markdownSource, firstLine, err := parseFrontmatter(content)
	if err != nil {
		return Document{}, err
	}
	doc := p.markdown.Parser().Parse(text.NewReader(markdownSource))
	blocks := topLevelBlocks(doc)
	title := strings.TrimSpace(meta.Title)
	headingStack := make([]string, 6)
	sections := make([]Section, 0)
	var current *sectionBuilder
	flush := func() {
		if current == nil {
			return
		}
		for _, part := range current.finish(p.maxSectionChars) {
			part.Path = path
			part.Ordinal = len(sections)
			part.Tags = append([]string(nil), meta.Tags...)
			part.Summary = firstNonEmpty(meta.Description, meta.Summary)
			part.ID = sectionID(part)
			sections = append(sections, part)
		}
		current = nil
	}

	for _, block := range blocks {
		if heading, ok := block.(*ast.Heading); ok {
			flush()
			headingTitle := nodeText(heading, markdownSource)
			if title == "" && heading.Level == 1 {
				title = headingTitle
			}
			headingStack[heading.Level-1] = headingTitle
			for i := heading.Level; i < len(headingStack); i++ {
				headingStack[i] = ""
			}
			headings := make([]string, 0, heading.Level)
			for _, value := range headingStack[:heading.Level] {
				if value != "" {
					headings = append(headings, value)
				}
			}
			current = &sectionBuilder{title: headingTitle, headings: headings, startLine: lineAt(markdownSource, blockStart(block)) + firstLine - 1}
			continue
		}
		if current == nil {
			current = &sectionBuilder{startLine: firstLine}
		}
		current.add(block, markdownSource, firstLine-1)
	}
	flush()
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	for i := range sections {
		sections[i].Title = title
	}
	return Document{Path: path, Title: title, Tags: append([]string(nil), meta.Tags...), Summary: firstNonEmpty(meta.Description, meta.Summary), Sections: sections}, nil
}

type sectionBuilder struct {
	title     string
	headings  []string
	blocks    []string
	code      []CodeBlock
	startLine int
	endLine   int
}

func (b *sectionBuilder) add(node ast.Node, source []byte, offset int) {
	text := strings.TrimSpace(blockText(node, source))
	if text != "" && !isMDXDirective(text) {
		b.blocks = append(b.blocks, text)
	}
	if fenced, ok := node.(*ast.FencedCodeBlock); ok {
		var code strings.Builder
		for i := 0; i < fenced.Lines().Len(); i++ {
			segment := fenced.Lines().At(i)
			code.Write(segment.Value(source))
		}
		value := strings.TrimSpace(code.String())
		if value != "" {
			b.code = append(b.code, CodeBlock{Language: string(fenced.Language(source)), Content: value})
		}
	}
	end := blockEnd(node)
	if end > 0 {
		b.endLine = lineAt(source, end) + offset
	}
}

func (b *sectionBuilder) finish(maxChars int) []Section {
	base := Section{Headings: append([]string(nil), b.headings...), Code: append([]CodeBlock(nil), b.code...), StartLine: b.startLine, EndLine: b.endLine}
	chunks := chunkBlocks(b.blocks, maxChars)
	if len(chunks) == 0 && len(base.Headings) > 0 {
		chunks = []string{strings.Join(base.Headings, " > ")}
	}
	result := make([]Section, 0, len(chunks))
	for _, chunk := range chunks {
		part := base
		part.Body = chunk
		result = append(result, part)
	}
	return result
}

func parseFrontmatter(source []byte) (frontmatter, []byte, int, error) {
	text := strings.ReplaceAll(string(source), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return frontmatter{}, []byte(text), 1, nil
	}
	end := strings.Index(text[4:], "\n---\n")
	delimiterLen := 5
	if end < 0 {
		end = strings.Index(text[4:], "\n...\n")
	}
	if end < 0 {
		return frontmatter{}, nil, 0, fmt.Errorf("frontmatter is missing a closing delimiter")
	}
	end += 4
	var meta frontmatter
	decoder := yaml.NewDecoder(strings.NewReader(text[4:end]))
	if err := decoder.Decode(&meta); err != nil {
		return frontmatter{}, nil, 0, fmt.Errorf("invalid frontmatter: %w", err)
	}
	firstLine := strings.Count(text[:end+delimiterLen], "\n") + 1
	return meta, []byte(text[end+delimiterLen:]), firstLine, nil
}

func topLevelBlocks(document ast.Node) []ast.Node {
	blocks := make([]ast.Node, 0)
	for node := document.FirstChild(); node != nil; node = node.NextSibling() {
		blocks = append(blocks, node)
	}
	return blocks
}

func nodeText(node ast.Node, source []byte) string {
	var parts []string
	_ = ast.Walk(node, func(current ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch value := current.(type) {
		case *ast.Text:
			parts = append(parts, string(value.Segment.Value(source)))
		case *ast.CodeSpan:
			parts = append(parts, string(value.Text(source)))
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

func blockText(node ast.Node, source []byte) string {
	if fenced, ok := node.(*ast.FencedCodeBlock); ok {
		var out strings.Builder
		for i := 0; i < fenced.Lines().Len(); i++ {
			segment := fenced.Lines().At(i)
			out.Write(segment.Value(source))
		}
		return strings.TrimSpace(out.String())
	}
	value := nodeText(node, source)
	if value == "" && node.Lines().Len() > 0 {
		var raw strings.Builder
		for i := 0; i < node.Lines().Len(); i++ {
			segment := node.Lines().At(i)
			raw.Write(segment.Value(source))
		}
		value = raw.String()
	}
	return stripJSXTags(value)
}

func stripJSXTags(text string) string {
	var out strings.Builder
	inTag := false
	for _, r := range text {
		switch r {
		case '<':
			inTag = true
		case '>':
			if inTag {
				inTag = false
			} else {
				out.WriteRune(r)
			}
		default:
			if !inTag {
				out.WriteRune(r)
			}
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

func isMDXDirective(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "export ")
}

func chunkBlocks(blocks []string, maxChars int) []string {
	var result []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			result = append(result, strings.TrimSpace(string(current)))
			current = nil
		}
	}
	for _, block := range blocks {
		runes := []rune(strings.TrimSpace(block))
		for len(runes) > 0 {
			space := maxChars - len(current)
			if len(current) > 0 {
				space -= 2
			}
			if space <= 0 {
				flush()
				continue
			}
			take := len(runes)
			if take > space {
				take = space
			}
			if len(current) > 0 {
				current = append(current, '\n', '\n')
			}
			current = append(current, runes[:take]...)
			runes = runes[take:]
			if len(runes) > 0 {
				flush()
			}
		}
	}
	flush()
	return result
}

func sectionID(section Section) string {
	identity := section.Path + "\x00" + strings.Join(section.Headings, "\x00") + fmt.Sprintf("\x00%d\x00%s", section.Ordinal, section.Body)
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:16])
}

func blockStart(node ast.Node) int {
	if node.Lines().Len() > 0 {
		return node.Lines().At(0).Start
	}
	return 0
}

func blockEnd(node ast.Node) int {
	if node.Lines().Len() > 0 {
		return node.Lines().At(node.Lines().Len() - 1).Stop
	}
	return 0
}

func lineAt(source []byte, offset int) int {
	if offset > len(source) {
		offset = len(source)
	}
	return bytes.Count(source[:offset], []byte{'\n'}) + 1
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
