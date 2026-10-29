// Package agents connects AI coding agents (Claude Code, Codex, Antigravity,
// Cursor, Gemini CLI) to Kith: it registers `kith mcp` as an MCP server in
// each agent's own configuration and installs the Kith skill (SKILL.md, the
// Agent Skills format they share) where each agent looks for skills.
// Changes are additive and reversible: JSON configs are merged (with a
// backup), Codex's TOML gets one marked block, and uninstall removes
// exactly what install added.
package agents

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Skill is the agent-neutral instructions file.
//
//go:embed SKILL.md
var Skill []byte

// ServerName is the MCP server's name in every agent's config.
const ServerName = "kith"

// Env is where and how to install.
type Env struct {
	Home    string   // user home directory
	Kith    string   // absolute path of the kith binary agents should start
	MCPArgs []string // arguments for it (default ["mcp"])
	DryRun  bool
	// AutoApprove lets the agent call Kith's tools without asking each
	// time (where the agent has such a setting). The peer's own Ask/Auto
	// policy still decides whether a task runs there.
	AutoApprove bool
	LookPath    func(string) (string, error)
	Run         func(name string, args ...string) ([]byte, error)
}

// DefaultEnv uses this process's binary and the real home directory.
func DefaultEnv(kithHome string) (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return Env{}, err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	args := []string{"mcp"}
	if kithHome != "" {
		args = append(args, "--home", kithHome)
	}
	return Env{Home: home, Kith: exe, MCPArgs: args, AutoApprove: true, LookPath: exec.LookPath,
		Run: func(name string, a ...string) ([]byte, error) { return exec.Command(name, a...).CombinedOutput() }}, nil
}

func (e Env) args() []string {
	if len(e.MCPArgs) == 0 {
		return []string{"mcp"}
	}
	return e.MCPArgs
}

func (e Env) path(parts ...string) string {
	return filepath.Join(append([]string{e.Home}, parts...)...)
}

// Change is one thing install or uninstall did (or would do).
type Change struct {
	Path   string `json:"path"`
	Action string `json:"action"` // wrote | updated | removed | ran | unchanged | skipped
	Note   string `json:"note,omitempty"`
}

// Status is a client's current state.
type Status struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Detected  bool   `json:"detected"`
	MCP       bool   `json:"mcp"`
	Skill     bool   `json:"skill"`
	SkillPath string `json:"skill_path"`
	Config    string `json:"config"`
	Note      string `json:"note,omitempty"`
}

// Client is one agent.
type Client struct {
	ID, Name  string
	detect    func(Env) bool
	skillDir  func(Env) string // directory that will hold kith/SKILL.md
	configLoc func(Env) string
	mcpOn     func(Env) bool
	addMCP    func(Env) (Change, error)
	removeMCP func(Env) (Change, error)
}

// Clients lists the supported agents.
func Clients() []Client {
	return []Client{claudeCode(), codex(), antigravity(), cursor(), geminiCLI()}
}

// Find returns the client with id.
func Find(id string) (Client, bool) {
	for _, c := range Clients() {
		if c.ID == id {
			return c, true
		}
	}
	return Client{}, false
}

// IDs lists the client ids.
func IDs() []string {
	var out []string
	for _, c := range Clients() {
		out = append(out, c.ID)
	}
	return out
}

func (c Client) skillFile(e Env) string { return filepath.Join(c.skillDir(e), "kith", "SKILL.md") }

// Status reports what is installed.
func (c Client) Status(e Env) Status {
	s := Status{ID: c.ID, Name: c.Name, Detected: c.detect(e), SkillPath: c.skillFile(e), Config: c.configLoc(e)}
	b, err := os.ReadFile(s.SkillPath)
	s.Skill = err == nil && bytes.Equal(b, Skill)
	if err == nil && !s.Skill {
		s.Note = "skill is out of date; run `kith agents install " + c.ID + "`"
	}
	s.MCP = c.mcpOn(e)
	return s
}

// Install registers the MCP server and writes the skill.
func (c Client) Install(e Env) ([]Change, error) {
	var out []Change
	ch, err := writeFile(e, c.skillFile(e), Skill)
	if err != nil {
		return out, err
	}
	out = append(out, ch)
	ch, err = c.addMCP(e)
	out = append(out, ch)
	return out, err
}

// Uninstall removes what Install added.
func (c Client) Uninstall(e Env) ([]Change, error) {
	var out []Change
	dir := filepath.Dir(c.skillFile(e))
	if _, err := os.Stat(dir); err == nil {
		if !e.DryRun {
			if err := os.RemoveAll(dir); err != nil {
				return out, err
			}
		}
		out = append(out, Change{Path: dir, Action: "removed"})
	}
	ch, err := c.removeMCP(e)
	out = append(out, ch)
	return out, err
}

// ---- helpers ----

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func writeFile(e Env, p string, b []byte) (Change, error) {
	if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, b) {
		return Change{Path: p, Action: "unchanged"}, nil
	}
	act := "wrote"
	if exists(p) {
		act = "updated"
	}
	if e.DryRun {
		return Change{Path: p, Action: act}, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return Change{}, err
	}
	return Change{Path: p, Action: act}, os.WriteFile(p, b, 0o644)
}

// backup copies p to p.kith-backup before its first change by Kith.
func backup(p string) {
	bk := p + ".kith-backup"
	if exists(bk) {
		return
	}
	if b, err := os.ReadFile(p); err == nil {
		os.WriteFile(bk, b, 0o600)
	}
}

// ---- JSON configs: {"mcpServers": {"kith": {...}}} ----

func readJSON(p string) (map[string]any, error) {
	m := map[string]any{}
	b, err := os.ReadFile(p)
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (%v); fix it or add the server by hand (`kith agents print`)", p, err)
	}
	return m, nil
}

func jsonServer(e Env) map[string]any {
	return map[string]any{"command": e.Kith, "args": e.args()}
}

func jsonHasServer(p string) bool {
	m, err := readJSON(p)
	if err != nil {
		return false
	}
	s, _ := m["mcpServers"].(map[string]any)
	_, ok := s[ServerName]
	return ok
}

func jsonSetServer(e Env, p string, server map[string]any) (Change, error) {
	m, err := readJSON(p)
	if err != nil {
		return Change{Path: p, Action: "skipped", Note: err.Error()}, err
	}
	servers, _ := m["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if server == nil {
		if _, ok := servers[ServerName]; !ok {
			return Change{Path: p, Action: "unchanged"}, nil
		}
		delete(servers, ServerName)
	} else {
		if cur, ok := servers[ServerName]; ok {
			a, _ := json.Marshal(cur)
			b, _ := json.Marshal(server)
			if bytes.Equal(a, b) {
				return Change{Path: p, Action: "unchanged"}, nil
			}
		}
		servers[ServerName] = server
	}
	m["mcpServers"] = servers
	out, _ := json.MarshalIndent(m, "", "  ")
	act := map[bool]string{true: "updated", false: "removed"}[server != nil]
	if e.DryRun {
		return Change{Path: p, Action: act}, nil
	}
	backup(p)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return Change{}, err
	}
	return Change{Path: p, Action: act}, os.WriteFile(p, append(out, '\n'), 0o644)
}

func jsonClient(id, name string, detect func(Env) bool, config func(Env) string, skills func(Env) string) Client {
	return Client{ID: id, Name: name, detect: detect, skillDir: skills, configLoc: config,
		mcpOn:     func(e Env) bool { return jsonHasServer(config(e)) },
		addMCP:    func(e Env) (Change, error) { return jsonSetServer(e, config(e), jsonServer(e)) },
		removeMCP: func(e Env) (Change, error) { return jsonSetServer(e, config(e), nil) },
	}
}

// ---- clients ----

func antigravity() Client {
	// Antigravity's documented global customization root is ~/.gemini/config
	// (mcp_config.json and skills/<name>/SKILL.md).
	return jsonClient("antigravity", "Antigravity",
		func(e Env) bool {
			return exists(e.path(".gemini", "antigravity")) || exists(e.path(".gemini", "config")) ||
				(runtime.GOOS == "darwin" && exists("/Applications/Antigravity.app"))
		},
		func(e Env) string { return e.path(".gemini", "config", "mcp_config.json") },
		func(e Env) string { return e.path(".gemini", "config", "skills") })
}

func cursor() Client {
	return jsonClient("cursor", "Cursor",
		func(e Env) bool { return exists(e.path(".cursor")) },
		func(e Env) string { return e.path(".cursor", "mcp.json") },
		func(e Env) string { return e.path(".cursor", "skills") })
}

func geminiCLI() Client {
	return jsonClient("gemini", "Gemini CLI",
		func(e Env) bool {
			_, err := e.LookPath("gemini")
			return err == nil || exists(e.path(".gemini", "settings.json"))
		},
		func(e Env) string { return e.path(".gemini", "settings.json") },
		func(e Env) string { return e.path(".gemini", "skills") })
}

func claudeCode() Client {
	cli := func(e Env) string {
		p, err := e.LookPath("claude")
		if err != nil {
			return ""
		}
		return p
	}
	cfg := func(e Env) string { return e.path(".claude.json") }
	return Client{ID: "claude", Name: "Claude Code",
		detect:    func(e Env) bool { return cli(e) != "" || exists(e.path(".claude")) },
		skillDir:  func(e Env) string { return e.path(".claude", "skills") },
		configLoc: func(e Env) string { return "claude mcp (user scope)" },
		mcpOn: func(e Env) bool {
			if c := cli(e); c != "" {
				_, err := e.Run(c, "mcp", "get", ServerName)
				return err == nil
			}
			return jsonHasServer(cfg(e))
		},
		addMCP: func(e Env) (Change, error) {
			if !e.DryRun {
				if _, err := claudeAllow(e, e.AutoApprove); err != nil {
					return Change{Path: e.path(".claude", "settings.json"), Action: "skipped", Note: err.Error()}, err
				}
			}
			c := cli(e)
			if c == "" {
				// No CLI: ~/.claude.json is Claude Code's own file; merge carefully.
				return jsonSetServer(e, cfg(e), map[string]any{"type": "stdio", "command": e.Kith, "args": e.args()})
			}
			args := append([]string{"mcp", "add", "--scope", "user", ServerName, "--", e.Kith}, e.args()...)
			if e.DryRun {
				return Change{Path: "claude " + strings.Join(args, " "), Action: "ran"}, nil
			}
			e.Run(c, "mcp", "remove", "--scope", "user", ServerName) // replace any older entry
			if out, err := e.Run(c, args...); err != nil {
				return Change{Path: "claude mcp add", Action: "skipped", Note: strings.TrimSpace(string(out))}, fmt.Errorf("claude mcp add: %v: %s", err, out)
			}
			return Change{Path: "claude mcp add --scope user " + ServerName, Action: "ran"}, nil
		},
		removeMCP: func(e Env) (Change, error) {
			if !e.DryRun {
				claudeAllow(e, false)
			}
			c := cli(e)
			if c == "" {
				return jsonSetServer(e, cfg(e), nil)
			}
			if e.DryRun {
				return Change{Path: "claude mcp remove --scope user " + ServerName, Action: "ran"}, nil
			}
			if _, err := e.Run(c, "mcp", "remove", "--scope", "user", ServerName); err != nil {
				return Change{Path: "claude mcp remove", Action: "unchanged"}, nil
			}
			return Change{Path: "claude mcp remove --scope user " + ServerName, Action: "ran"}, nil
		},
	}
}

// claudeRule allows every tool of the kith MCP server in Claude Code.
const claudeRule = "mcp__" + ServerName

// claudeAllow adds (or removes) claudeRule in ~/.claude/settings.json
// permissions.allow, keeping everything else in the file.
func claudeAllow(e Env, on bool) (bool, error) {
	p := e.path(".claude", "settings.json")
	m, err := readJSON(p)
	if err != nil {
		return false, err
	}
	perms, _ := m["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	allow, _ := perms["allow"].([]any)
	var kept []any
	has := false
	for _, r := range allow {
		if r == claudeRule {
			has = true
			continue
		}
		kept = append(kept, r)
	}
	if has == on {
		return false, nil
	}
	if on {
		kept = append(kept, claudeRule)
	}
	if len(kept) == 0 {
		delete(perms, "allow")
	} else {
		perms["allow"] = kept
	}
	if len(perms) == 0 {
		delete(m, "permissions")
	} else {
		m["permissions"] = perms
	}
	if e.DryRun {
		return true, nil
	}
	backup(p)
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(p, append(b, '\n'), 0o644)
}

// Codex keeps MCP servers in ~/.codex/config.toml. Kith owns exactly one
// marked block there.
const (
	codexBegin = "# >>> kith (managed by `kith agents`) >>>"
	codexEnd   = "# <<< kith <<<"
)

// tomlString quotes s as a TOML literal string when possible (no escapes,
// so Windows paths stay readable), else as a basic string.
func tomlString(s string) string {
	if !strings.ContainsAny(s, "'\n\r") {
		return "'" + s + "'"
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func codexBlock(e Env) string {
	approve := ""
	if e.AutoApprove {
		approve = "# Kith's tools run without asking; the peer's Ask/Auto policy still gates tasks\ndefault_tools_approval_mode = \"approve\"\n"
	}
	args := make([]string, len(e.args()))
	for i, a := range e.args() {
		args[i] = tomlString(a)
	}
	return codexBegin + "\n[mcp_servers." + ServerName + "]\ncommand = " + tomlString(e.Kith) +
		"\nargs = [" + strings.Join(args, ", ") + "]\nstartup_timeout_sec = 30\n" +
		"# wait_task can block for up to 600 s\ntool_timeout_sec = 660\n" + approve + codexEnd + "\n"
}

// stripCodexBlock removes Kith's block; ok reports whether there was one.
func stripCodexBlock(s string) (string, bool) {
	i := strings.Index(s, codexBegin)
	if i < 0 {
		return s, false
	}
	j := strings.Index(s[i:], codexEnd)
	if j < 0 {
		return s, false
	}
	end := i + j + len(codexEnd)
	if end < len(s) && s[end] == '\n' {
		end++
	}
	return strings.TrimRight(s[:i], "\n") + "\n" + s[end:], true
}

func codex() Client {
	cfg := func(e Env) string { return e.path(".codex", "config.toml") }
	return Client{ID: "codex", Name: "Codex",
		detect:    func(e Env) bool { _, err := e.LookPath("codex"); return err == nil || exists(e.path(".codex")) },
		skillDir:  func(e Env) string { return e.path(".codex", "skills") },
		configLoc: cfg,
		mcpOn: func(e Env) bool {
			b, _ := os.ReadFile(cfg(e))
			return strings.Contains(string(b), "[mcp_servers."+ServerName+"]")
		},
		addMCP: func(e Env) (Change, error) {
			p := cfg(e)
			b, _ := os.ReadFile(p)
			s, had := stripCodexBlock(string(b))
			if strings.Contains(s, "[mcp_servers."+ServerName+"]") {
				return Change{Path: p, Action: "skipped", Note: "a [mcp_servers.kith] table you wrote is already there; leaving it"}, nil
			}
			if had && strings.Contains(string(b), codexBlock(e)) {
				return Change{Path: p, Action: "unchanged"}, nil
			}
			s = strings.TrimRight(s, "\n")
			if s != "" {
				s += "\n\n"
			}
			s += codexBlock(e)
			if e.DryRun {
				return Change{Path: p, Action: "updated"}, nil
			}
			backup(p)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return Change{}, err
			}
			return Change{Path: p, Action: "updated"}, os.WriteFile(p, []byte(s), 0o644)
		},
		removeMCP: func(e Env) (Change, error) {
			p := cfg(e)
			b, err := os.ReadFile(p)
			if err != nil {
				return Change{Path: p, Action: "unchanged"}, nil
			}
			s, had := stripCodexBlock(string(b))
			if !had {
				return Change{Path: p, Action: "unchanged"}, nil
			}
			if e.DryRun {
				return Change{Path: p, Action: "removed"}, nil
			}
			return Change{Path: p, Action: "removed"}, os.WriteFile(p, []byte(s), 0o644)
		},
	}
}

// Snippet is the manual setup for any MCP client.
func Snippet(e Env) string {
	b, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{ServerName: jsonServer(e)}}, "", "  ")
	return string(b)
}
