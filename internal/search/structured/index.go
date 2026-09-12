package structured

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	searchcontract "github.com/soleapi/forgebot/internal/search"
)

type field int

const (
	fieldTitle field = iota
	fieldHeading
	fieldMetadata
	fieldPath
	fieldCode
	fieldBody
	fieldCount
)

var fieldWeights = [fieldCount]struct{ boost, b float64 }{
	fieldTitle: {5, .5}, fieldHeading: {4, .55}, fieldMetadata: {3.5, .5},
	fieldPath: {3, .3}, fieldCode: {2, .65}, fieldBody: {1, .75},
}

type Index struct {
	documents int
	sections  []Section
	indexed   []indexedSection
	df        map[string]int
	avgLength [fieldCount]float64
}

type indexedSection struct{ fields [fieldCount]indexedField }
type indexedField struct {
	text      string
	length    float64
	frequency map[string]float64
}

func newIndex(documents []Document) *Index {
	idx := &Index{documents: len(documents), df: make(map[string]int)}
	for _, document := range documents {
		for _, section := range document.Sections {
			idx.sections = append(idx.sections, section)
			fieldText := [fieldCount]string{
				fieldTitle: document.Title, fieldHeading: strings.Join(section.Headings, " "),
				fieldMetadata: strings.TrimSpace(document.Summary + " " + strings.Join(document.Tags, " ")),
				fieldPath:     section.Path, fieldCode: codeText(section.Code), fieldBody: section.Body,
			}
			var indexed indexedSection
			seen := make(map[string]struct{})
			for f := field(0); f < fieldCount; f++ {
				tokens := tokenize(fieldText[f])
				frequencies := make(map[string]float64)
				for _, item := range tokens {
					weight := 1.0
					if item.auxiliary {
						weight = .2
					}
					frequencies[item.value] += weight
					seen[item.value] = struct{}{}
				}
				indexed.fields[f] = indexedField{text: normalizeText(fieldText[f]), length: float64(len(tokens)), frequency: frequencies}
				idx.avgLength[f] += float64(len(tokens))
			}
			for term := range seen {
				idx.df[term]++
			}
			idx.indexed = append(idx.indexed, indexed)
		}
	}
	if len(idx.sections) > 0 {
		for f := field(0); f < fieldCount; f++ {
			idx.avgLength[f] /= float64(len(idx.sections))
		}
	}
	return idx
}

func (i *Index) Search(ctx context.Context, query searchcontract.Query, minMatchRatio float64) (searchcontract.Response, error) {
	started := time.Now()
	text := strings.TrimSpace(query.Text)
	if text == "" || utf8.RuneCountInString(text) > 1000 {
		return searchcontract.Response{}, searchcontract.ErrInvalidQuery
	}
	allTerms := uniqueTokens(tokenize(text), true)
	primary := uniqueTokens(tokenize(text), false)
	if len(primary) == 0 {
		return searchcontract.Response{}, searchcontract.ErrInvalidQuery
	}
	type candidate struct {
		result  searchcontract.Result
		ordinal int
	}
	results := make([]candidate, 0)
	for position, indexed := range i.indexed {
		if position%64 == 0 {
			select {
			case <-ctx.Done():
				return searchcontract.Response{}, ctx.Err()
			default:
			}
		}
		score, matched := i.score(indexed, text, allTerms, primary)
		ratio := float64(matched) / float64(len(primary))
		if score <= 0 || ratio < minMatchRatio {
			continue
		}
		section := i.sections[position]
		results = append(results, candidate{ordinal: section.Ordinal, result: searchcontract.Result{
			SectionID: section.ID, Path: section.Path, Title: section.Title, Headings: append([]string(nil), section.Headings...),
			Snippet: snippet(section.Body, text, 800), CodeLang: matchingCodeLanguage(section.Code, allTerms),
			Tags: append([]string(nil), section.Tags...), Score: score * (1 + ratio), MatchRatio: ratio,
		}})
	}
	sort.Slice(results, func(a, b int) bool {
		if results[a].result.Score != results[b].result.Score {
			return results[a].result.Score > results[b].result.Score
		}
		if results[a].result.Path != results[b].result.Path {
			return results[a].result.Path < results[b].result.Path
		}
		if results[a].ordinal != results[b].ordinal {
			return results[a].ordinal < results[b].ordinal
		}
		return results[a].result.SectionID < results[b].result.SectionID
	})
	if len(results) > query.Limit {
		results = results[:query.Limit]
	}
	response := searchcontract.Response{Query: text, Results: make([]searchcontract.Result, len(results)), Took: time.Since(started)}
	for n := range results {
		response.Results[n] = results[n].result
	}
	return response, nil
}

func (i *Index) score(section indexedSection, query string, allTerms, primary []token) (float64, int) {
	const k1 = 1.2
	score := 0.0
	matchedPrimary := make(map[string]struct{})
	primarySet := make(map[string]struct{}, len(primary))
	for _, item := range primary {
		primarySet[item.value] = struct{}{}
	}
	for _, item := range allTerms {
		df := i.df[item.value]
		if df == 0 {
			continue
		}
		weighted := 0.0
		for f := field(0); f < fieldCount; f++ {
			frequency := section.fields[f].frequency[item.value]
			if frequency == 0 {
				continue
			}
			settings := fieldWeights[f]
			normal := 1.0
			if i.avgLength[f] > 0 {
				normal = 1 - settings.b + settings.b*section.fields[f].length/i.avgLength[f]
			}
			weighted += settings.boost * frequency / normal
		}
		if weighted > 0 {
			idf := math.Log(1 + (float64(len(i.sections)-df)+.5)/(float64(df)+.5))
			score += idf * weighted * (k1 + 1) / (k1 + weighted)
			if _, ok := primarySet[item.value]; ok {
				matchedPrimary[item.value] = struct{}{}
			}
		}
	}
	normalized := normalizeText(query)
	if utf8.RuneCountInString(normalized) >= 2 {
		boosts := [fieldCount]float64{10, 8, 6, 7, 4, 2}
		for f := field(0); f < fieldCount; f++ {
			if strings.Contains(section.fields[f].text, normalized) {
				score += boosts[f]
			}
		}
	}
	return score, len(matchedPrimary)
}

func codeText(blocks []CodeBlock) string {
	var values []string
	for _, block := range blocks {
		values = append(values, block.Language, block.Content)
	}
	return strings.Join(values, "\n")
}

func matchingCodeLanguage(blocks []CodeBlock, terms []token) string {
	for _, block := range blocks {
		normalized := normalizeText(block.Content)
		for _, term := range terms {
			if strings.Contains(normalized, term.value) {
				return block.Language
			}
		}
	}
	return ""
}

func snippet(body, query string, maxRunes int) string {
	value := strings.TrimSpace(body)
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	position := strings.Index(normalizeText(value), normalizeText(query))
	start := 0
	if position > 0 {
		start = utf8.RuneCountInString(value[:position]) - maxRunes/3
		if start < 0 {
			start = 0
		}
	}
	end := start + maxRunes
	if end > len(runes) {
		end = len(runes)
		start = end - maxRunes
	}
	prefix, suffix := "", ""
	if start > 0 {
		prefix = "..."
	}
	if end < len(runes) {
		suffix = "..."
	}
	return prefix + string(runes[start:end]) + suffix
}
