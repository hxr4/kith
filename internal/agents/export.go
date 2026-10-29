package agents

import (
	"encoding/json"
	"os"
	"path/filepath"

	"kith/internal/config"
)

const pluginDescription = "Run commands and GPU training on your other Kith-paired computer and follow live progress (epochs, accuracy, F1) from any agent."

// Export writes installable bundles under dir for people who prefer
// plugin managers to `kith agents install`:
//
//	claude-code/   a Claude Code plugin marketplace (/plugin marketplace add <dir>/claude-code)
//	antigravity/   an Antigravity plugin (copy kith/ into ~/.gemini/config/plugins/)
//	skill/         the bare Agent Skill, for any agent that reads SKILL.md
//	mcp.json       the MCP server entry, for any MCP client
//
// They start `kith` from PATH; `kith agents install` writes absolute paths
// instead, which also works for GUI apps that don't see your shell's PATH.
func Export(dir string) ([]string, error) {
	server := map[string]any{"mcpServers": map[string]any{ServerName: map[string]any{"command": "kith", "args": []string{"mcp"}}}}
	js := func(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
	files := map[string][]byte{
		"claude-code/.claude-plugin/marketplace.json": js(map[string]any{
			"name":    "kith",
			"owner":   map[string]any{"name": "Kith"},
			"plugins": []any{map[string]any{"name": "kith", "source": "./kith", "description": pluginDescription}},
		}),
		"claude-code/kith/.claude-plugin/plugin.json": js(map[string]any{
			"name": "kith", "version": config.KithVersion, "description": pluginDescription,
			"keywords": []string{"gpu", "remote", "training", "mcp"},
		}),
		"claude-code/kith/.mcp.json":            js(server),
		"claude-code/kith/skills/kith/SKILL.md": Skill,
		"antigravity/kith/plugin.json":          js(map[string]any{"name": "kith"}),
		"antigravity/kith/mcp_config.json":      js(server),
		"antigravity/kith/skills/kith/SKILL.md": Skill,
		"skill/kith/SKILL.md":                   Skill,
		"mcp.json":                              js(server),
	}
	var wrote []string
	for rel, b := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return wrote, err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return wrote, err
		}
		wrote = append(wrote, p)
	}
	return wrote, nil
}
