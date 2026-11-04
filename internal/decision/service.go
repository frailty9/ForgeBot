package decision

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/soleapi/forgebot/internal/llm"
	"github.com/soleapi/forgebot/internal/operation"
)

type Provider interface {
	Decide(context.Context, ProviderRequest) (Answer, error)
}
type Decider interface {
	Decide(context.Context, Request) (Result, error)
}
type Attempt struct {
	Source  string             `json:"source"`
	Failure *operation.Failure `json:"failure,omitempty"`
}
type Result struct {
	Answer   Answer         `json:"answer"`
	Source   string         `json:"source"`
	Attempts []Attempt      `json:"attempts,omitempty"`
	Reason   operation.Code `json:"reason,omitempty"`
}

type Options struct {
	DM            Provider
	LLM           Provider
	DMTimeout     time.Duration
	LLMTimeout    time.Duration
	MaxConcurrent int
	Logger        *slog.Logger
}

type Service struct {
	tasks   map[string]Task
	options Options
	slots   chan struct{}
}

func New(tasks []Task, options Options) (*Service, error) {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.DMTimeout == 0 {
		options.DMTimeout = 3 * time.Second
	}
	if options.LLMTimeout == 0 {
		options.LLMTimeout = 8 * time.Second
	}
	if options.MaxConcurrent == 0 {
		options.MaxConcurrent = 16
	}
	if options.DMTimeout <= 0 || options.LLMTimeout <= 0 || options.MaxConcurrent < 1 || options.MaxConcurrent > 256 {
		return nil, fmt.Errorf("invalid decision service limits")
	}
	s := &Service{tasks: map[string]Task{}, options: options, slots: make(chan struct{}, options.MaxConcurrent)}
	for _, task := range append(append([]Task(nil), tasks...), booleanTask()) {
		if err := task.Validate(); err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%s/%d", task.ID, task.Version)
		if _, exists := s.tasks[key]; exists {
			return nil, fmt.Errorf("decision task %q registered twice", key)
		}
		// 注册后独立持有题型，消费者不能改写宿主的约束。
		data, _ := json.Marshal(task)
		var copy Task
		_ = json.Unmarshal(data, &copy)
		s.tasks[key] = copy
	}
	return s, nil
}

type budgetKey struct{}
type budget struct {
	mu        sync.Mutex
	remaining int
	cache     map[[32]byte]Result
}

// WithBudget 在聊天 run 或群事件入口调用；同一 context 的多个判断共享预算。
func WithBudget(ctx context.Context, max int) context.Context {
	if _, exists := ctx.Value(budgetKey{}).(*budget); exists {
		return ctx
	}
	return context.WithValue(ctx, budgetKey{}, &budget{remaining: max, cache: map[[32]byte]Result{}})
}

func (s *Service) Decide(ctx context.Context, request Request) (result Result, decisionErr error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	request.State = bytes.TrimSpace(request.State)
	task, ok := s.tasks[fmt.Sprintf("%s/%d", request.TaskID, request.Version)]
	if !ok {
		return Result{}, operation.New(operation.Unsupported, "decision task is not registered")
	}
	if len(request.State) == 0 || len(request.State) > 16384 || !json.Valid(request.State) {
		return Result{}, operation.New(operation.Invalid, "decision state must contain at most 16384 bytes of JSON")
	}
	if len(request.Media) > 4 {
		return Result{}, operation.New(operation.Invalid, "decision media count exceeds limit")
	}
	for _, media := range request.Media {
		if media.Type != "image" || len(media.Data) > 2<<20 || len(media.MediaURL) > 2048 || (media.Data == "" && media.MediaURL == "") {
			return Result{}, operation.New(operation.Invalid, "invalid decision image")
		}
	}
	if request.State[0] != '{' && request.State[0] != '[' && request.State[0] != '"' {
		return Result{}, operation.New(operation.Invalid, "decision state must be object, array or string")
	}
	ctx = WithBudget(ctx, 2)
	b := ctx.Value(budgetKey{}).(*budget)
	encoded, _ := json.Marshal(request)
	hash := sha256.Sum256(encoded)
	b.mu.Lock()
	if cached, ok := b.cache[hash]; ok {
		b.mu.Unlock()
		return cloneResult(cached), nil
	}
	b.mu.Unlock()
	// 在调用前只选一个模型；DM 故障绝不转调 LLM。
	provider, source, timeout := s.options.DM, "dm", s.options.DMTimeout
	if provider == nil {
		provider, source, timeout = s.options.LLM, "llm", s.options.LLMTimeout
	}
	result = Result{Answer: task.Unknown(), Source: source}
	defer func() {
		s.options.Logger.InfoContext(ctx, "model decision", "task_id", request.TaskID,
			"source", source, "answer", result.Answer, "failure", operation.Describe(decisionErr))
	}()
	if provider == nil {
		result.Reason = operation.NotConfigured
		return result, operation.New(result.Reason, "decision model is not configured")
	}
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		return result, ctx.Err()
	}
	defer func() { <-s.slots }()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	b.mu.Lock()
	available := b.remaining > 0
	if available {
		b.remaining--
	}
	b.mu.Unlock()
	if !available {
		result.Reason = operation.BudgetExhausted
		return result, operation.New(result.Reason, "decision model budget exhausted")
	}
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	answer, err := provider.Decide(attemptCtx, ProviderRequest{Task: task, State: append(json.RawMessage(nil), request.State...), Media: append([]llm.ContentPart(nil), request.Media...)})
	if err == nil {
		err = attemptCtx.Err()
	}
	if err == nil {
		err = operation.Wrap("decision.validate", operation.Invalid, operation.NotStarted, task.ValidateAnswer(answer))
	}
	attempt := Attempt{Source: source}
	if err != nil {
		attempt.Failure = operation.Describe(err)
		result.Reason = attempt.Failure.Code
		result.Attempts = []Attempt{attempt}
		return result, operation.Wrap("decision.model", operation.Unknown, operation.NotStarted, err)
	}
	result.Answer = cloneAnswer(answer)
	result.Attempts = []Attempt{attempt}
	b.mu.Lock()
	if len(b.cache) < 8 {
		b.cache[hash] = cloneResult(result)
	}
	b.mu.Unlock()
	return result, nil
}

func cloneResult(result Result) Result {
	result.Answer = cloneAnswer(result.Answer)
	result.Attempts = append([]Attempt(nil), result.Attempts...)
	for i := range result.Attempts {
		if failure := result.Attempts[i].Failure; failure != nil {
			copy := *failure
			result.Attempts[i].Failure = &copy
		}
	}
	return result
}
