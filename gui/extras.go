package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"kith/internal/config"
	"kith/internal/discovery"
	"kith/internal/svc"
)

// AppInfo is what the frontend needs to pick chrome, theme and copy.
type AppInfo struct {
	Platform string `json:"platform"` // windows | linux | darwin
	Chrome   string `json:"chrome"`   // windows | linux | mac (window decorations drawn by the app)
	Lite     bool   `json:"lite"`
	Mica     bool   `json:"mica"`
	Home     string `json:"home"`
	User     string `json:"user"`
	Version  string `json:"version"`
	LogPath  string `json:"log_path"`
}

func (s *KithService) Info() AppInfo {
	home := config.ResolveHome("")
	u := ""
	if cu, err := user.Current(); err == nil {
		u = cu.Username
		if i := strings.LastIndexAny(u, `\/`); i >= 0 { // DOMAIN\user
			u = u[i+1:]
		}
	}
	return AppInfo{Platform: runtime.GOOS, Chrome: chrome(), Lite: isLite(), Mica: chrome() == "windows" && micaSupported(),
		Home: home, User: u, Version: config.KithVersion, LogPath: filepath.Join(home, "daemon.log")}
}

// chrome selects the window decorations the app draws itself. Windows gets
// a frameless window with a Fluent title bar; Linux keeps native
// decorations with an in-app header bar. KITH_GUI_CHROME overrides it (for
// previewing the Windows chrome on another OS).
func chrome() string {
	switch v := os.Getenv("KITH_GUI_CHROME"); v {
	case "windows", "linux", "mac":
		return v
	}
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "mac"
	}
	return "linux"
}

// Check parses a task file and checks it against a peer (kith check).
func (s *KithService) Check(path, peer string) (json.RawMessage, error) {
	args := []string{"check", "-f", path}
	if peer != "" {
		args = append(args, "--peer", peer)
	}
	return jsonFromCLI(2*time.Minute, args...)
}

// ResolveTaskFile turns a dropped file or folder into a task file path.
func (s *KithService) ResolveTaskFile(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		if strings.EqualFold(filepath.Ext(path), ".toml") {
			return path, nil
		}
		return "", errors.New("drop a kith.toml task file, or a folder that contains one")
	}
	for _, n := range []string{"kith.toml", "task.toml"} {
		if _, err := os.Stat(filepath.Join(path, n)); err == nil {
			return filepath.Join(path, n), nil
		}
	}
	m, _ := filepath.Glob(filepath.Join(path, "*.toml"))
	if len(m) == 1 {
		return m[0], nil
	}
	return "", fmt.Errorf("no kith.toml in %s", filepath.Base(path))
}

// Found is a Kith daemon advertising on the LAN (pairing: Choose step).
type Found struct {
	Name   string `json:"name"`
	FP     string `json:"fp"`
	Addr   string `json:"addr"`
	Mode   string `json:"mode"`
	Paired bool   `json:"paired"`
}

// Discover browses mDNS for other Kith daemons for ~2 s.
func (s *KithService) Discover() ([]Found, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var st statusLite
	var peers []peerLite
	s.cl.Call(ctx, "status", nil, &st)
	s.cl.Call(ctx, "peers", nil, &peers)
	es, err := discovery.MDNS{}.Browse(ctx, 2*time.Second)
	if err != nil {
		return nil, err
	}
	out := []Found{}
	seen := map[string]bool{}
	for _, e := range es {
		if st.FP != "" && e.MatchesFP(st.FP) {
			continue
		}
		if seen[e.FPDisplay] {
			continue
		}
		seen[e.FPDisplay] = true
		f := Found{Name: e.Name, FP: e.FPDisplay, Addr: e.Addr, Mode: e.Mode}
		for _, p := range peers {
			if e.MatchesFP(p.FP) {
				f.Paired = true
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// CLIInfo says whether `kith` resolves for new terminals.
type CLIInfo struct {
	OnPath bool   `json:"on_path"`
	Path   string `json:"path"`
}

func (s *KithService) CLIStatus() CLIInfo {
	bin, _ := kithBinary()
	if runtime.GOOS == "windows" {
		return CLIInfo{OnPath: winDirOnPath(filepath.Dir(bin)), Path: bin}
	}
	if p, err := exec.LookPath("kith"); err == nil {
		return CLIInfo{OnPath: true, Path: p}
	}
	return CLIInfo{Path: bin}
}

// AddToPath adds the bundled CLI's folder to the user PATH (Windows).
func (s *KithService) AddToPath() (string, error) {
	bin, err := kithBinary()
	if err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" {
		return "", errors.New("on this OS the package installs kith on PATH")
	}
	if err := winAddToPath(filepath.Dir(bin)); err != nil {
		return "", err
	}
	return "Added to your user PATH. Open a new terminal to use kith.", nil
}

// AddFirewallRules runs `kith daemon firewall` in an Administrator window
// (Windows asks through UAC; the app never handles credentials).
func (s *KithService) AddFirewallRules() (string, error) {
	if runtime.GOOS != "windows" {
		return "No firewall rules are needed on this OS.", nil
	}
	bin, err := kithBinary()
	if err != nil {
		return "", err
	}
	ps := fmt.Sprintf(`Start-Process -Verb RunAs -Wait -WindowStyle Hidden -FilePath '%s' -ArgumentList 'daemon','firewall'`,
		strings.ReplaceAll(bin, "'", "''"))
	cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
	hideConsole(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("the Administrator step was cancelled or failed: %v %s", err, strings.TrimSpace(string(out)))
	}
	if ok, _ := svc.FirewallRuleExists(); !ok {
		return "", errors.New("the firewall rules were not added (the Administrator window may have been cancelled)")
	}
	return "Firewall rules added for Private networks", nil
}

// ServiceStatus is the OS service state for this home ("not installed",
// "installed, running", ...).
func (s *KithService) ServiceStatus() string {
	st, _ := svc.Status(config.ResolveHome(""))
	return st
}

// OpenPath opens a file or folder with its default app.
func (s *KithService) OpenPath(path string) error { return s.app.Browser.OpenFile(path) }

// ShowMain brings the main window forward at a route and hides the flyout.
func (s *KithService) ShowMain(route string) {
	s.HideFlyout()
	showWindow(s.app, route)
}

func (s *KithService) HideFlyout() {
	if w, ok := s.app.Window.Get("flyout"); ok {
		w.Hide()
	}
}

// FitFlyout resizes the tray flyout to its content, keeping its bottom edge
// (the tray side) where it is.
func (s *KithService) FitFlyout(height int) {
	w, ok := s.app.Window.Get("flyout")
	if !ok || height < 120 {
		return
	}
	if height > 760 {
		height = 760
	}
	ww, oh := w.Size()
	if oh == height {
		return
	}
	x, y := w.Position()
	w.SetSize(ww, height)
	if runtime.GOOS == "windows" {
		w.SetPosition(x, y+oh-height)
	}
}

func (s *KithService) Quit() { s.app.Quit() }
