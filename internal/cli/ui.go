package cli

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	"kith/internal/config"
	"kith/internal/protocol"
	"kith/internal/task"
)

// Exit codes (documented in docs/CLI.md).
const (
	ExitOK        = 0
	ExitError     = 1 // usage, connection or daemon error
	ExitFailed    = 2 // task FAILED
	ExitCancelled = 3 // task CANCELLED
	ExitRejected  = 4 // task REJECTED (capabilities, policy, busy)
	ExitTimeout   = 5 // `kith wait --timeout` elapsed
)

// exitError carries a specific process exit code without extra output.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// exitForState maps a terminal task state to an exit code.
func exitForState(state string) error {
	switch state {
	case task.Completed:
		return nil
	case task.Failed:
		return exitError{ExitFailed}
	case task.Cancelled:
		return exitError{ExitCancelled}
	case task.Rejected:
		return exitError{ExitRejected}
	}
	return nil
}

// versionString includes the git revision stamped by `go build` in a repo.
func versionString() string {
	v := fmt.Sprintf("kith %s (Kith/%d)", config.KithVersion, protocol.Version)
	if bi, ok := debug.ReadBuildInfo(); ok {
		var rev, dirty, when string
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				if s.Value == "true" {
					dirty = "+dirty"
				}
			case "vcs.time":
				when = s.Value
			}
		}
		if len(rev) > 7 {
			rev = rev[:7]
		}
		if rev != "" {
			v += fmt.Sprintf(" commit %s%s", rev, dirty)
		}
		if when != "" {
			v += " " + when
		}
	}
	return v
}

func buildDetail() string {
	return fmt.Sprintf("%s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// ---- color ----

type painter struct{ on bool }

func newPainter(noColor bool) painter {
	if noColor || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return painter{}
	}
	st, err := os.Stdout.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return painter{}
	}
	return painter{on: true}
}

func (p painter) wrap(code, s string) string {
	if !p.on || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p painter) bold(s string) string   { return p.wrap("1", s) }
func (p painter) dim(s string) string    { return p.wrap("2", s) }
func (p painter) green(s string) string  { return p.wrap("32", s) }
func (p painter) red(s string) string    { return p.wrap("31", s) }
func (p painter) yellow(s string) string { return p.wrap("33", s) }
func (p painter) cyan(s string) string   { return p.wrap("36", s) }

// state colors a task state.
func (p painter) state(s string) string {
	switch s {
	case task.Completed:
		return p.green(s)
	case task.Failed, task.Rejected:
		return p.red(s)
	case task.Cancelled, task.Interrupted, task.Cancelling:
		return p.yellow(s)
	case task.Running, task.Transferring, task.Finalizing:
		return p.cyan(s)
	}
	return s
}

func (p painter) online(on bool) string {
	if on {
		return p.green("online")
	}
	return p.dim("offline")
}

// padRight pads s (which may contain ANSI codes) to width visible chars.
func padRight(s string, width int) string {
	vis := len(stripANSI(s))
	if vis >= width {
		return s
	}
	return s + strings.Repeat(" ", width-vis)
}

func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			in = true
			continue
		}
		if in {
			if c == 'm' {
				in = false
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
