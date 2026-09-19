package capability_test

import (
	"testing"

	"github.com/soleapi/forgebot/internal/capability"
)

type service struct{ Name string }

func TestValuesProvideAndTypedResolve(t *testing.T) {
	values := capability.NewValues()
	key := capability.NewKey[*service]("example.service.v1")
	item := &service{Name: "ok"}
	if err := values.Provide(key.ID(), item); err != nil {
		t.Fatal(err)
	}
	resolved, ok := capability.Resolve(values, key)
	if !ok || resolved != item {
		t.Fatalf("resolved = %#v, %v", resolved, ok)
	}
	if values.Has(key.ID()) == false {
		t.Fatal("capability was not recorded")
	}
}

func TestValuesRejectDuplicateInvalidAndNil(t *testing.T) {
	values := capability.NewValues()
	if err := values.Provide("x", struct{}{}); err == nil {
		t.Fatal("invalid id accepted")
	}
	if err := values.Provide("example.service.v1", nil); err == nil {
		t.Fatal("nil value accepted")
	}
	if err := values.Provide("example.service.v1", struct{}{}); err != nil {
		t.Fatal(err)
	}
	if err := values.Provide("example.service.v1", struct{}{}); err == nil {
		t.Fatal("duplicate accepted")
	}
}

func TestResolveTypeMismatchReturnsZero(t *testing.T) {
	values := capability.NewValues()
	if err := values.Provide("example.service.v1", "wrong type"); err != nil {
		t.Fatal(err)
	}
	key := capability.NewKey[*service]("example.service.v1")
	if resolved, ok := capability.Resolve(values, key); ok || resolved != nil {
		t.Fatalf("mismatch resolved = %#v, %v", resolved, ok)
	}
}
