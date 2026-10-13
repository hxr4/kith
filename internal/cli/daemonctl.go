package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"kith/internal/config"
	"kith/internal/daemon"
	"kith/internal/svc"
)

func (a *app) daemonRunning(ctx context.Context) (daemon.StatusView, bool) {
	var st daemon.StatusView
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err := a.client().Call(c, "status", nil, &st)
	return st, err == nil
}

func (a *app) waitDaemon(ctx context.Context, up bool, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, ok := a.daemonRunning(ctx); ok == up {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}

// startBackground launches `kith daemon` detached, logging to daemon.log.
func (a *app) startBackground(home string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := config.EnsureHome(home); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(home, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	c := exec.Command(exe, "daemon", "--home", home)
	c.Stdout, c.Stderr = logf, logf
	detach(c)
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

func (a *app) daemonControlCmds() []*cobra.Command {
	start := &cobra.Command{Use: "start", Short: "Start the daemon in the background (or the installed service)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := config.ResolveHome(a.home)
			if st, ok := a.daemonRunning(cmd.Context()); ok {
				fmt.Fprintf(a.out, "already running (pid %d)\n", st.PID)
				return nil
			}
			how := "background process, log: " + filepath.Join(home, "daemon.log")
			if svc.Installed(home) {
				how = "service"
				if err := svc.Start(home); err != nil {
					return err
				}
			} else if err := a.startBackground(home); err != nil {
				return err
			}
			if !a.waitDaemon(cmd.Context(), true, 15*time.Second) {
				return fmt.Errorf("daemon did not come up; see `kith daemon logs`")
			}
			st, _ := a.daemonRunning(cmd.Context())
			fmt.Fprintf(a.out, "%s (pid %d, %s)\n", a.p.green("daemon started"), st.PID, how)
			return nil
		}}
	stop := &cobra.Command{Use: "stop", Short: "Stop the daemon gracefully (running tasks become INTERRUPTED)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := config.ResolveHome(a.home)
			if _, ok := a.daemonRunning(cmd.Context()); !ok {
				fmt.Fprintln(a.out, "not running")
				return nil
			}
			if svc.Installed(home) {
				if err := svc.Stop(home); err != nil {
					return err
				}
			} else if err := a.call(cmd, "shutdown", nil, nil); err != nil {
				return err
			}
			if !a.waitDaemon(cmd.Context(), false, 25*time.Second) {
				return fmt.Errorf("daemon still running after 25s")
			}
			fmt.Fprintln(a.out, "daemon stopped")
			return nil
		}}
	restart := &cobra.Command{Use: "restart", Short: "Stop then start the daemon", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := stop.RunE(cmd, args); err != nil {
				return err
			}
			return start.RunE(cmd, args)
		}}
	var follow bool
	var lines int
	logs := &cobra.Command{Use: "logs", Short: "Show the daemon log (background/service mode)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := filepath.Join(config.ResolveHome(a.home), "daemon.log")
			f, err := os.Open(p)
			if err != nil {
				return fmt.Errorf("no daemon log at %s (a foreground `kith daemon` logs to its terminal)", p)
			}
			defer f.Close()
			if err := tailLines(f, a.out, lines); err != nil {
				return err
			}
			for follow {
				select {
				case <-cmd.Context().Done():
					return nil
				case <-time.After(300 * time.Millisecond):
				}
				io.Copy(a.out, f)
			}
			return nil
		}}
	logs.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new lines")
	logs.Flags().IntVarP(&lines, "lines", "n", 50, "number of trailing lines to show")
	return []*cobra.Command{start, stop, restart, logs}
}

// tailLines prints the last n lines of f and leaves f at EOF.
func tailLines(f *os.File, w io.Writer, n int) error {
	var ring []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		ring = append(ring, sc.Text())
		if len(ring) > n {
			ring = ring[1:]
		}
	}
	for _, l := range ring {
		fmt.Fprintln(w, l)
	}
	_, err := f.Seek(0, io.SeekEnd)
	return err
}
