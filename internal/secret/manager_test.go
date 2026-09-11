package secret

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestManagerActivatesOnlyValidatedSecret(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "secrets.json"), testKey())
	if err != nil {
		t.Fatal(err)
	}
	validationErr := errors.New("provider rejected key")
	manager, err := NewManager(store, func(_ context.Context, _ string, value string) error {
		if value == "bad" {
			return validationErr
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	status, err := manager.Replace(context.Background(), "llm/default", "old")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Configured || status.Version != 1 {
		t.Fatalf("status = %+v", status)
	}

	if _, err := manager.Replace(context.Background(), "llm/default", "bad"); !errors.Is(err, validationErr) {
		t.Fatalf("replace error = %v", err)
	}
	value, err := store.Get(context.Background(), "llm/default")
	if err != nil {
		t.Fatal(err)
	}
	if value != "old" {
		t.Fatalf("failed validation replaced active secret with %q", value)
	}
	status, err = manager.Status(context.Background(), "llm/default")
	if err != nil {
		t.Fatal(err)
	}
	if status.Version != 1 {
		t.Fatalf("failed validation advanced active version to %d", status.Version)
	}
}

func TestConcurrentStagesProduceMonotonicCandidateVersions(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "secrets.json"), testKey())
	if err != nil {
		t.Fatal(err)
	}
	const workers = 16
	versions := make(chan uint64, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			candidate, stageErr := store.Stage(context.Background(), "llm/default", "key")
			if stageErr != nil {
				errs <- stageErr
				return
			}
			versions <- candidate.Version
		}()
	}
	seen := make(map[uint64]bool, workers)
	for i := 0; i < workers; i++ {
		select {
		case err := <-errs:
			t.Fatal(err)
		case version := <-versions:
			if seen[version] {
				t.Fatalf("duplicate version %d", version)
			}
			seen[version] = true
		}
	}
	for version := uint64(1); version <= workers; version++ {
		if !seen[version] {
			t.Fatalf("missing version %d in %v", version, seen)
		}
	}
}
