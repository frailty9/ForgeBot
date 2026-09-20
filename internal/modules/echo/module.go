// Package echo exposes the in-memory development connector as a native
// connector provider. It is deliberately small but exercises the same
// registration and lifecycle path as network protocols.
package echo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

import (
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/platform/memory"
)

const ID = "native.echo"

// Factory registers the built-in echo provider. The provider has no private
// settings, so both its module and connector options must be empty objects.
type Factory struct{}

func (Factory) Descriptor() modules.Descriptor {
	return modules.Descriptor{
		ID: ID, DefaultEnabled: true,
		Registrations: []modules.RegistrationKind{modules.RegistrationConnectorProviders},
	}
}

func (Factory) ValidateOptions(raw json.RawMessage) error { return validateOptions(raw) }
func (Factory) PublicOptions(raw json.RawMessage) (any, error) {
	if err := validateOptions(raw); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}
func (Factory) Build(context.Context, json.RawMessage) (modules.Instance, error) {
	return instance{}, nil
}

type instance struct{}

func (instance) Registrations() hooks.Registrations { return hooks.Registrations{} }
func (instance) Close(context.Context) error        { return nil }
func (instance) ExtendedRegistrations() modules.ExtendedRegistrations {
	return modules.ExtendedRegistrations{ConnectorProviders: []platform.ConnectorProvider{provider{}}}
}

type provider struct{}

func (provider) Type() string { return "echo" }
func (provider) ValidateOptions(moduleOptions, connectorOptions json.RawMessage, _ []platform.ConnectorBinding) error {
	if err := validateOptions(moduleOptions); err != nil {
		return fmt.Errorf("module options: %w", err)
	}
	if err := validateOptions(connectorOptions); err != nil {
		return fmt.Errorf("connector options: %w", err)
	}
	return nil
}
func (provider) PublicOptions(moduleOptions, connectorOptions json.RawMessage) (any, error) {
	if err := (provider{}).ValidateOptions(moduleOptions, connectorOptions, nil); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}
func (provider) Build(_ context.Context, request platform.ConnectorBuildRequest) (platform.Adapter, error) {
	if err := (provider{}).ValidateOptions(request.ModuleOptions, request.ConnectorOptions, request.Bindings); err != nil {
		return nil, err
	}
	return memory.NewAdapter(request.ConnectorID, request.Logger), nil
}

func validateOptions(raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var options map[string]json.RawMessage
	if err := json.Unmarshal(raw, &options); err != nil {
		return err
	}
	if options == nil {
		return errors.New("options must be an object")
	}
	if len(options) != 0 {
		return errors.New("echo options must be empty")
	}
	return nil
}
