// Package markdown publishes ForgeBot-managed knowledge into one controlled
// subdirectory of the configured search root.
package markdown

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/soleapi/forgebot/internal/knowledge"
	"gopkg.in/yaml.v3"
)

var (
	ErrInvalidConfig = errors.New("markdown publisher configuration is invalid")
	ErrInvalidEntry  = errors.New("markdown knowledge entry is invalid")
	ErrPathCollision = errors.New("managed markdown path collides with an unmanaged file")
)

type Config struct {
	Root           string
	ExtraDirectory string
	MaxBytes       int64
}

type Publisher struct {
	root     string
	extra    string
	maxBytes int64
}

func New(cfg Config) (*Publisher, error) {
	rawRoot := strings.TrimSpace(cfg.Root)
	if rawRoot == "" || strings.TrimSpace(cfg.ExtraDirectory) != "extra" || cfg.MaxBytes <= 0 {
		return nil, ErrInvalidConfig
	}
	root, err := filepath.Abs(rawRoot)
	if err != nil || root == "" || strings.TrimSpace(cfg.ExtraDirectory) != "extra" || cfg.MaxBytes <= 0 {
		return nil, ErrInvalidConfig
	}
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		return nil, ErrInvalidConfig
	}
	extra := filepath.Join(root, "extra")
	if info, statErr := os.Lstat(extra); statErr == nil && !info.IsDir() {
		return nil, ErrInvalidConfig
	}
	if err := os.MkdirAll(extra, 0o755); err != nil {
		return nil, fmt.Errorf("create extra directory: %w", err)
	}
	return &Publisher{root: root, extra: extra, maxBytes: cfg.MaxBytes}, nil
}

func (p *Publisher) Publish(entry knowledge.Entry) (knowledge.PublishResult, error) {
	canonical, hash, err := canonicalEntry(entry)
	if err != nil {
		return knowledge.PublishResult{}, err
	}
	if duplicate, path, err := p.findDuplicate(canonical, hash); err != nil {
		return knowledge.PublishResult{}, err
	} else if duplicate {
		return knowledge.PublishResult{Status: knowledge.Duplicate, Path: path, ContentHash: hash}, nil
	}
	path := filepath.Join(p.extra, hash[:16]+".md")
	if !withinDirectory(p.extra, path) || filepath.Ext(path) != ".md" {
		return knowledge.PublishResult{}, ErrInvalidConfig
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return knowledge.PublishResult{}, ErrPathCollision
		}
		return knowledge.PublishResult{}, ErrPathCollision
	} else if !errors.Is(err, os.ErrNotExist) {
		return knowledge.PublishResult{}, err
	}
	body, err := render(canonical)
	if err != nil {
		return knowledge.PublishResult{}, err
	}
	if int64(len(body)) > p.maxBytes {
		return knowledge.PublishResult{}, fmt.Errorf("%w: rendered document exceeds limit", ErrInvalidEntry)
	}
	if err := atomicWrite(path, body, p.maxBytes); err != nil {
		return knowledge.PublishResult{}, err
	}
	return knowledge.PublishResult{Status: knowledge.Published, Path: filepath.ToSlash(filepath.Join("extra", filepath.Base(path))), ContentHash: hash}, nil
}

func (p *Publisher) findDuplicate(entry knowledge.Entry, hash string) (bool, string, error) {
	entries, err := os.ReadDir(p.extra)
	if err != nil {
		return false, "", err
	}
	for _, item := range entries {
		if !strings.HasSuffix(strings.ToLower(item.Name()), ".md") {
			continue
		}
		path := filepath.Join(p.extra, item.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return false, "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, "", fmt.Errorf("%w: %s", ErrPathCollision, item.Name())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return false, "", err
		}
		if int64(len(data)) > p.maxBytes {
			continue
		}
		var meta struct {
			ManagedBy          string `yaml:"managed_by"`
			SourceConversation string `yaml:"source_conversation_id"`
			SourceQuestion     string `yaml:"source_question_message_id"`
			SourceAnswer       string `yaml:"source_answer_message_id"`
			ContentHash        string `yaml:"content_hash"`
		}
		if !parseFrontmatter(data, &meta) || meta.ManagedBy != "forgebot" {
			continue
		}
		if meta.ContentHash == hash || (meta.SourceConversation == entry.ConversationID && meta.SourceQuestion == entry.SourceQuestionMessageID && meta.SourceAnswer == entry.SourceAnswerMessageID) {
			return true, filepath.ToSlash(filepath.Join("extra", item.Name())), nil
		}
	}
	return false, "", nil
}

func canonicalEntry(entry knowledge.Entry) (knowledge.Entry, string, error) {
	entry.Title, entry.Question, entry.Answer = strings.TrimSpace(entry.Title), strings.TrimSpace(entry.Question), strings.TrimSpace(entry.Answer)
	entry.ConversationID = strings.TrimSpace(entry.ConversationID)
	entry.SourceQuestionMessageID = strings.TrimSpace(entry.SourceQuestionMessageID)
	entry.SourceAnswerMessageID = strings.TrimSpace(entry.SourceAnswerMessageID)
	if entry.Title == "" || entry.Question == "" || entry.Answer == "" || entry.ConversationID == "" || entry.SourceQuestionMessageID == "" || entry.SourceAnswerMessageID == "" {
		return knowledge.Entry{}, "", ErrInvalidEntry
	}
	if utf8.RuneCountInString(entry.Title) > 200 || utf8.RuneCountInString(entry.Question) > 4000 || utf8.RuneCountInString(entry.Answer) > 12000 {
		return knowledge.Entry{}, "", ErrInvalidEntry
	}
	seen := make(map[string]struct{}, len(entry.Tags))
	tags := make([]string, 0, len(entry.Tags))
	for _, tag := range entry.Tags {
		tag = strings.TrimSpace(strings.ToLower(tag))
		if tag == "" || utf8.RuneCountInString(tag) > 64 {
			return knowledge.Entry{}, "", ErrInvalidEntry
		}
		if _, ok := seen[tag]; !ok {
			seen[tag] = struct{}{}
			tags = append(tags, tag)
		}
	}
	sort.Strings(tags)
	entry.Tags = tags
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	} else {
		entry.CreatedAt = entry.CreatedAt.UTC()
	}
	basis := strings.Join([]string{entry.ConversationID, entry.SourceQuestionMessageID, entry.SourceAnswerMessageID, entry.Title, entry.Question, entry.Answer, strings.Join(entry.Tags, "\x00")}, "\x1f")
	digest := sha256.Sum256([]byte(basis))
	return entry, hex.EncodeToString(digest[:]), nil
}

func render(entry knowledge.Entry) ([]byte, error) {
	front, err := yaml.Marshal(map[string]any{"title": entry.Title, "tags": entry.Tags, "managed_by": "forgebot", "source_conversation_id": entry.ConversationID, "source_question_message_id": entry.SourceQuestionMessageID, "source_answer_message_id": entry.SourceAnswerMessageID, "content_hash": contentHash(entry), "created_at": entry.CreatedAt.Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}
	text := "---\n" + string(front) + "---\n\n# " + entry.Title + "\n\n## 问题\n\n" + entry.Question + "\n\n## 答案\n\n" + entry.Answer + "\n"
	return []byte(text), nil
}

func contentHash(entry knowledge.Entry) string {
	basis := strings.Join([]string{entry.ConversationID, entry.SourceQuestionMessageID, entry.SourceAnswerMessageID, entry.Title, entry.Question, entry.Answer, strings.Join(entry.Tags, "\x00")}, "\x1f")
	digest := sha256.Sum256([]byte(basis))
	return hex.EncodeToString(digest[:])
}

func parseFrontmatter(data []byte, out any) bool {
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return false
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return false
	}
	return yaml.Unmarshal([]byte(text[4:4+end]), out) == nil
}

func atomicWrite(path string, data []byte, maxBytes int64) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".forgebot-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if int64(len(data)) > maxBytes {
		_ = tmp.Close()
		return ErrInvalidEntry
	}
	_ = tmp.Chmod(0o644)
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func withinDirectory(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}
