// Package agent defines host capabilities shared with trusted native modules.
package agent

import (
	"context"

	"github.com/soleapi/forgebot/internal/capability"
	"github.com/soleapi/forgebot/internal/llm"
)

var CompleterKey = capability.NewKey[Completer]("agent.completer.v1")

type Completer interface {
	Complete(context.Context, llm.Request) (llm.Response, error)
	Model() string
}

type completer struct {
	client llm.Client
	model  string
}

func NewCompleter(client llm.Client, model string) Completer {
	if client == nil {
		return nil
	}
	return &completer{client: client, model: model}
}

func (c *completer) Complete(ctx context.Context, request llm.Request) (llm.Response, error) {
	if request.Model == "" {
		request.Model = c.model
	}
	return c.client.Complete(ctx, request)
}

func (c *completer) Model() string { return c.model }
