// Package capability defines the typed, generation-local capability registry.
// Capabilities are internal host contracts; they are not an external plugin wire
// protocol and never expose the complete runtime object graph.
package capability

import (
	"errors"
	"fmt"
	"maps"
)

type ID string

func (id ID) Valid() bool { return len(id) >= 3 && len(id) <= 128 }

type Values struct{ values map[ID]any }

// View is a read-only snapshot used while constructing a module. Its map is
// private so a consumer cannot register or replace host capabilities.
type View struct{ values map[ID]any }

func NewValues() Values { return Values{values: make(map[ID]any)} }

func (v Values) Clone() Values {
	return Values{values: maps.Clone(v.values)}
}

func (v Values) View() View { return View{values: maps.Clone(v.values)} }

func (v Values) Provide(id ID, value any) error {
	if !id.Valid() {
		return fmt.Errorf("invalid capability id %q", id)
	}
	if value == nil {
		return errors.New("capability value is nil")
	}
	if v.values == nil {
		return errors.New("capability values are not initialized")
	}
	if _, exists := v.values[id]; exists {
		return fmt.Errorf("capability %q is already provided", id)
	}
	v.values[id] = value
	return nil
}

func (v Values) Has(id ID) bool {
	_, ok := v.values[id]
	return ok
}

func (v Values) Get(id ID) (any, bool) {
	value, ok := v.values[id]
	return value, ok
}

func (v View) Has(id ID) bool {
	_, ok := v.values[id]
	return ok
}

func (v View) Get(id ID) (any, bool) {
	value, ok := v.values[id]
	return value, ok
}

type Key[T any] struct{ id ID }

func NewKey[T any](id ID) Key[T] { return Key[T]{id: id} }

func (k Key[T]) ID() ID { return k.id }

type Resolver interface {
	Get(ID) (any, bool)
}

func Resolve[T any](values Resolver, key Key[T]) (T, bool) {
	var zero T
	value, ok := values.Get(key.id)
	if !ok {
		return zero, false
	}
	typed, ok := value.(T)
	if !ok {
		return zero, false
	}
	return typed, true
}
