package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kith/internal/daemon"
	"kith/internal/store"
	"kith/internal/task"
)

type runOpts struct {
	peer, dir, python, os, arch, probe, onInterrupt, checkpoint, id, name, project string
	inputs, outputs, pkgs                                                          []string
	gpu, retainCap, maxIn                                                          float64
	timeout                                                                        int
	noCopyBack, detach                                                             bool
}

// buildSpec turns flags + argv into a task spec (the same JSON a task.toml
// produces), validated by the shared parser.
func (o runOpts) buildSpec(argv []string) (json.RawMessage, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("missing command: kith run [flags] -- <command> [args...]")
	}
	env := map[string]any{}
	if o.python != "" {
		env["python"] = o.python
	}
	if o.os != "" {
		env["os"] = o.os
	}
	if o.arch != "" {
		env["arch"] = o.arch
	}
	if o.probe != "" {
		env["probe"] = o.probe
	}
	if o.gpu > 0 {
		env["gpu"] = map[string]any{"vram_gb": o.gpu, "required": true}
	}
	if len(o.pkgs) > 0 {
		pk := map[string]string{}
		for _, p := range o.pkgs {
			name, c := splitPkg(p)
			if name == "" {
				return nil, fmt.Errorf("--pkg %q: expected name and constraint, e.g. 'torch>=2.3'", p)
			}
			pk[name] = c
		}
		env["packages"] = pk
	}
	spec := map[string]any{"command": argv, "env": env}
	if o.id != "" {
		spec["id"] = o.id
	}
	if o.name != "" {
		spec["name"] = o.name
	}
	if o.project != "" {
		spec["project"] = o.project
	}
	in := map[string]any{"files": o.inputs}
	if o.maxIn > 0 {
		in["max_total_gb"] = o.maxIn
	}
	if o.inputs == nil {
		in["files"] = []string{}
	}
	spec["inputs"] = in
	out := map[string]any{"include": o.outputs}
	if o.outputs == nil {
		out["include"] = []string{}
	}
	if o.noCopyBack {
		out["copy_back"] = false
	}
	if o.retainCap > 0 {
		out["retain_cap_gb"] = o.retainCap
	}
	spec["outputs"] = out
	pol := map[string]any{}
	if o.onInterrupt != "" {
		pol["on_interrupt"] = o.onInterrupt
	}
	if o.checkpoint != "" {
		pol["checkpoint"] = map[string]any{"pattern": o.checkpoint}
	}
	if o.timeout > 0 {
		pol["timeout_minutes"] = o.timeout
	}
	spec["policy"] = pol
	b, _ := json.Marshal(spec)
	s, err := task.ParseJSON(b)
	if err != nil {
		return nil, err
	}
	if s.ID == "" {
		s.ID = task.NewID()
	}
	return s.JSON(), nil
}

func (a *app) runCmd() *cobra.Command {
	var o runOpts
	c := &cobra.Command{
		Use:   "run [flags] -- <command> [args...]",
		Short: "Run a command on a peer without writing a task.toml (follows logs, exits with the task's result)",
		Long: `Run a command on a paired peer. Input files are sent from --dir (default:
the current directory); declared outputs are copied back into it.

Examples:
  kith run --peer HARI-PC -i train.py -i dataset.zip -o 'out/*' \
      --gpu 4 --pkg 'torch>=2.3+cu121' -- python -u train.py --epochs 5
  kith run -i report.py -- python3 report.py
  kith run -d --peer gpu-box -i job.py -- python job.py     # detach: print the id and return

Exit codes: 0 completed, 2 failed, 3 cancelled, 4 rejected, 1 other errors.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, argv []string) error {
			if cmd.ArgsLenAtDash() > 0 {
				return fmt.Errorf("put the command after `--`: kith run [flags] -- %s", strings.Join(argv, " "))
			}
			raw, err := o.buildSpec(argv)
			if err != nil {
				return err
			}
			dir := o.dir
			if dir == "" {
				dir, _ = os.Getwd()
			}
			dir, _ = filepath.Abs(dir)
			var r daemon.SubmitResult
			if err := a.call(cmd, "submit", map[string]any{"peer": o.peer, "spec": raw, "project_dir": dir}, &r); err != nil {
				return err
			}
			if a.jsonOut && o.detach {
				a.printJSON(r)
				return exitForState(r.State)
			}
			if r.State == task.Rejected {
				a.printSubmit(r)
				return exitError{ExitRejected}
			}
			if o.detach {
				fmt.Fprintln(a.out, r.TaskID)
				return nil
			}
			fmt.Fprintf(a.errOut, "%s %s on %s\n", a.p.dim("[kith]"), r.TaskID, peerLabel(r))
			if r.PendingApproval {
				fmt.Fprintf(a.errOut, "%s waiting for approval on the executor (`kith approve %s` there)\n", a.p.dim("[kith]"), r.TaskID)
			}
			return a.followToEnd(cmd.Context(), r.TaskID)
		},
	}
	f := c.Flags()
	f.StringVar(&o.peer, "peer", "", "peer name or fingerprint (optional with exactly one peer)")
	f.StringArrayVarP(&o.inputs, "input", "i", nil, "input file or glob, relative to --dir (repeatable)")
	f.StringArrayVarP(&o.outputs, "output", "o", nil, "output file or glob to bring back (repeatable)")
	f.StringVar(&o.dir, "dir", "", "project directory for inputs/outputs (default: current directory)")
	f.StringVar(&o.python, "python", "", "required Python version, e.g. '>=3.10'")
	f.StringArrayVar(&o.pkgs, "pkg", nil, "required package, e.g. 'torch>=2.3+cu121' (repeatable)")
	f.Float64Var(&o.gpu, "gpu", 0, "require an NVIDIA GPU with at least this many GB of VRAM")
	f.StringVar(&o.os, "os", "", "required OS prefix: macos | windows")
	f.StringVar(&o.arch, "arch", "", "required architecture: arm64 | x86_64")
	f.StringVar(&o.probe, "probe", "", "command that must succeed on the executor before transfer")
	f.StringVar(&o.onInterrupt, "on-interrupt", "", "fail | restart | resume-if-checkpoint")
	f.StringVar(&o.checkpoint, "checkpoint", "", "checkpoint glob for resume-if-checkpoint")
	f.IntVar(&o.timeout, "timeout", 0, "wall-clock limit in minutes (0 = none)")
	f.Float64Var(&o.retainCap, "retain-cap", 0, "keep outputs on the executor if larger than this many GB")
	f.Float64Var(&o.maxIn, "max-input", 0, "refuse to send more than this many GB of inputs (default 25)")
	f.BoolVar(&o.noCopyBack, "no-copy-back", false, "leave outputs on the executor (fetch later with `kith fetch`)")
	f.StringVar(&o.id, "id", "", "explicit task id (tsk_<ULID>) for idempotent re-runs")
	f.StringVar(&o.name, "name", "", "task name (default: the program name)")
	f.StringVar(&o.project, "project", "", "project label")
	f.BoolVarP(&o.detach, "detach", "d", false, "submit and print the task id without following")
	c.RegisterFlagCompletionFunc("peer", a.completePeers)
	c.RegisterFlagCompletionFunc("on-interrupt", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"fail", "restart", "resume-if-checkpoint"}, cobra.ShellCompDirectiveNoFileComp
	})
	c.RegisterFlagCompletionFunc("os", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"macos", "windows"}, cobra.ShellCompDirectiveNoFileComp
	})
	return c
}

func peerLabel(r daemon.SubmitResult) string {
	if r.PeerName != "" {
		return r.PeerName
	}
	if len(r.Peer) >= 16 {
		return r.Peer[:16]
	}
	return r.Peer
}

// followToEnd streams logs until the task is terminal, prints a summary and
// returns an exit error matching the final state. Ctrl-C stops following
// but leaves the task running.
func (a *app) followToEnd(ctx context.Context, id string) error {
	var lr daemon.LogsResult
	err := a.client().Stream(ctx, "logs", map[string]any{"id": id, "follow": true, "max": 1 << 30},
		func(ev string, data json.RawMessage) error {
			var c daemon.LogChunkEvent
			json.Unmarshal(data, &c)
			io.WriteString(a.out, c.Data)
			return nil
		}, &lr)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			fmt.Fprintf(a.errOut, "\n%s stopped following; the task keeps running. Resume: kith logs %s -f; stop: kith cancel %s\n",
				a.p.dim("[kith]"), id, id)
			return nil
		}
		return err
	}
	var tv daemon.TaskView
	if err := a.client().Call(context.Background(), "task", map[string]string{"id": id}, &tv); err != nil {
		return err
	}
	if a.jsonOut {
		a.printJSON(tv)
		return exitForState(tv.State)
	}
	a.printSummary(tv)
	return exitForState(tv.State)
}

func (a *app) printSummary(tv daemon.TaskView) {
	dur := ""
	if len(tv.Timeline) > 1 {
		d := store.ParseTS(tv.Timeline[len(tv.Timeline)-1].TS).Sub(store.ParseTS(tv.Timeline[0].TS))
		dur = " in " + d.Round(100*time.Millisecond).String()
	}
	fmt.Fprintf(a.errOut, "%s %s %s%s", a.p.dim("[kith]"), tv.ID, a.p.state(tv.State), dur)
	if tv.Detail != "" {
		fmt.Fprintf(a.errOut, " - %s", tv.Detail)
	}
	fmt.Fprintln(a.errOut)
	if tv.Remediation != "" {
		fmt.Fprintf(a.errOut, "%s fix: %s\n", a.p.dim("[kith]"), tv.Remediation)
	}
	for _, ar := range tv.Artifacts {
		where := ar.Status
		if ar.Location == store.LocLocal && tv.ProjectDir != "" {
			where = filepath.Join(tv.ProjectDir, filepath.FromSlash(ar.Path))
		}
		fmt.Fprintf(a.errOut, "%s   %s  %s  sha256 %s...\n", a.p.dim("[kith]"), padRight(ar.Path, 20), where, ar.SHA256[:12])
	}
}

func (a *app) waitCmd() *cobra.Command {
	var timeout time.Duration
	c := &cobra.Command{Use: "wait <ID>", Short: "Block until a task finishes; exit code reflects the result",
		Long:              "Exit codes: 0 completed, 2 failed, 3 cancelled, 4 rejected, 5 timeout, 1 other errors.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.completeActiveTasks,
		RunE: func(cmd *cobra.Command, args []string) error {
			var deadline <-chan time.Time
			if timeout > 0 {
				deadline = time.After(timeout)
			}
			for {
				var tv daemon.TaskView
				if err := a.call(cmd, "task", map[string]string{"id": args[0]}, &tv); err != nil {
					return err
				}
				if task.IsTerminal(tv.State) {
					if a.jsonOut {
						a.printJSON(tv)
					} else {
						a.printSummary(tv)
					}
					return exitForState(tv.State)
				}
				select {
				case <-cmd.Context().Done():
					return exitError{ExitError}
				case <-deadline:
					fmt.Fprintf(a.errOut, "%s %s still %s after %s\n", a.p.dim("[kith]"), tv.ID, tv.State, timeout)
					return exitError{ExitTimeout}
				case <-time.After(500 * time.Millisecond):
				}
			}
		}}
	c.Flags().DurationVar(&timeout, "timeout", 0, "give up after this long (e.g. 30m); exit code 5")
	return c
}

// splitPkg parses "torch>=2.3+cu121", "numpy==1.26" or "torch=>=2.3".
func splitPkg(p string) (string, string) {
	p = strings.TrimSpace(p)
	i := strings.IndexAny(p, "<>=~!")
	if i <= 0 {
		return p, ""
	}
	name, c := strings.TrimSpace(p[:i]), strings.TrimSpace(p[i:])
	if len(c) > 1 && c[0] == '=' && strings.ContainsRune("<>~!", rune(c[1])) {
		c = c[1:] // name=>=x form
	}
	return name, c
}
