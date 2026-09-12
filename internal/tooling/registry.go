package tooling

import "context"

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
	return tool.Execute(ctx, call)
}
