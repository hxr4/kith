//go:build linux

package svc

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

const UnitName = "kith.service"

func unitPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		h, _ := os.UserHomeDir()
		dir = filepath.Join(h, ".config")
	}
	return filepath.Join(dir, "systemd", "user", UnitName)
}

func systemctl(args ...string) (string, error) {
	b, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(b)), err
}

// Install writes a systemd user unit, enables and starts it, and enables
// lingering so the daemon also runs at boot without a login (headless Pi).
func Install(home, exe string, out io.Writer) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemd not found; run `kith daemon start` from your init system instead")
	}
	p := unitPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(SystemdUnit(exe, home, os.Getenv("PATH"))), 0o644); err != nil {
		return err
	}
	if o, err := systemctl("daemon-reload"); err != nil {
		return fmt.Errorf("systemctl --user daemon-reload: %v: %s", err, o)
	}
	if o, err := systemctl("enable", "--now", UnitName); err != nil {
		return fmt.Errorf("systemctl --user enable --now %s: %v: %s", UnitName, err, o)
	}
	fmt.Fprintf(out, "Installed systemd user service %s (started, enabled at login).\n", p)
	fmt.Fprintf(out, "Daemon log: %s\n", filepath.Join(home, "daemon.log"))
	fmt.Fprintln(out, "Tasks will see this shell's PATH; re-run `kith daemon install` after installing new tools.")
	u, _ := user.Current()
	name := ""
	if u != nil {
		name = u.Username
	}
	if b, err := exec.Command("loginctl", "enable-linger", name).CombinedOutput(); err != nil {
		fmt.Fprintf(out, "\nTo run kith at boot without logging in (headless), run once:\n  sudo loginctl enable-linger %s\n(%s)\n",
			name, strings.TrimSpace(string(b)))
	} else {
		fmt.Fprintln(out, "Lingering enabled: kith also starts at boot without a login (headless).")
	}
	fmt.Fprintln(out, "\nIf a firewall (ufw/firewalld) is active, allow TCP 7420 and UDP 5353 on your LAN, e.g.:")
	fmt.Fprintln(out, "  sudo ufw allow from 192.168.0.0/16 to any port 7420 proto tcp")
	return nil
}

func Uninstall(home string, out io.Writer) error {
	systemctl("disable", "--now", UnitName)
	if err := os.Remove(unitPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	systemctl("daemon-reload")
	fmt.Fprintln(out, "systemd user service removed. Your identity, peers and tasks in", home, "are kept.")
	return nil
}

func Installed(home string) bool {
	b, err := os.ReadFile(unitPath())
	return err == nil && unitServesHome(string(b), home)
}

func Status(home string) (string, error) {
	b, err := os.ReadFile(unitPath())
	if err != nil {
		return "not installed", nil
	}
	if !unitServesHome(string(b), home) {
		return "not installed for this home (the systemd unit serves another --home)", nil
	}
	state, _ := systemctl("is-active", UnitName)
	if state == "" {
		state = "unknown"
	}
	return "installed, " + state, nil
}

func Start(home string) error {
	if o, err := systemctl("start", UnitName); err != nil {
		return fmt.Errorf("systemctl --user start: %v: %s", err, o)
	}
	return nil
}

// Stop is synchronous: systemctl waits for the daemon's graceful shutdown.
func Stop(home string) error {
	if o, err := systemctl("stop", UnitName); err != nil {
		return fmt.Errorf("systemctl --user stop: %v: %s", err, o)
	}
	return nil
}

func IsService() bool { return false }

func RunService(run func(ctx context.Context) error) error { return run(context.Background()) }

func FirewallRuleExists() (bool, error) { return true, nil }

func MDNSRuleExists() bool { return true }

// AddFirewallRules is a no-op: only Windows needs inbound rules for Kith.
func AddFirewallRules(exe string) error { return nil }

// NetworkProfiles is Windows-only (Public/Private network categories).
func NetworkProfiles() ([]string, error) { return nil, nil }
