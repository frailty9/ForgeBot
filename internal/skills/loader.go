// Package skills loads bounded local SKILL.md instruction files. Skills are
// text-only extensions and do not execute code or gain tool permissions.
package skills

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Config limits the directories and total content accepted by Loader.
type Config struct {
	Directories                 []string
	MaxFileBytes, MaxTotalBytes int64
	MaxFiles                    int
}

// Loader reads and combines configured skill files deterministically.
type Loader struct{ cfg Config }

// New validates limits and creates a skill loader.
func New(cfg Config) (*Loader, error) {
	if cfg.MaxFileBytes <= 0 || cfg.MaxTotalBytes <= 0 || cfg.MaxFiles <= 0 {
		return nil, errors.New("skill limits must be positive")
	}
	if cfg.MaxFileBytes > cfg.MaxTotalBytes {
		return nil, errors.New("skill max_file_bytes must not exceed max_total_bytes")
	}
	return &Loader{cfg: cfg}, nil
}

// Load returns the instruction snapshot from all configured skill files.
func (l *Loader) Load(ctx context.Context) (string, error) {
	if l == nil || len(l.cfg.Directories) == 0 {
		return "", nil
	}
	paths := make([]string, 0)
	seen := map[string]struct{}{}
	for _, root := range l.cfg.Directories {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if strings.EqualFold(filepath.Base(path), "SKILL.md") {
				absolute, absErr := filepath.Abs(path)
				if absErr != nil {
					return absErr
				}
				if _, ok := seen[absolute]; !ok {
					seen[absolute] = struct{}{}
					paths = append(paths, absolute)
				}
			}
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("scan skills %q: %w", root, err)
		}
	}
	sort.Strings(paths)
	if len(paths) > l.cfg.MaxFiles {
		return "", fmt.Errorf("skill file count exceeds limit %d", l.cfg.MaxFiles)
	}
	var builder strings.Builder
	var total int64
	for _, path := range paths {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if info.Size() > l.cfg.MaxFileBytes {
			return "", fmt.Errorf("skill file %q exceeds %d bytes", path, l.cfg.MaxFileBytes)
		}
		total += info.Size()
		if total > l.cfg.MaxTotalBytes {
			return "", fmt.Errorf("skill content exceeds %d bytes", l.cfg.MaxTotalBytes)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		builder.WriteString("\n\n[External skill: ")
		builder.WriteString(filepath.ToSlash(path))
		builder.WriteString("]\n")
		builder.WriteString(content)
	}
	return strings.TrimSpace(builder.String()), nil
}
