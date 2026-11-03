package tooling

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type MultiExecutor struct{ executors []Executor }

func NewMultiExecutor(executors ...Executor) Executor {
	filtered := make([]Executor, 0, len(executors))
	for _, executor := range executors {
		if executor != nil {
			filtered = append(filtered, executor)
		}
	}
	return &multiExecutor{executors: filtered}
}

// NewStrictMultiExecutor rejects duplicate or empty tool names across all
// sources so an MCP server cannot silently shadow a native module tool.
func NewStrictMultiExecutor(executors ...Executor) (Executor, error) {
	filtered := make([]Executor, 0, len(executors))
	seen := make(map[string]struct{})
	for _, executor := range executors {
		if executor == nil {
			continue
		}
		for _, definition := range executor.Definitions() {
			name := strings.TrimSpace(definition.Name)
			if name == "" {
				return nil, errors.New("tool definition has empty name")
			}
			if _, exists := seen[name]; exists {
				return nil, fmt.Errorf("tool %q is registered by more than one provider", name)
			}
			seen[name] = struct{}{}
		}
		filtered = append(filtered, executor)
	}
	return &multiExecutor{executors: filtered}, nil
}

type multiExecutor struct{ executors []Executor }

func (m *multiExecutor) Definitions() []Definition {
	if m == nil {
		return nil
	}
	seen := map[string]struct{}{}
	result := make([]Definition, 0)
	for _, executor := range m.executors {
		for _, definition := range executor.Definitions() {
			if definition.Name == "" {
				continue
			}
			if _, ok := seen[definition.Name]; ok {
				continue
			}
			seen[definition.Name] = struct{}{}
			result = append(result, definition)
		}
	}
	return result
}

func (m *multiExecutor) Execute(ctx context.Context, call Call) Result {
	if m == nil {
		return Result{Code: "tool_not_allowed"}
	}
	for _, executor := range m.executors {
		for _, definition := range executor.Definitions() {
			if definition.Name == call.Name {
				return Normalize(executor.Execute(ctx, call))
			}
		}
	}
	return Result{Code: "tool_not_allowed"}
}

func (m *multiExecutor) Close() error {
	if m == nil {
		return nil
	}
	var errs []error
	for _, executor := range m.executors {
		if closer, ok := executor.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

type Registry struct {
	tools       map[string]Tool
	definitions []Definition
}

func NewRegistry(tools ...Tool) *Registry {
	registry := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		definition := tool.Definition()
		if definition.Name == "" {
			continue
		}
		if _, exists := registry.tools[definition.Name]; exists {
			continue
		}
		registry.tools[definition.Name] = tool
		registry.definitions = append(registry.definitions, definition)
	}
	return registry
}

func (r *Registry) Definitions() []Definition {
	if r == nil {
		return nil
	}
	return append([]Definition(nil), r.definitions...)
}

func (r *Registry) Execute(ctx context.Context, call Call) Result {
	if r == nil {
		return Result{Code: "tool_not_allowed"}
	}
	tool, exists := r.tools[call.Name]
	if !exists {
		return Result{Code: "tool_not_allowed"}
	}
	return Normalize(tool.Execute(ctx, call))
}
