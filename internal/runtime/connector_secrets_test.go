package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/hooks"
	"github.com/soleapi/forgebot/internal/modules"
	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/secret"
)

type credentialFactory struct{ providers *credentialProvider }

func (credentialFactory) Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: "native.credentials_test", DefaultEnabled: true, Registrations: []modules.RegistrationKind{modules.RegistrationConnectorProviders}}
}
func (credentialFactory) ValidateOptions(json.RawMessage) error      { return nil }
func (credentialFactory) PublicOptions(json.RawMessage) (any, error) { return nil, nil }
func (f credentialFactory) Build(context.Context, json.RawMessage) (modules.Instance, error) {
	return credentialModule{f.providers}, nil
}

type credentialModule struct{ provider *credentialProvider }

func (credentialModule) Registrations() hooks.Registrations { return hooks.Registrations{} }
func (credentialModule) Close(context.Context) error        { return nil }
func (m credentialModule) ExtendedRegistrations() modules.ExtendedRegistrations {
	return modules.ExtendedRegistrations{ConnectorProviders: []platform.ConnectorProvider{m.provider}}
}

type credentialProvider struct {
	values   []string
	adapters []*fakeConnectorAdapter
	leak     bool
}

func (*credentialProvider) Type() string { return "credential_test" }
func (*credentialProvider) ValidateOptions(json.RawMessage, json.RawMessage, []platform.ConnectorBinding) error {
	return nil
}
func (*credentialProvider) PublicOptions(json.RawMessage, json.RawMessage) (any, error) {
	return map[string]bool{"secret_configured": true}, nil
}
func (*credentialProvider) CredentialRefs(json.RawMessage, json.RawMessage) ([]string, error) {
	return []string{"connector/key"}, nil
}
func (p *credentialProvider) Build(_ context.Context, r platform.ConnectorBuildRequest) (platform.Adapter, error) {
	if len(r.Credentials) != 1 {
		p.leak = true
	}
	value := r.Credentials["connector/key"]
	p.values = append(p.values, value)
	a := newFakeConnectorAdapter()
	a.id = r.ConnectorID
	if value == "invalid" {
		a.failStart = errors.New("invalid token")
	}
	p.adapters = append(p.adapters, a)
	return a, nil
}

func TestConnectorCredentialsRotateThroughCandidateAndRollbackBeforeActivation(t *testing.T) {
	store := configuredSecrets(t, "answer-key")
	candidate, err := store.Stage(context.Background(), "connector/key", "old")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Activate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	provider := &credentialProvider{}
	snapshot := testSnapshot()
	snapshot.Runtime.Platforms = config.PlatformConfig{Connectors: []config.ConnectorConfig{{ID: "qq", Type: "credential_test", Enabled: true}}, Groups: []config.GroupBindingConfig{{ID: "local", ConnectorID: "qq", Target: "group"}}}
	manager, err := NewManager(context.Background(), snapshot, Builder{Modules: moduleRegistry(t, credentialFactory{provider}), Secrets: store})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	writer, err := secret.NewManagedWriter(store, manager.ReplaceSecret, manager.DeleteSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Replace(context.Background(), "connector/key", "invalid"); err == nil {
		t.Fatal("bad credential activated")
	}
	if value, _ := store.Get(context.Background(), "connector/key"); value != "old" {
		t.Fatal("old credential replaced before candidate starts")
	}
	if !provider.adapters[0].running || provider.adapters[0].starts != 2 {
		t.Fatal("old connector was not restored")
	}
	if _, err := writer.Replace(context.Background(), "connector/key", "new"); err != nil {
		t.Fatal(err)
	}
	if value, _ := store.Get(context.Background(), "connector/key"); value != "new" {
		t.Fatal(value)
	}
	if got := strings.Join(provider.values, ","); got != "old,invalid,new" || provider.leak {
		t.Fatal("credential scoping/candidate failure", got, provider.leak)
	}
	if err := writer.Delete(context.Background(), "connector/key"); err == nil {
		t.Fatal("active required connector credential deleted")
	}
	if value, _ := store.Get(context.Background(), "connector/key"); value != "new" {
		t.Fatal("failed delete changed store")
	}
	if err := manager.ReplaceSecret(context.Background(), "connector/key", "next", func() error { return errors.New("disk full") }); err == nil {
		t.Fatal("activation failure accepted")
	}
	if !provider.adapters[2].running {
		t.Fatal("old connector not restored on persistence failure")
	}
}
