package runtime

import (
	"context"
	"slices"

	"github.com/soleapi/forgebot/internal/platform"
	"github.com/soleapi/forgebot/internal/secret"
)

type candidateSecretReader struct {
	secret.Reader
	ref, value string
}

func (r candidateSecretReader) Get(ctx context.Context, ref string) (string, error) {
	if ref == r.ref {
		return r.value, nil
	}
	if r.Reader == nil {
		return "", secret.ErrNotFound
	}
	return r.Reader.Get(ctx, ref)
}

func connectorUsesSecret(current *generation, ref string) bool {
	for _, connector := range current.snapshot.Runtime.Platforms.Connectors {
		if !connector.Enabled {
			continue
		}
		registration, exists := current.modules.ConnectorProvider(connector.Type)
		if !exists {
			continue
		}
		provider, ok := registration.Provider.(platform.CredentialProvider)
		if !ok {
			continue
		}
		refs, _ := provider.CredentialRefs(registration.ModuleOptions, connector.Options)
		if slices.Contains(refs, ref) {
			return true
		}
	}
	return false
}
