//go:build darwin

// Package svc installs Kith as an OS service: a LaunchAgent on macOS and a
// Windows service (plus firewall rule) on Windows.
package svc

import (
	"context"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const Label = "dev.kith.daemon"

func plistPath() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "Library", "LaunchAgents", Label+".plist")
}

func plist(exe, home string) string {
	esc := html.EscapeString
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + Label + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + esc(exe) + `</string>
    <string>daemon</string>
    <string>--home</string>
    <string>` + esc(home) + `</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key><string>` + esc(os.Getenv("PATH")) + `</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardOutPath</key><string>` + esc(filepath.Join(home, "daemon.log")) + `</string>
  <key>StandardErrorPath</key><string>` + esc(filepath.Join(home, "daemon.log")) + `</string>
</dict>
</plist>
`
}

func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// Install writes and loads the LaunchAgent.
func Install(home, exe string, out io.Writer) error {
	p := plistPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	exec.Command("launchctl", "bootout", domain()+"/"+Label).Run()
	if err := os.WriteFile(p, []byte(plist(exe, home)), 0o644); err != nil {
		return err
	}
	if b, err := exec.Command("launchctl", "bootstrap", domain(), p).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(b)))
	}
	fmt.Fprintf(out, "Installed LaunchAgent %s (autostarts at login).\n", p)
	fmt.Fprintln(out, "Tasks will see this shell's PATH; re-run `kith daemon install` after installing new tools (e.g. another Python).")
	fmt.Fprintf(out, "Daemon log: %s\n\n", filepath.Join(home, "daemon.log"))
	fmt.Fprintln(out, "Manual step: macOS may ask to allow \"kith\" to find devices on your local network.")
	fmt.Fprintln(out, "Allow it, or enable it later in System Settings > Privacy & Security > Local Network.")
	fmt.Fprintln(out, "Without it, mDNS discovery and LAN connections can silently fail; `kith doctor` checks this.")
	return nil
}

func Uninstall(home string, out io.Writer) error {
	exec.Command("launchctl", "bootout", domain()+"/"+Label).Run()
	if err := os.Remove(plistPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Fprintln(out, "LaunchAgent removed. Your identity, peers and tasks in", home, "are kept.")
	return nil
}

func Status(home string) (string, error) {
	if _, err := os.Stat(plistPath()); err != nil {
		return "not installed", nil
	}
	if !Installed(home) {
		return "not installed for this home (the LaunchAgent serves another --home)", nil
	}
	b, err := exec.Command("launchctl", "print", domain()+"/"+Label).CombinedOutput()
	if err != nil {
		return "installed, not loaded", nil
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "state = ") {
			return "installed, " + strings.TrimPrefix(l, "state = "), nil
		}
	}
	return "installed", nil
}

func IsService() bool { return false }

func RunService(run func(ctx context.Context) error) error { return run(context.Background()) }

func FirewallRuleExists() (bool, error) { return true, nil }

// Installed reports whether the LaunchAgent exists and serves this home.
func Installed(home string) bool {
	b, err := os.ReadFile(plistPath())
	return err == nil && strings.Contains(string(b), "<string>"+html.EscapeString(home)+"</string>")
}

func loaded() bool {
	return exec.Command("launchctl", "print", domain()+"/"+Label).Run() == nil
}

// Start loads the LaunchAgent, or restarts it in place if already loaded.
// launchd tears jobs down asynchronously after bootout, so bootstrap is
// retried briefly (DECISIONS.md D-046).
func Start(home string) error {
	if loaded() {
		if b, err := exec.Command("launchctl", "kickstart", "-k", domain()+"/"+Label).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl kickstart: %v: %s", err, strings.TrimSpace(string(b)))
		}
		return nil
	}
	var last error
	for i := 0; i < 40; i++ {
		b, err := exec.Command("launchctl", "bootstrap", domain(), plistPath()).CombinedOutput()
		if err == nil || loaded() {
			return nil
		}
		last = fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(b)))
		time.Sleep(250 * time.Millisecond)
	}
	return last
}

// Stop unloads the LaunchAgent (KeepAlive would otherwise restart it) and
// waits until launchd has finished tearing it down. It loads again at next
// login, or with `kith daemon start`.
func Stop(home string) error {
	if b, err := exec.Command("launchctl", "bootout", domain()+"/"+Label).CombinedOutput(); err != nil && loaded() {
		return fmt.Errorf("launchctl bootout: %v: %s", err, strings.TrimSpace(string(b)))
	}
	for i := 0; i < 80 && loaded(); i++ {
		time.Sleep(250 * time.Millisecond)
	}
	return nil
}

func MDNSRuleExists() bool { return true }
