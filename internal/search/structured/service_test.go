package structured

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	searchcontract "github.com/soleapi/forgebot/internal/search"
)

func TestServiceBuildSearchReloadAndFailureRetention(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "guide/auth.mdx", "# Authentication\n\nUse an API key.\n")
	service, err := New(context.Background(), testOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	status := service.Status()
	if status.State != searchcontract.StateReady || status.Documents != 1 || status.Sections != 1 {
		t.Fatalf("status = %+v", status)
	}
	response, err := service.Search(context.Background(), searchcontract.Query{Text: "authentication"})
	if err != nil || len(response.Results) != 1 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	writeDoc(t, root, "guide/retry.md", "# Retry\n\nUse exponential backoff.\n")
	result, err := service.Reload(context.Background())
	if err != nil || result.Documents != 2 {
		t.Fatalf("reload=%+v err=%v", result, err)
	}
	service.opts.Root = filepath.Join(root, "missing")
	if _, err := service.Reload(context.Background()); err == nil {
		t.Fatal("failed reload succeeded")
	}
	response, err = service.Search(context.Background(), searchcontract.Query{Text: "retry"})
	if err != nil || len(response.Results) != 1 {
		t.Fatalf("old index unavailable after failed reload: response=%+v err=%v", response, err)
	}
	if service.Status().State != searchcontract.StateReady || service.Status().LastError == "" {
		t.Fatalf("failed reload status = %+v", service.Status())
	}
}

func TestServiceWarningsLimitsAndEmptyIndex(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "bad.md", "---\ntitle: [bad\n---\n")
	service, err := New(context.Background(), testOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	if status := service.Status(); status.State != searchcontract.StateReadyWithWarnings || status.WarningCount != 1 || status.Documents != 0 {
		t.Fatalf("status = %+v", status)
	}
	if response, err := service.Search(context.Background(), searchcontract.Query{Text: "anything"}); err != nil || len(response.Results) != 0 {
		t.Fatalf("empty index response=%+v err=%v", response, err)
	}

	largeRoot := t.TempDir()
	writeDoc(t, largeRoot, "large.md", "# "+string(make([]byte, 101)))
	opts := testOptions(largeRoot)
	opts.MaxFileBytes = 10
	limited, err := New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if limited.Status().WarningCount != 1 {
		t.Fatalf("limited status = %+v", limited.Status())
	}
}

func TestServiceHonorsCancellationAndUnavailableRoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(ctx, testOptions(t.TempDir()))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled build error = %v", err)
	}
	opts := testOptions(filepath.Join(t.TempDir(), "missing"))
	if _, err := New(context.Background(), opts); err == nil {
		t.Fatal("missing root accepted")
	}
}

func TestServiceRejectsInvalidOptions(t *testing.T) {
	opts := testOptions(t.TempDir())
	opts.MaxSectionChars = 0
	if _, err := New(context.Background(), opts); err == nil {
		t.Fatal("invalid resource limits accepted")
	}
}

func TestConcurrentSearchAndReload(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "index.md", "# Authentication\n\nAPI key authentication.\n")
	service, err := New(context.Background(), testOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 100; iteration++ {
				response, searchErr := service.Search(context.Background(), searchcontract.Query{Text: "authentication"})
				if searchErr != nil || len(response.Results) != 1 {
					t.Errorf("search response=%+v err=%v", response, searchErr)
					return
				}
			}
		}()
	}
	for iteration := 0; iteration < 20; iteration++ {
		if _, err := service.Reload(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	wait.Wait()
}

func testOptions(root string) Options {
	return Options{Root: root, Extensions: []string{".md", ".mdx"}, DefaultResults: 5, MaxResults: 8, MinMatchRatio: .35, QueryTimeout: time.Second, BuildTimeout: time.Second, MaxSectionChars: 8000, MaxFileBytes: 1 << 20, MaxTotalBytes: 10 << 20, MaxFiles: 100}
}

func writeDoc(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
