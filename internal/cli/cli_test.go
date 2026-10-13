package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVPNNames(t *testing.T) {
	for _, n := range []string{"utun3", "wg0", "tun0", "ppp0", "Tailscale", "WireGuard Tunnel"} {
		if !isVPNName(n) {
			t.Error(n)
		}
	}
	for _, n := range []string{"en0", "eth0", "Wi-Fi", "Ethernet"} {
		if isVPNName(n) {
			t.Error(n)
		}
	}
}

func TestCommandSurface(t *testing.T) {
	a := &app{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	root := a.root()
	want := []string{"daemon", "pair", "peers", "unpair", "id", "probe", "submit", "tasks", "task", "logs", "cancel",
		"artifacts", "fetch", "approve", "deny", "doctor", "mcp"}
	have := map[string]bool{}
	for _, c := range root.Commands() {
		have[strings.Fields(c.Use)[0]] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("missing command %s", w)
		}
	}
	d, _, _ := root.Find([]string{"daemon", "install"})
	if d == nil || d.Name() != "install" {
		t.Error("daemon install missing")
	}
}

func TestDaemonNotRunningIsReported(t *testing.T) {
	var out, errOut bytes.Buffer
	home := t.TempDir() // never the real ~/.kith, which may have a live daemon
	t.Setenv("KITH_HOME", home)
	a := &app{out: &out, errOut: &errOut}
	root := a.root()
	root.SetArgs([]string{"--home", home, "tasks"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatal(err)
	}
}
