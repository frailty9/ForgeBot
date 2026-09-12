package structured

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type token struct {
	value     string
	auxiliary bool
}

func tokenize(text string) []token {
	text = strings.ToLower(norm.NFKC.String(text))
	terms := make([]token, 0)
	word := make([]rune, 0)
	cjk := make([]rune, 0)
	flushWord := func() {
		if len(word) == 0 {
			return
		}
		terms = appendTechnical(terms, string(word))
		word = word[:0]
	}
	flushCJK := func() {
		if len(cjk) == 0 {
			return
		}
		for _, r := range cjk {
			terms = append(terms, token{value: string(r), auxiliary: true})
		}
		if len(cjk) == 1 {
			terms = append(terms, token{value: string(cjk)})
		}
		for i := 0; i+1 < len(cjk); i++ {
			terms = append(terms, token{value: string(cjk[i : i+2])})
		}
		cjk = cjk[:0]
	}
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_./\\:-@", r):
			flushCJK()
			word = append(word, r)
		default:
			flushWord()
			flushCJK()
		}
	}
	flushWord()
	flushCJK()
	return terms
}

func appendTechnical(terms []token, raw string) []token {
	raw = strings.Trim(raw, "_./\\:-@")
	if raw == "" {
		return terms
	}
	seen := make(map[string]struct{})
	add := func(value string) {
		value = strings.ToLower(strings.Trim(value, "_./\\:-@"))
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		terms = append(terms, token{value: value})
	}
	add(raw)
	var components []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		for _, value := range splitCamelCase(part) {
			add(value)
			components = append(components, value)
		}
	}
	for i := 0; i+1 < len(components); i++ {
		add(components[i] + components[i+1])
	}
	return terms
}

func splitCamelCase(value string) []string {
	runes := []rune(value)
	if len(runes) == 0 {
		return nil
	}
	parts := make([]string, 0, 2)
	start := 0
	for i := 1; i < len(runes); i++ {
		wordBoundary := unicode.IsUpper(runes[i]) && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]))
		acronymBoundary := i+1 < len(runes) && unicode.IsUpper(runes[i-1]) && unicode.IsUpper(runes[i]) && unicode.IsLower(runes[i+1])
		if wordBoundary || acronymBoundary {
			parts = append(parts, string(runes[start:i]))
			start = i
		}
	}
	return append(parts, string(runes[start:]))
}

func uniqueTokens(tokens []token, includeAuxiliary bool) []token {
	seen := make(map[string]struct{})
	result := make([]token, 0, len(tokens))
	for _, item := range tokens {
		if item.auxiliary && !includeAuxiliary {
			continue
		}
		if _, ok := seen[item.value]; ok {
			continue
		}
		seen[item.value] = struct{}{}
		result = append(result, item)
	}
	return result
}

func normalizeText(text string) string {
	return strings.ToLower(norm.NFKC.String(strings.TrimSpace(text)))
}
