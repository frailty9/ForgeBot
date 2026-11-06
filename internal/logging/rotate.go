package logging

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// backupTimeFormat 必须与 lumberjack 生成备份名的格式一致（UTC）。
const backupTimeFormat = "2006-01-02T15-04-05.000"

// periodCheckInterval 决定按周期轮转的最大误差，对按天计的周期可以忽略。
const periodCheckInterval = time.Hour

// rotatingFile 在 lumberjack 的按大小轮转之上补充“最长写入天数”。
// lumberjack 先关闭文件再改名，因此 Windows 上同样可以轮转。
type rotatingFile struct {
	*lumberjack.Logger
	period time.Duration
	mu     sync.Mutex
	// start 是当前文件开始写入的时间；轮转后由备份名中的时间刷新。
	start time.Time
	stop  chan struct{}
	done  chan struct{}
}

func newRotatingFile(path string, maxSizeMB, rotateAfterDays, maxAgeDays int, compress bool, now time.Time) (*rotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	r := &rotatingFile{
		Logger: &lumberjack.Logger{Filename: path, MaxSize: maxSizeMB, MaxAge: maxAgeDays, Compress: compress},
		period: time.Duration(rotateAfterDays) * 24 * time.Hour,
		// 在 lumberjack 打开文件之前读取，避免与写入竞争。
		start: segmentStart(path, now),
	}
	return r, nil
}

// check 在当前文件写满周期时轮转。空文件或不存在的文件不轮转，避免产生空备份。
// 当前文件的开始时间取内存记录与最新备份时间中较晚者，这样按大小轮转后
// 不需要回调也能得到正确的起点。
func (r *rotatingFile) check(now time.Time) error {
	if r.period <= 0 {
		return nil
	}
	info, err := os.Stat(r.Filename)
	if os.IsNotExist(err) || (err == nil && info.Size() == 0) {
		return nil
	}
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if latest, ok := newestBackup(r.Filename); ok && latest.After(r.start) {
		r.start = latest
	}
	if now.Sub(r.start) < r.period {
		return nil
	}
	if err := r.Rotate(); err != nil {
		return err
	}
	r.start = now
	return nil
}

func (r *rotatingFile) run() {
	r.stop, r.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(r.done)
		ticker := time.NewTicker(periodCheckInterval)
		defer ticker.Stop()
		for {
			r.report(r.check(time.Now()))
			select {
			case <-r.stop:
				return
			case <-ticker.C:
			}
		}
	}()
}

// report 把轮转失败写到 stderr：此时日志文件本身可能不可用，不能再写回它。
func (r *rotatingFile) report(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "forgebot: log rotation failed: %v\n", err)
	}
}

// Close 先停止周期检查，再关闭文件，保证关闭后不会再轮转。
func (r *rotatingFile) Close() error {
	if r.stop != nil {
		close(r.stop)
		<-r.done
		r.stop = nil
	}
	return r.Logger.Close()
}

// newestBackup 按 lumberjack 的命名规则（prefix-时间ext[.gz]）找最新备份的轮转时间。
func newestBackup(path string) (time.Time, bool) {
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return time.Time{}, false
	}
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	prefix := base[:len(base)-len(ext)] + "-"
	var latest time.Time
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".gz")
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ext) {
			continue
		}
		stamp := name[len(prefix) : len(name)-len(ext)]
		if t, err := time.Parse(backupTimeFormat, stamp); err == nil && t.After(latest) {
			latest = t
		}
	}
	return latest, !latest.IsZero()
}

// segmentStart 推算重启前已有文件的开始时间：优先取第一条日志的时间，
// 其次取最新备份的轮转时间。都无法确定时返回零值，首次检查即轮转，
// 宁可多切一次也不让周期被无限拉长。
func segmentStart(path string, now time.Time) time.Time {
	file, err := os.Open(path)
	if err != nil {
		return now
	}
	defer file.Close()
	// 只读开头 64 KiB；时间字段位于行首，过长的首行不会被整行读入内存。
	line, err := bufio.NewReader(io.LimitReader(file, 64<<10)).ReadString('\n')
	if strings.TrimSpace(line) == "" && err != nil {
		return now
	}
	if t, ok := lineTime(line); ok {
		return t
	}
	if t, ok := newestBackup(path); ok {
		return t
	}
	return time.Time{}
}

// lineTime 解析 slog text（time=...）或 json（"time":...）格式日志行的时间。
func lineTime(line string) (time.Time, bool) {
	value := ""
	if strings.HasPrefix(strings.TrimSpace(line), "{") {
		var record struct {
			Time string `json:"time"`
		}
		if json.Unmarshal([]byte(line), &record) == nil {
			value = record.Time
		}
	} else if index := strings.Index(line, "time="); index >= 0 {
		value, _, _ = strings.Cut(line[index+len("time="):], " ")
	}
	t, err := time.Parse(time.RFC3339, value)
	return t, err == nil
}
