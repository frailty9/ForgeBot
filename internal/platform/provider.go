package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
)

var connectorTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// ConnectorBinding is the protocol-neutral input supplied to one connector
// instance. The runtime filters bindings before handing them to a provider.
type ConnectorBinding struct {
	ConversationID string
	Target         string
	PromptSuffix   string
}

// ConnectorBuildRequest contains only one connector instance's public
// metadata, its private options, and its already-filtered bindings.
type ConnectorBuildRequest struct {
	ConnectorID      string
	PromptSuffix     string
	ModuleOptions    json.RawMessage
	ConnectorOptions json.RawMessage
	Bindings         []ConnectorBinding
	Logger           *slog.Logger
}

// ConnectorProvider creates adapters for one stable connector type. Providers
// must not start listeners, goroutines, or network requests from Build; the
// ConnectorSet owns activation through Adapter.Start.
type ConnectorProvider interface {
	Type() string
	ValidateOptions(moduleOptions, connectorOptions json.RawMessage, bindings []ConnectorBinding) error
	PublicOptions(moduleOptions, connectorOptions json.RawMessage) (any, error)
	Build(context.Context, ConnectorBuildRequest) (Adapter, error)
}

// ValidConnectorType reports whether a connector type is a stable provider
// lookup key. It deliberately has no dependency on a concrete protocol.
func ValidConnectorType(value string) bool { return connectorTypePattern.MatchString(value) }
