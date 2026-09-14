package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// StdioConfig describes the executable and resource limits for a local MCP process.
type StdioConfig struct {
	Command          string
	Args             []string
	WorkingDirectory string
	Environment      map[string]string
	Timeout          time.Duration
	MaxResponseBytes int64
}

type stdioTransport struct {
	cmd              *exec.Cmd
	stdin            io.WriteCloser
	scanner          *bufio.Scanner
	maxResponseBytes int64
	callMu           sync.Mutex
	mu               sync.Mutex
	closed           bool
	done             chan struct{}
	waitErr          error
}

// NewStdioTransport starts a JSON-RPC-over-stdio MCP subprocess.
func NewStdioTransport(cfg StdioConfig) (Transport, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, errors.New("MCP stdio command is required")
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = 1 << 20
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	if cfg.WorkingDirectory != "" {
		cmd.Dir = cfg.WorkingDirectory
	}
	environment := os.Environ()
	for key, value := range cfg.Environment {
		if strings.TrimSpace(key) == "" || strings.ContainsRune(key, '=') {
			return nil, fmt.Errorf("invalid MCP stdio environment name %q", key)
		}
		environment = append(environment, key+"="+value)
	}
	cmd.Env = environment
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open MCP stdio stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open MCP stdio stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open MCP stdio stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start MCP stdio process: %w", err)
	}
	transport := &stdioTransport{cmd: cmd, stdin: stdin, maxResponseBytes: cfg.MaxResponseBytes, done: make(chan struct{})}
	transport.scanner = bufio.NewScanner(stdout)
	transport.scanner.Buffer(make([]byte, 4096), int(cfg.MaxResponseBytes+1))
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go func() {
		err := cmd.Wait()
		transport.mu.Lock()
		transport.waitErr = err
		close(transport.done)
		transport.mu.Unlock()
	}()
	return transport, nil
}

func (t *stdioTransport) Call(ctx context.Context, body []byte) ([]byte, error) {
	t.callMu.Lock()
	defer t.callMu.Unlock()
	if err := t.write(ctx, body); err != nil {
		return nil, err
	}
	for {
		if err := t.scan(ctx); err != nil {
			return nil, err
		}
		line := bytes.TrimSpace(t.scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var message struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(line, &message); err != nil {
			return nil, fmt.Errorf("invalid MCP stdio JSON: %w", err)
		}
		if len(message.ID) == 0 || string(message.ID) == "null" {
			continue
		}
		return append([]byte(nil), line...), nil
	}
}

func (t *stdioTransport) Notify(ctx context.Context, body []byte) error {
	t.callMu.Lock()
	defer t.callMu.Unlock()
	return t.write(ctx, body)
}

func (t *stdioTransport) write(ctx context.Context, body []byte) error {
	t.mu.Lock()
	closed := t.closed
	waitErr := t.waitErr
	t.mu.Unlock()
	if closed {
		return errors.New("MCP stdio transport is closed")
	}
	if waitErr != nil {
		return fmt.Errorf("MCP stdio process exited: %w", waitErr)
	}
	data := append(append([]byte(nil), body...), '\n')
	result := make(chan error, 1)
	go func() { _, err := t.stdin.Write(data); result <- err }()
	select {
	case err := <-result:
		if err != nil {
			return fmt.Errorf("write MCP stdio request: %w", err)
		}
		return nil
	case <-ctx.Done():
		t.terminate()
		return ctx.Err()
	case <-t.done:
		return errors.New("MCP stdio process exited")
	}
}

func (t *stdioTransport) scan(ctx context.Context) error {
	result := make(chan bool, 1)
	go func() { result <- t.scanner.Scan() }()
	select {
	case ok := <-result:
		if !ok {
			if err := t.scanner.Err(); err != nil {
				return fmt.Errorf("read MCP stdio response: %w", err)
			}
			return errors.New("MCP stdio process closed stdout")
		}
		if int64(len(t.scanner.Bytes())) > t.maxResponseBytes {
			return fmt.Errorf("MCP stdio response exceeds %d bytes", t.maxResponseBytes)
		}
		return nil
	case <-ctx.Done():
		t.terminate()
		return ctx.Err()
	case <-t.done:
		return errors.New("MCP stdio process exited")
	}
}

func (t *stdioTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()
	t.terminate()
	select {
	case <-t.done:
	case <-time.After(2 * time.Second):
		return errors.New("timeout waiting for MCP stdio process")
	}
	return nil
}

func (t *stdioTransport) terminate() {
	_ = t.stdin.Close()
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
}

var _ Transport = (*stdioTransport)(nil)
