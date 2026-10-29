// Package cli implements the `kith` command line. Every command except
// `daemon` and `mcp` is a thin IPC client of the local daemon.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"kith/internal/caps"
	"kith/internal/config"
	"kith/internal/daemon"
	"kith/internal/identity"
	"kith/internal/ipc"
	"kith/internal/mcp"
	"kith/internal/protocol"
	"kith/internal/svc"
	"kith/internal/task"
)

type app struct {
	home    string
	jsonOut bool
	noColor bool
	p       painter
	out     io.Writer
	errOut  io.Writer
	in      io.Reader
}

func (a *app) client() ipc.Client { return ipc.Client{Home: config.ResolveHome(a.home)} }

func (a *app) call(cmd *cobra.Command, method string, params, out any) error {
	return a.client().Call(cmd.Context(), method, params, out)
}

func (a *app) printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(a.out, string(b))
}

// Main runs the CLI and returns the process exit code.
func Main(args []string) int {
	a := &app{out: os.Stdout, errOut: os.Stderr, in: os.Stdin}
	root := a.root()
	root.SetArgs(args)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := root.ExecuteContext(ctx); err != nil {
		if e, ok := err.(exitError); ok {
			return e.code
		}
		if e, ok := err.(*protocol.Error); ok {
			fmt.Fprintf(a.errOut, "%s: %s\n", e.Code, e.Message)
			if e.Remediation != "" {
				fmt.Fprintf(a.errOut, "Fix: %s\n", e.Remediation)
			}
		} else if err != errSilent {
			fmt.Fprintln(a.errOut, "error:", err)
		}
		return 1
	}
	return 0
}

var errSilent = fmt.Errorf("silent")

func (a *app) root() *cobra.Command {
	root := &cobra.Command{
		Use:           "kith",
		Short:         "Kith: let AI agents on two paired machines delegate work to each other's hardware",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&a.home, "home", "", "Kith home directory (default $KITH_HOME or ~/.kith)")
	root.PersistentFlags().BoolVar(&a.jsonOut, "json", false, "machine-readable JSON output")
	root.PersistentFlags().BoolVar(&a.noColor, "no-color", false, "disable colored output (also honors NO_COLOR)")
	root.PersistentPreRun = func(cmd *cobra.Command, _ []string) { a.p = newPainter(a.noColor || a.jsonOut) }
	root.Version = config.KithVersion
	root.SetVersionTemplate(versionString() + "\n")
	root.AddGroup(&cobra.Group{ID: "tasks", Title: "Tasks:"}, &cobra.Group{ID: "peers", Title: "Peers & pairing:"},
		&cobra.Group{ID: "daemon", Title: "Daemon & setup:"})
	group := func(id string, cs ...*cobra.Command) []*cobra.Command {
		for _, c := range cs {
			c.GroupID = id
		}
		return cs
	}
	root.AddCommand(group("tasks", a.runCmd(), a.submitCmd(), a.checkCmd(), a.tasksCmd(), a.taskCmd(), a.logsCmd(), a.waitCmd(),
		a.cancelCmd(), a.approveCmd(true), a.approveCmd(false), a.artifactsCmd(), a.fetchCmd())...)
	root.AddCommand(group("peers", a.pairCmd(), a.peersCmd(), a.unpairCmd())...)
	root.AddCommand(group("daemon", a.statusCmd(), a.daemonCmd(), a.configCmd(), a.idCmd(), a.probeCmd(), a.doctorCmd(),
		a.mcpCmd(), a.agentsCmd(), a.versionCmd())...)
	return root
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Print the Kith version and build details", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, _ := os.Executable()
			if a.jsonOut {
				a.printJSON(map[string]string{"version": config.KithVersion, "full": versionString(), "build": buildDetail(), "path": exe})
				return nil
			}
			fmt.Fprintln(a.out, versionString())
			fmt.Fprintf(a.out, "%s\n%s\n", buildDetail(), exe)
			return nil
		}}
}

// ---- daemon ----

func (a *app) daemonCmd() *cobra.Command {
	var asService bool
	c := &cobra.Command{
		Use:   "daemon",
		Short: "Run the Kith daemon in the foreground (or install|uninstall|status)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := config.ResolveHome(a.home)
			run := func(ctx context.Context) error {
				opt := daemon.Options{Home: home}
				if asService || svc.IsService() {
					config.EnsureHome(home)
					f, err := os.OpenFile(filepath.Join(home, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
					if err == nil {
						defer f.Close()
						cfg, _ := config.Load(home)
						lvl := slog.LevelInfo
						if cfg != nil {
							lvl = cfg.SlogLevel()
						}
						opt.Logger = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: lvl}))
					}
				}
				d, err := daemon.New(opt)
				if err != nil {
					return err
				}
				return d.Run(ctx)
			}
			if svc.IsService() {
				return svc.RunService(run)
			}
			return run(cmd.Context())
		},
	}
	c.Flags().BoolVar(&asService, "service", false, "running under the OS service manager")
	c.Flags().MarkHidden("service")
	c.Use = "daemon"
	c.Short = "Run the daemon in the foreground, or control it (start|stop|restart|logs|install|uninstall|status)"
	c.AddCommand(a.daemonControlCmds()...)
	c.AddCommand(
		&cobra.Command{Use: "install", Short: "Install and start Kith as a login/boot service", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				exe, _ = filepath.EvalSymlinks(exe)
				home := config.ResolveHome(a.home)
				if err := config.EnsureHome(home); err != nil {
					return err
				}
				return svc.Install(home, exe, a.out)
			}},
		&cobra.Command{Use: "firewall", Short: "Add the inbound firewall rules Kith needs (Windows, elevated)", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				if runtime.GOOS != "windows" {
					fmt.Fprintln(a.out, "No firewall rules are needed on this OS.")
					return nil
				}
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				if err := svc.AddFirewallRules(exe); err != nil {
					return err
				}
				fmt.Fprintln(a.out, "Added the inbound Kith rules (TCP 7420 and mDNS UDP 5353) for Private networks.")
				return nil
			}},
		&cobra.Command{Use: "uninstall", Short: "Stop and remove the Kith service", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return svc.Uninstall(config.ResolveHome(a.home), a.out) }},
		&cobra.Command{Use: "status", Short: "Show service and daemon status", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				s, _ := svc.Status(config.ResolveHome(a.home))
				var st daemon.StatusView
				err := a.call(cmd, "status", nil, &st)
				if a.jsonOut {
					a.printJSON(map[string]any{"service": s, "daemon": st, "running": err == nil})
					return nil
				}
				fmt.Fprintf(a.out, "service: %s\n", s)
				if err != nil {
					fmt.Fprintln(a.out, "daemon:  not running")
					return nil
				}
				fmt.Fprintf(a.out, "daemon:  running (pid %d, port %d, %d/%d peers online)\n", st.PID, st.Port, st.Online, st.Peers)
				fmt.Fprintf(a.out, "id:      %s (%s)\n", st.Display, st.Name)
				return nil
			}},
	)
	return c
}

// ---- pairing & peers ----

func (a *app) pairCmd() *cobra.Command {
	var ip string
	var yes bool
	c := &cobra.Command{
		Use:   "pair",
		Short: "Pair with another machine (run on both machines at the same time)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cn, err := a.client().Open(cmd.Context(), "pair", map[string]string{"ip": ip})
			if err != nil {
				return err
			}
			defer cn.Close()
			var peer daemon.PeerView
			stdin := bufio.NewReader(a.in)
			err = cn.Wait(func(ev string, data json.RawMessage) error {
				switch ev {
				case "waiting":
					var m map[string]string
					json.Unmarshal(data, &m)
					fmt.Fprintln(a.out, m["message"])
				case "code":
					var e struct {
						Code string           `json:"code"`
						Peer *daemon.PeerView `json:"peer"`
						Addr string           `json:"addr"`
					}
					json.Unmarshal(data, &e)
					fmt.Fprintf(a.out, "\nPeer: %s  %s  (%s)\n\n", e.Peer.Name, e.Peer.Display, e.Addr)
					fmt.Fprintf(a.out, "    Pairing code:  %s %s\n\n", e.Code[:3], e.Code[3:])
					fmt.Fprintln(a.out, "Check that the other machine shows the SAME code.")
					fmt.Fprintln(a.out, "Warning: pairing lets that machine run commands on this one as your user.")
					ok := yes
					if !ok {
						fmt.Fprint(a.out, "Do the codes match? [y/N] ")
						line, _ := stdin.ReadString('\n')
						ok = strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes")
					}
					return cn.Send(map[string]any{"confirm": ok, "code": e.Code})
				case "peer-confirmed":
					var e struct {
						Peer *daemon.PeerView `json:"peer"`
					}
					json.Unmarshal(data, &e)
					if e.Peer != nil && e.Peer.Name != "" {
						fmt.Fprintf(a.out, "%s confirmed the code.\n", e.Peer.Name)
					}
				}
				return nil
			}, &peer)
			if err != nil {
				return err
			}
			if a.jsonOut {
				a.printJSON(peer)
				return nil
			}
			fmt.Fprintf(a.out, "\nPaired with %s (%s). Policy: %s.\n", peer.Name, peer.Display, peer.Policy)
			if peer.Policy == config.PolicyAsk {
				fmt.Fprintf(a.out, "Tasks from %s need `kith approve <id>` here; to auto-accept: kith peers set-policy %s auto\n", peer.Name, peer.Name)
			}
			return nil
		},
	}
	c.Flags().StringVar(&ip, "ip", "", "connect to this address instead of using mDNS discovery")
	c.Flags().BoolVar(&yes, "yes", false, "confirm the code without prompting (only when you have compared both screens)")
	return c
}

func (a *app) peersCmd() *cobra.Command {
	c := &cobra.Command{Use: "peers", Short: "List paired peers", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var ps []daemon.PeerView
			if err := a.call(cmd, "peers", nil, &ps); err != nil {
				return err
			}
			if a.jsonOut {
				a.printJSON(ps)
				return nil
			}
			if len(ps) == 0 {
				fmt.Fprintln(a.out, "No peers. Run `kith pair` on both machines.")
				return nil
			}
			var rows [][]string
			for _, p := range ps {
				rows = append(rows, []string{a.p.bold(p.Name), p.Display, a.p.online(p.Reachable), p.Policy, p.LastAddr, p.Caps})
			}
			a.table([]string{"NAME", "FINGERPRINT", "STATUS", "POLICY", "ADDRESS", "CAPABILITIES"}, rows)
			return nil
		}}
	c.AddCommand(&cobra.Command{Use: "set-policy <peer> <ask|auto>", Short: "Set whether tasks from a peer need approval", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var r map[string]string
			if err := a.call(cmd, "set_policy", map[string]string{"peer": args[0], "policy": args[1]}, &r); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "policy for %s is now %s\n", args[0], r["policy"])
			return nil
		}})
	return c
}

func (a *app) unpairCmd() *cobra.Command {
	return &cobra.Command{Use: "unpair <FP|name>", ValidArgsFunction: a.completePeers, Short: "Remove a paired peer", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var r map[string]string
			if err := a.call(cmd, "unpair", map[string]string{"peer": args[0]}, &r); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "Unpaired %s\n", identity.Display(r["unpaired"]))
			return nil
		}}
}

func (a *app) idCmd() *cobra.Command {
	return &cobra.Command{Use: "id", Short: "Show this machine's fingerprint", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var r map[string]string
			if err := a.call(cmd, "id", nil, &r); err != nil {
				// Works without a daemon too: the identity is a local file.
				id, ierr := identity.LoadOrCreate(config.ResolveHome(a.home))
				if ierr != nil {
					return err
				}
				r = map[string]string{"fp": id.FP, "display": identity.Display(id.FP)}
			}
			if a.jsonOut {
				a.printJSON(r)
				return nil
			}
			fmt.Fprintf(a.out, "%s\n%s\n", r["display"], r["fp"])
			if r["name"] != "" {
				fmt.Fprintf(a.out, "name: %s\n", r["name"])
			}
			return nil
		}}
}

func (a *app) probeCmd() *cobra.Command {
	return &cobra.Command{Use: "probe", Short: "Show the local capability manifest", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var m caps.Manifest
			if err := a.call(cmd, "probe", nil, &m); err != nil {
				return err
			}
			a.printJSON(m)
			return nil
		}}
}

// ---- tasks ----

func (a *app) submitCmd() *cobra.Command {
	var file, peerRef string
	c := &cobra.Command{Use: "submit -f task.toml --peer <name|FP>", Short: "Submit a task to a peer", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return fmt.Errorf("-f task.toml is required")
			}
			b, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			spec, err := task.ParseTOML(b)
			if err != nil {
				return err
			}
			if spec.ID == "" {
				spec.ID = task.NewID()
			}
			abs, _ := filepath.Abs(file)
			var r daemon.SubmitResult
			if err := a.call(cmd, "submit", map[string]any{"peer": peerRef, "spec": json.RawMessage(spec.JSON()), "project_dir": filepath.Dir(abs)}, &r); err != nil {
				return err
			}
			if a.jsonOut {
				a.printJSON(r)
			} else {
				a.printSubmit(r)
			}
			if r.State == task.Rejected {
				return errSilent
			}
			return nil
		}}
	c.Flags().StringVarP(&file, "file", "f", "", "task spec (TOML)")
	c.Flags().StringVar(&peerRef, "peer", "", "peer name or fingerprint (optional with exactly one peer)")
	c.RegisterFlagCompletionFunc("peer", a.completePeers)
	return c
}

func (a *app) printSubmit(r daemon.SubmitResult) {
	switch {
	case r.State == task.Rejected:
		fmt.Fprintf(a.out, "%s REJECTED\n%s\n", r.TaskID, r.Detail)
		if r.Remediation != "" {
			fmt.Fprintf(a.out, "Fix: %s\n", r.Remediation)
		}
	case r.PendingApproval:
		fmt.Fprintf(a.out, "%s submitted; waiting for approval on the executor (`kith approve %s` there)\n", r.TaskID, r.TaskID)
	default:
		fmt.Fprintf(a.out, "%s %s\n", r.TaskID, r.State)
	}
	if r.Existing {
		fmt.Fprintln(a.out, "(this task id already existed; showing the existing record)")
	}
	if r.State != task.Rejected {
		fmt.Fprintf(a.out, "Follow it: kith logs %s -f\n", r.TaskID)
	}
}

func (a *app) tasksCmd() *cobra.Command {
	var state, peerRef, role string
	var active, pending, all bool
	var limit int
	c := &cobra.Command{Use: "tasks", Short: "List tasks in both directions (filters: --active --pending --state --peer --role)",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if all {
				limit = 100000
			}
			var ts []daemon.TaskView
			if err := a.call(cmd, "tasks", map[string]int{"limit": 100000}, &ts); err != nil {
				return err
			}
			ts = filterTasks(ts, taskFilter{state: state, peer: peerRef, role: role, active: active, pending: pending})
			if len(ts) > limit {
				ts = ts[:limit]
			}
			if a.jsonOut {
				a.printJSON(ts)
				return nil
			}
			if len(ts) == 0 {
				fmt.Fprintln(a.out, "No matching tasks.")
				return nil
			}
			var rows [][]string
			for _, t := range ts {
				st := a.p.state(t.State)
				if t.PendingApproval {
					st += a.p.yellow(" (needs approval)")
				}
				rows = append(rows, []string{t.ID, roleArrow(t.Role), t.PeerName, st, t.Name, short(t.UpdatedAt), trunc(t.Detail, 50)})
			}
			a.table([]string{"ID", "DIR", "PEER", "STATE", "NAME", "UPDATED", "DETAIL"}, rows)
			return nil
		}}
	c.Flags().StringVar(&state, "state", "", "only tasks in this state (e.g. RUNNING, FAILED)")
	c.Flags().StringVar(&peerRef, "peer", "", "only tasks with this peer (name or fingerprint prefix)")
	c.Flags().StringVar(&role, "role", "", "out = submitted from here, in = executed here")
	c.Flags().BoolVar(&active, "active", false, "only unfinished tasks")
	c.Flags().BoolVar(&pending, "pending", false, "only tasks waiting for approval on this machine")
	c.Flags().IntVarP(&limit, "limit", "n", 30, "maximum rows")
	c.Flags().BoolVar(&all, "all", false, "no row limit")
	c.RegisterFlagCompletionFunc("state", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return task.AllStates, cobra.ShellCompDirectiveNoFileComp
	})
	c.RegisterFlagCompletionFunc("peer", a.completePeers)
	c.RegisterFlagCompletionFunc("role", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"in", "out"}, cobra.ShellCompDirectiveNoFileComp
	})
	return c
}

func roleArrow(role string) string {
	if role == "executor" {
		return "<- in"
	}
	return "-> out"
}

type taskFilter struct {
	state, peer, role string
	active, pending   bool
}

func filterTasks(ts []daemon.TaskView, f taskFilter) []daemon.TaskView {
	var out []daemon.TaskView
	for _, t := range ts {
		if f.state != "" && !strings.EqualFold(t.State, f.state) {
			continue
		}
		if f.peer != "" && !strings.EqualFold(t.PeerName, f.peer) && !strings.HasPrefix(t.PeerFP, identity.NormalizeFP(f.peer)) {
			continue
		}
		if f.role == "in" && t.Role != "executor" || f.role == "out" && t.Role != "requester" {
			continue
		}
		if f.active && task.IsTerminal(t.State) {
			continue
		}
		if f.pending && !(t.PendingApproval && t.Role == "executor" && t.State == task.Submitted) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// table prints aligned columns; cells may contain ANSI colors.
func (a *app) table(headers []string, rows [][]string) {
	w := make([]int, len(headers))
	for i, h := range headers {
		w[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if n := len(stripANSI(c)); n > w[i] {
				w[i] = n
			}
		}
	}
	line := func(cells []string) {
		var b strings.Builder
		for i, c := range cells {
			if i == len(cells)-1 {
				b.WriteString(c)
			} else {
				b.WriteString(padRight(c, w[i]+2))
			}
		}
		fmt.Fprintln(a.out, strings.TrimRight(b.String(), " "))
	}
	hs := make([]string, len(headers))
	for i, h := range headers {
		hs[i] = a.p.dim(h)
	}
	line(hs)
	for _, r := range rows {
		line(r)
	}
}

func short(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("Jan 02 15:04:05")
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func (a *app) taskCmd() *cobra.Command {
	return &cobra.Command{Use: "task <ID>", ValidArgsFunction: a.completeTasks, Short: "Show a task with its timeline and artifacts", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var t daemon.TaskView
			if err := a.call(cmd, "task", map[string]string{"id": args[0]}, &t); err != nil {
				return err
			}
			if a.jsonOut {
				a.printJSON(t)
				return nil
			}
			fmt.Fprintf(a.out, "%s  %s  (%s, peer %s)\n", a.p.bold(t.ID), a.p.state(t.State), t.Role, t.PeerName)
			fmt.Fprintf(a.out, "argv:    %q\n", t.Argv)
			fmt.Fprintf(a.out, "attempt: %d\n", t.Attempt)
			if t.PID > 0 {
				fmt.Fprintf(a.out, "pid:     %d\n", t.PID)
			}
			if t.Detail != "" {
				fmt.Fprintf(a.out, "detail:  %s\n", t.Detail)
			}
			if t.Remediation != "" {
				fmt.Fprintf(a.out, "fix:     %s\n", t.Remediation)
			}
			fmt.Fprintln(a.out, "timeline:")
			for _, tr := range t.Timeline {
				fmt.Fprintf(a.out, "  %s  %s %s\n", short(tr.TS), padRight(a.p.state(tr.State), 12), tr.Detail)
			}
			if len(t.Artifacts) > 0 {
				fmt.Fprintln(a.out, "artifacts:")
				for _, ar := range t.Artifacts {
					fmt.Fprintf(a.out, "  %-30s %10d  %s  %s\n", ar.Path, ar.Size, ar.SHA256[:16], ar.Status)
				}
			}
			return nil
		}}
}

func (a *app) logsCmd() *cobra.Command {
	var follow bool
	c := &cobra.Command{Use: "logs <ID>", ValidArgsFunction: a.completeTasks, Short: "Print task output", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var r daemon.LogsResult
			err := a.client().Stream(cmd.Context(), "logs", map[string]any{"id": args[0], "follow": follow, "max": 1 << 30},
				func(ev string, data json.RawMessage) error {
					var c daemon.LogChunkEvent
					json.Unmarshal(data, &c)
					io.WriteString(a.out, c.Data)
					return nil
				}, &r)
			if err != nil {
				return err
			}
			io.WriteString(a.out, r.Data)
			if follow {
				fmt.Fprintf(a.errOut, "[kith] task %s %s\n", r.TaskID, r.State)
			}
			return nil
		}}
	c.Flags().BoolVarP(&follow, "follow", "f", false, "follow until the task finishes")
	return c
}

func (a *app) cancelCmd() *cobra.Command {
	return &cobra.Command{Use: "cancel <ID>", ValidArgsFunction: a.completeActiveTasks, Short: "Cancel a task", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var t daemon.TaskView
			if err := a.call(cmd, "cancel", map[string]string{"id": args[0]}, &t); err != nil {
				return err
			}
			if a.jsonOut {
				a.printJSON(t)
				return nil
			}
			fmt.Fprintf(a.out, "%s %s %s\n", t.ID, t.State, t.Detail)
			return nil
		}}
}

func (a *app) approveCmd(approve bool) *cobra.Command {
	use, short, method := "approve [<ID>]", "Approve a task waiting for approval (no ID: list them)", "approve"
	if !approve {
		use, short, method = "deny <ID>", "Deny a task waiting for approval", "deny"
	}
	args := cobra.MaximumNArgs(1)
	if !approve {
		args = cobra.ExactArgs(1)
	}
	return &cobra.Command{Use: use, Short: short, Args: args, ValidArgsFunction: a.completePendingTasks,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				var ts []daemon.TaskView
				if err := a.call(cmd, "tasks", map[string]int{"limit": 100000}, &ts); err != nil {
					return err
				}
				ts = filterTasks(ts, taskFilter{pending: true})
				if a.jsonOut {
					a.printJSON(ts)
					return nil
				}
				if len(ts) == 0 {
					fmt.Fprintln(a.out, "Nothing is waiting for approval.")
					return nil
				}
				for _, t := range ts {
					var tv daemon.TaskView
					a.call(cmd, "task", map[string]string{"id": t.ID}, &tv)
					fmt.Fprintf(a.out, "%s  from %s  %q\n", a.p.bold(t.ID), t.PeerName, tv.Argv)
				}
				fmt.Fprintln(a.out, "\nApprove with: kith approve <ID>   |   deny with: kith deny <ID>")
				return nil
			}
			var t daemon.TaskView
			if err := a.call(cmd, method, map[string]string{"id": args[0]}, &t); err != nil {
				return err
			}
			if a.jsonOut {
				a.printJSON(t)
				return nil
			}
			fmt.Fprintf(a.out, "%s %s %s\n", t.ID, a.p.state(t.State), t.Detail)
			if t.Remediation != "" {
				fmt.Fprintf(a.out, "Fix: %s\n", t.Remediation)
			}
			return nil
		}}
}

func (a *app) artifactsCmd() *cobra.Command {
	return &cobra.Command{Use: "artifacts [<task>]", ValidArgsFunction: a.completeTasks, Short: "List artifacts", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			var as []daemon.ArtifactView
			if err := a.call(cmd, "artifacts", map[string]string{"id": id}, &as); err != nil {
				return err
			}
			if a.jsonOut {
				a.printJSON(as)
				return nil
			}
			sort.Slice(as, func(i, j int) bool { return as[i].TaskID+as[i].Path < as[j].TaskID+as[j].Path })
			w := tabwriter.NewWriter(a.out, 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "TASK\tPATH\tSIZE\tSHA256\tSTATUS")
			for _, ar := range as {
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", ar.TaskID, ar.Path, ar.Size, ar.SHA256[:16], ar.Status)
			}
			return w.Flush()
		}}
}

func (a *app) fetchCmd() *cobra.Command {
	return &cobra.Command{Use: "fetch <task> <path>", ValidArgsFunction: a.completeArtifactPaths, Short: "Fetch an artifact retained on the executor", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var r daemon.FetchResult
			if err := a.call(cmd, "fetch", map[string]string{"id": args[0], "path": args[1]}, &r); err != nil {
				return err
			}
			if a.jsonOut {
				a.printJSON(r)
				return nil
			}
			fmt.Fprintf(a.out, "%s\nsha256 %s (verified)\n", r.Local, r.SHA256)
			return nil
		}}
}

func (a *app) mcpCmd() *cobra.Command {
	return &cobra.Command{Use: "mcp", Short: "Run the MCP server on stdio (for AI agents)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return mcp.ServeStdio(cmd.Context(), config.ResolveHome(a.home))
		}}
}
