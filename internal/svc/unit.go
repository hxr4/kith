package svc

import (
	"strings"
)

// systemdQuote quotes one word for an ExecStart= line and escapes '%'
// (systemd specifiers) so arbitrary paths are safe.
func systemdQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "%", "%%")
	return `"` + s + `"`
}

// SystemdUnit renders the user unit that runs `kith daemon` for home.
// KillMode=mixed sends SIGTERM to the daemon only, so its graceful
// shutdown marks running tasks INTERRUPTED and stops their process groups
// itself; anything left is SIGKILLed after TimeoutStopSec.
func SystemdUnit(exe, home, path string) string {
	return `[Unit]
Description=Kith task fabric daemon
Documentation=https://github.com/hxr4/kith
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=` + systemdQuote(exe) + ` daemon --home ` + systemdQuote(home) + `
Environment=` + systemdQuote("PATH="+path) + `
Restart=on-failure
RestartSec=5
KillMode=mixed
TimeoutStopSec=30
StandardOutput=append:` + strings.ReplaceAll(home, "%", "%%") + `/daemon.log
StandardError=append:` + strings.ReplaceAll(home, "%", "%%") + `/daemon.log

[Install]
WantedBy=default.target
`
}

// unitServesHome reports whether a rendered unit runs the daemon for home.
func unitServesHome(unit, home string) bool {
	return strings.Contains(unit, " daemon --home "+systemdQuote(home))
}
