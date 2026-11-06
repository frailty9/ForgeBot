package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func backups(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	base := filepath.Base(path)
	for _, entry := range entries {
		if entry.Name() != base {
			names = append(names, entry.Name())
		}
	}
	return names
}

// 写满大小上限时 lumberjack 先关闭再改名；在 Windows 上同样要能成功轮转。
func TestSizeRotationRenamesOpenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "forgebot.log")
	file, err := newRotatingFile(path, 1, 0, 0, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	chunk := []byte(strings.Repeat("x", 1023) + "\n")
	for i := 0; i < 1200; i++ {
		if _, err := file.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	names := backups(t, path)
	if len(names) != 1 || !strings.HasPrefix(names[0], "forgebot-") || !strings.HasSuffix(names[0], ".log") {
		t.Fatalf("expected one size backup, got %v", names)
	}
	if latest, ok := newestBackup(path); !ok || time.Since(latest) > time.Minute || time.Until(latest) > time.Minute {
		t.Fatalf("backup name not parsed with lumberjack's format: %v %v", latest, ok)
	}
}

// 未写满大小时，到达最长写入天数也要轮转；到期前和刚轮转后都不重复轮转。
func TestPeriodRotationUsesConfiguredDays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forgebot.log")
	start := time.Now()
	file, err := newRotatingFile(path, 10, 7, 60, false, start)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write([]byte("time=" + start.Format(time.RFC3339) + " level=INFO msg=first\n")); err != nil {
		t.Fatal(err)
	}
	if err := file.check(start.Add(6 * 24 * time.Hour)); err != nil || len(backups(t, path)) != 0 {
		t.Fatalf("rotated before the period: %v %v", err, backups(t, path))
	}
	due := start.Add(7*24*time.Hour + time.Minute)
	if err := file.check(due); err != nil || len(backups(t, path)) != 1 {
		t.Fatalf("period rotation missing: %v %v", err, backups(t, path))
	}
	if err := file.check(due.Add(time.Minute)); err != nil || len(backups(t, path)) != 1 {
		t.Fatalf("rotated twice in one period: %v %v", err, backups(t, path))
	}
}

// 当前文件为空或不存在时不轮转，避免生成空备份。
func TestPeriodRotationSkipsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forgebot.log")
	file, err := newRotatingFile(path, 10, 7, 60, false, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := file.check(time.Now()); err != nil || len(backups(t, path)) != 0 {
		t.Fatalf("missing file rotated: %v %v", err, backups(t, path))
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := file.check(time.Now()); err != nil || len(backups(t, path)) != 0 {
		t.Fatalf("empty file rotated: %v %v", err, backups(t, path))
	}
}

// 重启后从已有文件第一条日志推算开始时间（text 与 json 两种格式），
// 无法解析时退回最新备份时间，再不行则返回零值以便尽快轮转。
func TestSegmentStartSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "forgebot.log")
	now := time.Now()
	old := now.Add(-5 * 24 * time.Hour).UTC().Truncate(time.Millisecond)
	for _, line := range []string{
		"time=" + old.Format(time.RFC3339Nano) + " level=INFO msg=first\n",
		`{"time":"` + old.Format(time.RFC3339Nano) + `","level":"INFO","msg":"first"}` + "\n",
	} {
		if err := os.WriteFile(path, []byte(line+"later line\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := segmentStart(path, now); !got.Equal(old) {
			t.Fatalf("start from %q = %v, want %v", line, got, old)
		}
	}
	if err := os.WriteFile(path, []byte("not a log line\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := segmentStart(path, now); !got.IsZero() {
		t.Fatalf("unparseable file should rotate soon, got %v", got)
	}
	backupTime := now.Add(-2 * 24 * time.Hour).UTC().Truncate(time.Millisecond)
	backup := filepath.Join(dir, "forgebot-"+backupTime.Format(backupTimeFormat)+".log.gz")
	if err := os.WriteFile(backup, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := segmentStart(path, now); !got.Equal(backupTime) {
		t.Fatalf("fallback to newest backup = %v, want %v", got, backupTime)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := segmentStart(path, now); !got.Equal(now) {
		t.Fatalf("missing file should start now, got %v", got)
	}
}

// 轮转后清理早于 max_age_days 的备份；清理在后台进行，因此轮询等待。
func TestRotationRemovesBackupsOlderThanMaxAge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "forgebot.log")
	stale := filepath.Join(dir, "forgebot-"+time.Now().Add(-61*24*time.Hour).UTC().Format(backupTimeFormat)+".log")
	recent := filepath.Join(dir, "forgebot-"+time.Now().Add(-10*24*time.Hour).UTC().Format(backupTimeFormat)+".log")
	for _, name := range []string{stale, recent} {
		if err := os.WriteFile(name, []byte("old\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	file, err := newRotatingFile(path, 10, 7, 60, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("line\n")); err != nil {
		t.Fatal(err)
	}
	if err := file.Rotate(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, staleErr := os.Stat(stale)
		if os.IsNotExist(staleErr) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backup older than max_age_days was not removed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatal("recent backup removed", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// Close 停止后台周期检查后才返回，之后不会再轮转。
func TestCloseStopsPeriodChecks(t *testing.T) {
	file, err := newRotatingFile(filepath.Join(t.TempDir(), "forgebot.log"), 10, 7, 60, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	file.run()
	if err := file.Close(); err != nil || file.stop != nil {
		t.Fatalf("close did not stop checks: %v", err)
	}
}
