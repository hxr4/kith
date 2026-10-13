package main

import (
	"html"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Open-at-login for the GUI (starts hidden in the tray). The daemon has
// its own service (kith daemon install); this only concerns the app.

func guiExe() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe
}

func macLoginPlist() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "Library", "LaunchAgents", "dev.kith.gui.plist")
}

func linuxAutostartFile() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		h, _ := os.UserHomeDir()
		dir = filepath.Join(h, ".config")
	}
	return filepath.Join(dir, "autostart", "kith-gui.desktop")
}

func autostartEnabled() bool {
	switch runtime.GOOS {
	case "darwin":
		_, err := os.Stat(macLoginPlist())
		return err == nil
	case "linux":
		_, err := os.Stat(linuxAutostartFile())
		return err == nil
	case "windows":
		return winAutostartEnabled()
	}
	return false
}

func setAutostart(on bool) error {
	exe := guiExe()
	switch runtime.GOOS {
	case "darwin":
		p := macLoginPlist()
		if !on {
			return removeIfExists(p)
		}
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>dev.kith.gui</string>
  <key>ProgramArguments</key>
  <array><string>` + html.EscapeString(exe) + `</string><string>--hidden</string></array>
  <key>RunAtLoad</key><true/>
  <key>LimitLoadToSessionType</key><string>Aqua</string>
</dict>
</plist>
`
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(plist), 0o644)
	case "linux":
		p := linuxAutostartFile()
		if !on {
			return removeIfExists(p)
		}
		desktop := "[Desktop Entry]\nType=Application\nName=Kith\nComment=Kith tray\nExec=" +
			strings.ReplaceAll(exe, " ", `\s`) + " --hidden\nIcon=kith-gui\nX-GNOME-Autostart-enabled=true\n"
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(desktop), 0o644)
	case "windows":
		return winSetAutostart(on, exe)
	}
	return nil
}

func removeIfExists(p string) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Autostart reports whether Kith opens (in the tray) at login.
func (s *KithService) Autostart() bool { return autostartEnabled() }

// SetAutostart turns open-at-login on or off.
func (s *KithService) SetAutostart(on bool) error { return setAutostart(on) }
