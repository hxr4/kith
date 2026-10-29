package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testEnv(t *testing.T) Env {
	home := t.TempDir()
	for _, d := range []string{".codex", ".cursor", ".gemini/config"} {
		os.MkdirAll(filepath.Join(home, d), 0o755)
	}
	return Env{Home: home, Kith: `C:\Program Files\Kith\kith.exe`, MCPArgs: []string{"mcp"}, AutoApprove: true,
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
		Run:      func(string, ...string) ([]byte, error) { return nil, errors.New("no cli") }}
}

func TestInstallUninstallRoundTrip(t *testing.T) {
	e := testEnv(t)
	codexCfg := "model = \"gpt-5\"\n\n[mcp_servers.other]\ncommand = \"x\"\n"
	os.WriteFile(filepath.Join(e.Home, ".codex/config.toml"), []byte(codexCfg), 0o644)
	claudeSettings := `{"permissions":{"allow":["Bash(ls:*)"]},"model":"opus"}`
	os.MkdirAll(filepath.Join(e.Home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(e.Home, ".claude/settings.json"), []byte(claudeSettings), 0o644)
	cursorCfg := `{"mcpServers":{"other":{"command":"y"}},"theme":"dark"}`
	os.WriteFile(filepath.Join(e.Home, ".cursor/mcp.json"), []byte(cursorCfg), 0o644)

	for _, c := range Clients() {
		if _, err := c.Install(e); err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		s := c.Status(e)
		if !s.MCP || !s.Skill {
			t.Fatalf("%s after install: %+v", c.ID, s)
		}
		// Installing again changes nothing.
		chs, _ := c.Install(e)
		for _, ch := range chs {
			if ch.Action != "unchanged" {
				t.Fatalf("%s: second install changed %+v", c.ID, ch)
			}
		}
	}
	// Codex: one marked block with a Windows path kept literal.
	b, _ := os.ReadFile(filepath.Join(e.Home, ".codex/config.toml"))
	if !strings.HasPrefix(string(b), codexCfg) || strings.Count(string(b), "[mcp_servers.kith]") != 1 ||
		!strings.Contains(string(b), `command = 'C:\Program Files\Kith\kith.exe'`) || !strings.Contains(string(b), "tool_timeout_sec = 660") ||
		!strings.Contains(string(b), `default_tools_approval_mode = "approve"`) {
		t.Fatalf("codex config:\n%s", b)
	}
	// Claude: the kith tools are allowed, other rules and keys kept.
	var cs map[string]any
	b, _ = os.ReadFile(filepath.Join(e.Home, ".claude/settings.json"))
	json.Unmarshal(b, &cs)
	if allow := cs["permissions"].(map[string]any)["allow"].([]any); len(allow) != 2 || allow[1] != "mcp__kith" || cs["model"] != "opus" {
		t.Fatalf("claude settings: %s", b)
	}
	// Cursor: merged, other keys kept.
	var m map[string]any
	b, _ = os.ReadFile(filepath.Join(e.Home, ".cursor/mcp.json"))
	json.Unmarshal(b, &m)
	srv := m["mcpServers"].(map[string]any)
	if m["theme"] != "dark" || srv["other"] == nil || srv["kith"].(map[string]any)["command"] != e.Kith {
		t.Fatalf("cursor config: %s", b)
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".cursor/mcp.json.kith-backup")); err != nil {
		t.Fatal("no backup of the original cursor config")
	}

	for _, c := range Clients() {
		if _, err := c.Uninstall(e); err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		if s := c.Status(e); s.MCP || s.Skill {
			t.Fatalf("%s after uninstall: %+v", c.ID, s)
		}
	}
	b, _ = os.ReadFile(filepath.Join(e.Home, ".claude/settings.json"))
	cs = nil
	json.Unmarshal(b, &cs)
	if allow := cs["permissions"].(map[string]any)["allow"].([]any); len(allow) != 1 || allow[0] != "Bash(ls:*)" {
		t.Fatalf("claude settings after uninstall: %s", b)
	}
	b, _ = os.ReadFile(filepath.Join(e.Home, ".codex/config.toml"))
	if string(b) != codexCfg {
		t.Fatalf("codex config not restored:\n%q\nwant\n%q", b, codexCfg)
	}
	b, _ = os.ReadFile(filepath.Join(e.Home, ".cursor/mcp.json"))
	m = nil
	json.Unmarshal(b, &m)
	if _, ok := m["mcpServers"].(map[string]any)["kith"]; ok || m["theme"] != "dark" {
		t.Fatalf("cursor config after uninstall: %s", b)
	}
}

func TestRefusesBrokenJSON(t *testing.T) {
	e := testEnv(t)
	p := filepath.Join(e.Home, ".cursor/mcp.json")
	os.WriteFile(p, []byte(`{"mcpServers": {`), 0o644)
	c, _ := Find("cursor")
	if _, err := c.Install(e); err == nil {
		t.Fatal("a broken config must not be overwritten")
	}
	if b, _ := os.ReadFile(p); string(b) != `{"mcpServers": {` {
		t.Fatalf("config was changed: %s", b)
	}
}

func TestCodexLeavesUserTable(t *testing.T) {
	e := testEnv(t)
	own := "[mcp_servers.kith]\ncommand = \"/opt/kith\"\n"
	p := filepath.Join(e.Home, ".codex/config.toml")
	os.WriteFile(p, []byte(own), 0o644)
	c, _ := Find("codex")
	chs, err := c.Install(e)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != own || chs[1].Action != "skipped" {
		t.Fatalf("hand-written table must be left alone: %s %+v", b, chs)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	e := testEnv(t)
	e.DryRun = true
	for _, c := range Clients() {
		if _, err := c.Install(e); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	filepath.Walk(e.Home, func(p string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() {
			n++
		}
		return nil
	})
	if n != 0 {
		t.Fatalf("dry run wrote %d files", n)
	}
}

// The committed bundles under integrations/ must match the embedded skill.
func TestIntegrationsUpToDate(t *testing.T) {
	root := filepath.Join("..", "..", "integrations")
	for _, rel := range []string{"skill/kith/SKILL.md", "claude-code/kith/skills/kith/SKILL.md", "antigravity/kith/skills/kith/SKILL.md"} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s: %v (run `make integrations`)", rel, err)
		}
		if !bytes.Equal(b, Skill) {
			t.Fatalf("%s is stale; run `make integrations`", rel)
		}
	}
}
