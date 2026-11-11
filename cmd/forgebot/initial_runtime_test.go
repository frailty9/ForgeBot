package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/soleapi/forgebot/configs"
	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/llm"
)

func TestInitKeepsCompleteExampleAndOnlyOverridesModelOptions(t *testing.T) {
	dir := t.TempDir()
	if err := initializeProject(dir, "my-model", "https://model.example/v1", llm.AnthropicMessages, true, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "runtime.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expected, actual map[string]any
	if err := json.Unmarshal(configs.RuntimeExample(), &expected); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(generated, &actual); err != nil {
		t.Fatal(err)
	}
	model := expected["llm"].(map[string]any)
	model["model"], model["base_url"], model["protocol"] = "my-model", "https://model.example/v1", string(llm.AnthropicMessages)
	model["authentication"], model["image_understanding"] = config.AuthenticationNone, true
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("init omitted or changed example fields outside requested model options")
	}
	for _, data := range [][]byte{configs.RuntimeExample(), generated} {
		decoder := json.NewDecoder(bytes.NewReader(data))
		if _, err := decoder.Token(); err != nil {
			t.Fatal(err)
		}
		for index := range reflect.TypeFor[config.RuntimeConfig]().NumField() {
			field := reflect.TypeFor[config.RuntimeConfig]().Field(index)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			key, err := decoder.Token()
			if err != nil || key != name {
				t.Fatal("runtime fields out of configured order", key, name, err)
			}
			var value json.RawMessage
			if err := decoder.Decode(&value); err != nil {
				t.Fatal(err)
			}
		}
	}
}
