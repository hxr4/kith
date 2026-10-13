package svc

import (
	"strings"
	"testing"
)

func TestSystemdUnit(t *testing.T) {
	u := SystemdUnit("/home/pi/.local/bin/kith", "/home/pi/my kith %home", "/usr/bin:/home/pi/.local/bin")
	for _, want := range []string{
		`ExecStart="/home/pi/.local/bin/kith" daemon --home "/home/pi/my kith %%home"`,
		`Environment="PATH=/usr/bin:/home/pi/.local/bin"`,
		"Restart=on-failure", "KillMode=mixed", "WantedBy=default.target",
		"StandardOutput=append:/home/pi/my kith %%home/daemon.log",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit lacks %q\n%s", want, u)
		}
	}
	if !unitServesHome(u, "/home/pi/my kith %home") || unitServesHome(u, "/home/pi/other") {
		t.Fatal("home matching")
	}
}
