package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/platform"
)

// 生命周期测试也经过模块注册、Provider 构建和真实 ConnectorSet，不注入宿主旁路。
type lifecycleFactory struct {
	build func(platform.ConnectorBuildRequest) (platform.Adapter, error)
}

func (lifecycleFactory) Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: "native.lifecycle_test", DefaultEnabled: true,
		Registrations: []modules.RegistrationKind{modules.RegistrationConnectorProviders}}
}
func (lifecycleFactory) ValidateOptions(json.RawMessage) error      { return nil }
func (lifecycleFactory) PublicOptions(json.RawMessage) (any, error) { return nil, nil }
func (f lifecycleFactory) Build(context.Context, json.RawMessage) (modules.Instance, error) {
	return lifecycleModule{provider: lifecycleProvider{build: f.build}}, nil
}

type lifecycleModule struct{ provider lifecycleProvider }

func (lifecycleModule) Registrations() hooks.Registrations { return hooks.Registrations{} }
func (lifecycleModule) Close(context.Context) error        { return nil }
func (m lifecycleModule) ExtendedRegistrations() modules.ExtendedRegistrations {
	return modules.ExtendedRegistrations{ConnectorProviders: []platform.ConnectorProvider{m.provider}}
}

type lifecycleProvider struct {
	build func(platform.ConnectorBuildRequest) (platform.Adapter, error)
}

func (lifecycleProvider) Type() string { return "echo" }
func (lifecycleProvider) ValidateOptions(json.RawMessage, json.RawMessage, []platform.ConnectorBinding) error {
	return nil
}
func (lifecycleProvider) PublicOptions(json.RawMessage, json.RawMessage) (any, error) {
	return nil, nil
}
func (p lifecycleProvider) Build(_ context.Context, request platform.ConnectorBuildRequest) (platform.Adapter, error) {
	return p.build(request)
}

func connectorRegistry(t *testing.T, build func(platform.ConnectorBuildRequest) (platform.Adapter, error)) *modules.Registry {
	t.Helper()
	return moduleRegistry(t, lifecycleFactory{build: build})
}
