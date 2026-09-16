// Package tooling defines the allow-listed tool boundary used by the bot
// engine. Implementations may be local search tools or external MCP clients.
package tooling

import "context"

type Definition struct {
	Name        string
	Description string
	InputSchema map[string]any
}

type Call struct {
	Name      string
	Arguments string
}

type Result struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Payload string `json:"payload,omitempty"`
}

type Tool interface {
	Definition() Definition
	Execute(context.Context, Call) Result
}

type Executor interface {
	Definitions() []Definition
	Execute(context.Context, Call) Result
}
