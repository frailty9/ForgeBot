// Package conversation defines read-only logical conversation metadata shared
// with trusted native modules during generation construction.
package conversation

import "github.com/soleapi/forgebot/internal/capability"

var CatalogKey = capability.NewKey[Catalog]("conversation.catalog.v1")

type Catalog interface {
	Has(string) bool
}

type Set map[string]struct{}

func (s Set) Has(id string) bool {
	_, ok := s[id]
	return ok
}
