package runtime

import (
	"context"
	"sort"
	"strings"

	"github.com/soleapi/forgebot/internal/catalog"
	"github.com/soleapi/forgebot/internal/secret"
)

// Capabilities 从当前 generation 获取快照，不探测网络或启动子进程。
// MCP 可加载不等于远端持续健康；Skill 加载完成也不冒充活跃网络连接。
func (m *Manager) Capabilities(ctx context.Context) catalog.Snapshot {
	g := m.acquire()
	if g == nil {
		return catalog.Snapshot{Entries: []catalog.Entry{}}
	}
	defer g.mu.RUnlock()
	result := catalog.Snapshot{Version: g.snapshot.Version, Entries: []catalog.Entry{}}
	for _, status := range g.modules.Status() {
		result.Entries = append(result.Entries, catalog.Entry{ID: status.ID, Kind: "module", Source: string(status.Source), Enabled: status.Enabled, Configured: true, Loaded: status.State == "active", Health: status.State, LastError: publicCatalogError(status.Error), Documentation: "docs/configuration.md"})
	}
	connectorStates := map[string]string{}
	if g.connectors != nil {
		for _, status := range g.connectors.Status() {
			connectorStates[status.ID] = string(status.State)
		}
	}
	for _, connector := range g.snapshot.Runtime.Platforms.Connectors {
		state := connectorStates[connector.ID]
		if state == "" {
			state = "unknown"
		}
		result.Entries = append(result.Entries, catalog.Entry{ID: connector.ID, Kind: "connector", Source: connector.Type, Enabled: connector.Enabled, Configured: true, Loaded: connector.Enabled && state != "stopped" && state != "unknown", Health: state, Permission: "unknown", Documentation: "docs/configuration.md"})
	}
	configured := strings.TrimSpace(g.snapshot.Runtime.LLM.Model) != ""
	if g.snapshot.Runtime.LLM.Authentication != "none" {
		configured = configured && m.credentialConfigured(ctx, g.snapshot.Runtime.LLM.APIKeyRef)
	}
	result.Entries = append(result.Entries, catalog.Entry{ID: "answer", Kind: "model", Source: string(g.snapshot.Runtime.LLM.Protocol), Enabled: true, Configured: configured, Loaded: g.engine.HasModel(), Health: "not_probed", Documentation: "docs/configuration.md"})
	decisionSource := "llm_fallback"
	dmConfigured := configured
	if dm := g.snapshot.Runtime.Decision.DM; dm != nil {
		decisionSource = dm.Provider + "_with_llm_fallback"
		dmConfigured = m.credentialConfigured(ctx, dm.APIKeyRef)
	}
	result.Entries = append(result.Entries, catalog.Entry{ID: "decision", Kind: "decision", Source: decisionSource, Enabled: true, Configured: dmConfigured, Loaded: g.decisions != nil, Health: "not_probed", Documentation: "docs/decision-service.md"})
	for _, server := range g.snapshot.Runtime.Extensions.MCP {
		result.Entries = append(result.Entries, catalog.Entry{ID: server.ID, Kind: "mcp", Source: server.Transport, Enabled: server.Enabled, Configured: true, Loaded: server.Enabled, Health: "not_probed", Documentation: "docs/configuration.md"})
	}
	if len(g.snapshot.Runtime.Extensions.Skills.Directories) > 0 {
		health := "empty"
		if g.engine.HasSkills() {
			health = "loaded"
		}
		result.Entries = append(result.Entries, catalog.Entry{ID: "skills", Kind: "skill", Source: "local", Enabled: true, Configured: true, Loaded: g.engine.HasSkills(), Health: health, Documentation: "docs/configuration.md"})
	}
	for _, name := range g.engine.ToolNames() {
		source := "native"
		if strings.HasPrefix(name, "mcp.") {
			source = "mcp"
		}
		result.Entries = append(result.Entries, catalog.Entry{ID: name, Kind: "tool", Source: source, Enabled: true, Configured: true, Loaded: true, Health: "not_probed", Documentation: "docs/configuration.md"})
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		a, b := result.Entries[i], result.Entries[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
	return result
}

func (m *Manager) credentialConfigured(ctx context.Context, ref string) bool {
	reader, ok := m.builder.Secrets.(secret.StatusReader)
	if !ok {
		return false
	}
	status, err := reader.Status(ctx, ref)
	return err == nil && status.Configured
}
func publicCatalogError(text string) string {
	if text == "" {
		return ""
	}
	return "该能力发生错误，详情见诊断日志"
}
