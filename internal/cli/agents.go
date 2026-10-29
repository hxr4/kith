package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"kith/internal/agents"
)

// agentsCmd connects AI agents (Claude Code, Codex, Antigravity, Cursor,
// Gemini CLI) to Kith: the MCP server plus the Kith skill.
func (a *app) agentsCmd() *cobra.Command {
	var dry, ask bool
	env := func() (agents.Env, error) {
		home := a.home
		if home == "" {
			home = os.Getenv("KITH_HOME")
		}
		e, err := agents.DefaultEnv(home)
		e.DryRun = dry
		e.AutoApprove = !ask
		return e, err
	}
	pick := func(e agents.Env, args []string, onlyDetected bool) ([]agents.Client, error) {
		if len(args) == 0 || (len(args) == 1 && args[0] == "all") {
			var out []agents.Client
			for _, c := range agents.Clients() {
				if !onlyDetected || c.Status(e).Detected {
					out = append(out, c)
				}
			}
			if len(out) == 0 {
				return nil, fmt.Errorf("no supported agent found here; name one: %s", strings.Join(agents.IDs(), ", "))
			}
			return out, nil
		}
		var out []agents.Client
		for _, id := range args {
			c, ok := agents.Find(strings.ToLower(id))
			if !ok {
				return nil, fmt.Errorf("unknown agent %q; choose from %s", id, strings.Join(agents.IDs(), ", "))
			}
			out = append(out, c)
		}
		return out, nil
	}
	list := func(cmd *cobra.Command, _ []string) error {
		e, err := env()
		if err != nil {
			return err
		}
		var st []agents.Status
		for _, c := range agents.Clients() {
			st = append(st, c.Status(e))
		}
		if a.jsonOut {
			a.printJSON(st)
			return nil
		}
		mark := func(ok bool) string {
			if ok {
				return a.p.green("yes")
			}
			return a.p.dim("no")
		}
		fmt.Fprintf(a.out, "%s %s %s %s %s\n", padRight("AGENT", 13), padRight("FOUND", 7), padRight("MCP", 5), padRight("SKILL", 7), "CONFIG")
		for _, s := range st {
			fmt.Fprintf(a.out, "%s %s %s %s %s\n", padRight(s.Name, 13), padRight(mark(s.Detected), 7), padRight(mark(s.MCP), 5), padRight(mark(s.Skill), 7), a.p.dim(s.Config))
			if s.Note != "" {
				fmt.Fprintf(a.out, "  %s\n", a.p.yellow(s.Note))
			}
		}
		fmt.Fprintf(a.out, "\nInstall for every agent found here: kith agents install\nServer for any other MCP client:   kith agents print\n")
		return nil
	}
	report := func(c agents.Client, chs []agents.Change, err error) {
		if a.jsonOut {
			return
		}
		fmt.Fprintf(a.out, "%s\n", a.p.bold(c.Name))
		for _, ch := range chs {
			line := fmt.Sprintf("  %-9s %s", ch.Action, ch.Path)
			if ch.Note != "" {
				line += "  (" + ch.Note + ")"
			}
			fmt.Fprintln(a.out, line)
		}
		if err != nil {
			fmt.Fprintf(a.out, "  %s\n", a.p.red(err.Error()))
		}
	}
	c := &cobra.Command{Use: "agents", Short: "Connect AI agents (Claude Code, Codex, Antigravity, Cursor, Gemini CLI) to Kith", Args: cobra.NoArgs, RunE: list}
	c.AddCommand(&cobra.Command{Use: "list", Short: "Show which agents are found and connected", Args: cobra.NoArgs, RunE: list})
	install := &cobra.Command{Use: "install [agent...|all]", Short: "Add the Kith MCP server and skill to agents (default: every agent found here)",
		Long: "Registers `kith mcp` as an MCP server in each agent's own config and installs the Kith skill where the agent\n" +
			"loads skills. JSON configs are merged (a .kith-backup copy is kept); Codex's config.toml gets one marked block.\n" +
			"Agents: " + strings.Join(agents.IDs(), ", ") + ". Restart the agent afterwards.",
		ValidArgs: append(agents.IDs(), "all"),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			cs, err := pick(e, args, true)
			if err != nil {
				return err
			}
			var res []map[string]any
			failed := false
			for _, cl := range cs {
				chs, err := cl.Install(e)
				report(cl, chs, err)
				r := map[string]any{"agent": cl.ID, "changes": chs}
				if err != nil {
					r["error"], failed = err.Error(), true
				}
				res = append(res, r)
			}
			if a.jsonOut {
				a.printJSON(res)
			} else if !dry {
				if !ask {
					fmt.Fprintf(a.out, "\nKith's tools are pre-approved in Codex and Claude Code (use --ask to be asked each time).\n"+
						"Tasks still need approval on the other machine unless its policy for this one is Auto.\n")
				}
				fmt.Fprintf(a.out, "\nRestart the agents, then ask one: \"I have a Kith-connected computer, run ... on it\".\n")
				fmt.Fprintf(a.out, "Agents start %s; reinstall if you move it.\n", e.Kith)
			}
			if failed {
				return errSilent
			}
			return nil
		}}
	install.Flags().BoolVar(&dry, "dry-run", false, "show what would change without changing it")
	install.Flags().BoolVar(&ask, "ask", false, "don't pre-approve Kith's tools; the agent asks before each call (Codex, Claude Code)")
	uninstall := &cobra.Command{Use: "uninstall [agent...|all]", Short: "Remove the Kith MCP server and skill from agents", ValidArgs: append(agents.IDs(), "all"),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			cs, err := pick(e, args, false)
			if err != nil {
				return err
			}
			for _, cl := range cs {
				chs, err := cl.Uninstall(e)
				report(cl, chs, err)
			}
			return nil
		}}
	uninstall.Flags().BoolVar(&dry, "dry-run", false, "show what would change without changing it")
	c.AddCommand(install, uninstall)
	c.AddCommand(&cobra.Command{Use: "print", Short: "Print the MCP server entry for any MCP client, and the skill's path", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := env()
			if err != nil {
				return err
			}
			fmt.Fprintln(a.out, agents.Snippet(e))
			if !a.jsonOut {
				fmt.Fprintln(a.out, "\nAdd this to your agent's MCP config. For agents that read Agent Skills (SKILL.md),")
				fmt.Fprintln(a.out, "save the skill with: kith agents skill > <skills dir>/kith/SKILL.md")
			}
			return nil
		}})
	c.AddCommand(&cobra.Command{Use: "skill", Short: "Print the Kith skill (SKILL.md)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { _, err := a.out.Write(agents.Skill); return err }})
	c.AddCommand(&cobra.Command{Use: "export <dir>", Short: "Write plugin bundles (Claude Code marketplace, Antigravity plugin, bare skill)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			files, err := agents.Export(args[0])
			for _, f := range files {
				fmt.Fprintln(a.out, "wrote", f)
			}
			return err
		}})
	return c
}
