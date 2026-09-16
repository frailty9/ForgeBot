package skills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoaderLoadsSkillsDeterministicallyWithinLimits(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# Platform\nUse platform tools."), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "SKILL.md"), []byte("# Pricing\nReturn concise prices."), 0644); err != nil {
		t.Fatal(err)
	}
	loader, err := New(Config{Directories: []string{root}, MaxFileBytes: 1024, MaxTotalBytes: 2048, MaxFiles: 4})
	if err != nil {
		t.Fatal(err)
	}
	content, err := loader.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "# Platform") || !strings.Contains(content, "# Pricing") {
		t.Fatalf("content=%q", content)
	}
}
