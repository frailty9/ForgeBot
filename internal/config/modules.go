package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
)

const MaxModules = 64
const MaxModuleOptionsBytes = 16 << 10
const MaxTotalModuleOptionsBytes = 256 << 10

var moduleIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)

// ModuleConfig holds one module's switch and opaque, implementation-owned options.
type ModuleConfig struct {
	Enabled *bool           `json:"enabled,omitempty"`
	Options json.RawMessage `json:"options,omitempty"`
}

func (c *ModuleConfig) UnmarshalJSON(data []byte) error {
	type plain ModuleConfig
	var value plain
	if err := decodeStrictJSON(data, &value); err != nil {
		return err
	}
	*c = ModuleConfig(value)
	return nil
}

func ValidModuleID(id string) bool { return moduleIDPattern.MatchString(id) }

func ValidateModules(entries map[string]ModuleConfig) error {
	if len(entries) > MaxModules {
		return fmt.Errorf("modules exceeds %d entries", MaxModules)
	}
	total := 0
	for id, entry := range entries {
		if !ValidModuleID(id) {
			return fmt.Errorf("invalid module id %q", id)
		}
		total += len(entry.Options)
		if len(entry.Options) > MaxModuleOptionsBytes {
			return fmt.Errorf("module %q options too large", id)
		}
		if len(entry.Options) > 0 {
			value := bytes.TrimSpace(entry.Options)
			if len(value) == 0 || value[0] != '{' || !json.Valid(value) {
				return fmt.Errorf("module %q options must be a JSON object", id)
			}
		}
	}
	if total > MaxTotalModuleOptionsBytes {
		return fmt.Errorf("total module options too large")
	}
	return nil
}

// Clone returns an independently owned runtime configuration, including secrets
// already held by the runtime. It is not a redaction or serialization operation.
func (c RuntimeConfig) Clone() RuntimeConfig {
	c.Modules = maps.Clone(c.Modules)
	for id, entry := range c.Modules {
		if entry.Enabled != nil {
			enabled := *entry.Enabled
			entry.Enabled = &enabled
		}
		entry.Options = slices.Clone(entry.Options)
		c.Modules[id] = entry
	}
	c.Search.Extensions = slices.Clone(c.Search.Extensions)
	c.Platforms.Connectors = slices.Clone(c.Platforms.Connectors)
	for index := range c.Platforms.Connectors {
		if connector := c.Platforms.Connectors[index].OneBot; connector != nil {
			value := *connector
			c.Platforms.Connectors[index].OneBot = &value
		}
	}
	c.Platforms.Groups = slices.Clone(c.Platforms.Groups)
	c.Administration.Administrators = slices.Clone(c.Administration.Administrators)
	c.Reflection.ConversationIDs = slices.Clone(c.Reflection.ConversationIDs)
	c.Operations.Groups = slices.Clone(c.Operations.Groups)
	for index := range c.Operations.Groups {
		c.Operations.Groups[index].Commands = slices.Clone(c.Operations.Groups[index].Commands)
	}
	c.Extensions.Skills.Directories = slices.Clone(c.Extensions.Skills.Directories)
	c.Extensions.MCP = slices.Clone(c.Extensions.MCP)
	for index := range c.Extensions.MCP {
		item := &c.Extensions.MCP[index]
		item.Args = slices.Clone(item.Args)
		item.AllowedTools = slices.Clone(item.AllowedTools)
		item.Environment = maps.Clone(item.Environment)
	}
	return c
}

func (s Snapshot) Clone() Snapshot { s.Runtime = s.Runtime.Clone(); return s }
