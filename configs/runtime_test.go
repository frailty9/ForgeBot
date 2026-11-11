package configs_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/configs"
	"github.com/soleapi/forgebot/internal/config"
)

func TestRuntimeExampleIsUniqueAndListsConfigFields(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "runtime.") && strings.HasSuffix(entry.Name(), ".example.json") && entry.Name() != "runtime.example.json" {
			t.Error("additional runtime example", entry.Name())
		}
	}
	var value map[string]any
	if err := json.Unmarshal(configs.RuntimeExample(), &value); err != nil {
		t.Fatal(err)
	}
	assertFields(t, "runtime", reflect.TypeFor[config.RuntimeConfig](), value)
}

func assertFields(t *testing.T, path string, kind reflect.Type, value any) {
	t.Helper()
	if kind == reflect.TypeFor[json.RawMessage]() {
		return
	}
	if kind.Kind() == reflect.Pointer {
		kind = kind.Elem()
	}
	switch kind.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatal("expected object", path)
		}
		for index := range kind.NumField() {
			field := kind.Field(index)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			child, exists := object[name]
			if !exists {
				t.Error("example missing field", path+"."+name)
				continue
			}
			assertFields(t, path+"."+name, field.Type, child)
		}
	case reflect.Slice:
		if items, ok := value.([]any); ok {
			for _, child := range items {
				assertFields(t, path+"[]", kind.Elem(), child)
			}
		}
	case reflect.Map:
		if object, ok := value.(map[string]any); ok {
			for name, child := range object {
				assertFields(t, path+"."+name, kind.Elem(), child)
			}
		}
	}
}
