package settings

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
)

func TestFailedWriteKeepsCurrentRevisionAndSnapshotIsolation(t *testing.T) {
	path := writeRuntimeFile(t)
	store, err := NewJSONFileStore(path, config.DefaultStatic())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	before, err := store.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before.Runtime.Platforms.Groups[0].ID = "outside-mutation"
	fresh, _ := store.Current(ctx)
	if fresh.Runtime.Platforms.Groups[0].ID == "outside-mutation" {
		t.Fatal("current snapshot aliases store state")
	}
	// 用一个普通文件占据原父目录，使写入在提交前失败，全部操作仅涉及 TempDir。
	dir := filepath.Dir(path)
	if err := os.Rename(dir, dir+"-saved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(dir); _ = os.Rename(dir+"-saved", dir) })
	if err := os.WriteFile(dir, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	next := fresh.Runtime.Clone()
	next.Bot.Name = "must-not-commit"
	if _, err := store.Update(ctx, UpdateRequest{ExpectedVersion: fresh.Version, Runtime: next, Source: "failure-test"}); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	after, _ := store.Current(ctx)
	if after.Version != fresh.Version || after.Runtime.Bot.Name != fresh.Runtime.Bot.Name {
		t.Fatal("failed persistence changed active snapshot")
	}
	if history, err := store.History(ctx, 10); err != nil || len(history) != 1 {
		t.Fatalf("failed write changed history: %v %v", history, err)
	}
}
