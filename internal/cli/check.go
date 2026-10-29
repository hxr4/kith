package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"kith/internal/caps"
	"kith/internal/daemon"
	"kith/internal/task"
	"kith/internal/transfer"
)

// CheckInput is one resolved input file.
type CheckInput struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// CheckResult is `kith check --json`: the parsed task and a preflight
// against the peer's last advertised manifest (the desktop app's New task
// sheet renders this). The executor still checks again when the task is sent.
type CheckResult struct {
	File           string             `json:"file"`
	ProjectDir     string             `json:"project_dir"`
	Name           string             `json:"name"`
	Project        string             `json:"project"`
	Command        []string           `json:"command"`
	Inputs         []CheckInput       `json:"inputs"`
	InputBytes     int64              `json:"input_bytes"`
	Outputs        []string           `json:"outputs"`
	TimeoutMinutes int                `json:"timeout_minutes"`
	OnInterrupt    string             `json:"on_interrupt"`
	Probe          []string           `json:"probe,omitempty"`
	Peer           string             `json:"peer,omitempty"`
	PeerName       string             `json:"peer_name,omitempty"`
	Reachable      bool               `json:"reachable"`
	Requirements   []caps.Requirement `json:"requirements"`
	Problem        string             `json:"problem,omitempty"`
	OK             bool               `json:"ok"`
}

func (a *app) checkCmd() *cobra.Command {
	var file, peerRef string
	c := &cobra.Command{Use: "check -f task.toml [--peer <name|FP>]", Short: "Check a task against a peer before sending it", Args: cobra.NoArgs,
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
			abs, _ := filepath.Abs(file)
			r := CheckResult{File: abs, ProjectDir: filepath.Dir(abs), Name: spec.Name, Project: spec.Project, Command: spec.Command,
				Outputs: spec.Outputs.Include, TimeoutMinutes: spec.Policy.TimeoutMinutes, OnInterrupt: spec.Policy.OnInterrupt,
				Probe: spec.Env.Probe, Inputs: []CheckInput{}, Requirements: []caps.Requirement{}}
			files, err := task.ExpandGlobs(r.ProjectDir, spec.Inputs.Files)
			if err != nil {
				r.Problem = "inputs: " + err.Error()
			}
			for _, f := range files {
				_, size, err := transfer.SHAFile(filepath.Join(r.ProjectDir, filepath.FromSlash(f)))
				if err != nil {
					r.Problem = fmt.Sprintf("input %s: %v", f, err)
					continue
				}
				r.Inputs = append(r.Inputs, CheckInput{Path: f, Size: size})
				r.InputBytes += size
			}
			var peers []daemon.PeerView
			if err := a.call(cmd, "peers", nil, &peers); err != nil {
				return err
			}
			p, perr := pickPeer(peers, peerRef)
			switch {
			case perr != nil:
				if r.Problem == "" {
					r.Problem = perr.Error()
				}
			case p.Manifest == nil:
				r.Peer, r.PeerName, r.Reachable = p.FP, p.Name, p.Reachable
				if r.Problem == "" {
					r.Problem = p.Name + " is offline; Kith can check this task once it is back online"
				}
			default:
				r.Peer, r.PeerName, r.Reachable = p.FP, p.Name, p.Reachable
				r.Requirements = caps.Explain(*p.Manifest, spec.Env, r.InputBytes)
			}
			r.OK = r.Problem == "" && r.Reachable
			for _, q := range r.Requirements {
				if q.Status == "fail" {
					r.OK = false
				}
			}
			if a.jsonOut {
				a.printJSON(r)
			} else {
				a.printCheck(r)
			}
			if !r.OK {
				return errSilent
			}
			return nil
		}}
	c.Flags().StringVarP(&file, "file", "f", "", "task spec (TOML)")
	c.Flags().StringVar(&peerRef, "peer", "", "peer name or fingerprint (optional with exactly one peer)")
	c.RegisterFlagCompletionFunc("peer", a.completePeers)
	return c
}

// pickPeer: see daemon.PickPeer.
var pickPeer = daemon.PickPeer

func (a *app) printCheck(r CheckResult) {
	fmt.Fprintf(a.out, "%s  %s  (%d inputs, %s)\n", a.p.bold(r.Name), strings.Join(r.Command, " "), len(r.Inputs), humanBytes(r.InputBytes))
	if r.PeerName != "" {
		fmt.Fprintf(a.out, "peer  %s  %s\n", r.PeerName, a.p.online(r.Reachable))
	}
	for _, q := range r.Requirements {
		mark := map[string]string{"pass": a.p.green("[ OK ]"), "fail": a.p.red("[FAIL]"), "unknown": a.p.dim("[ ?? ]")}[q.Status]
		fmt.Fprintf(a.out, "%s %s  ->  %s\n", mark, padRight(q.Req, 24), q.Have)
	}
	if r.Problem != "" {
		fmt.Fprintf(a.out, "%s\n", a.p.red(r.Problem))
	}
	if r.OK {
		fmt.Fprintln(a.out, "ready to send: kith submit -f "+r.File)
	}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
