package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsAndOverrides(t *testing.T) {
	home := t.TempDir()
	c, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != DefaultPort || c.DefaultPolicy != PolicyAsk || c.LogLevel != "info" || c.Name == "" || !c.MDNSEnabled() || c.Bind != "0.0.0.0" {
		t.Fatalf("%+v", c)
	}
	os.WriteFile(filepath.Join(home, FileName), []byte("port = 9000\nname = \"gpu-box\"\ndefault_policy = \"AUTO\"\nlog_level = \"debug\"\nmdns = false\n"), 0o600)
	c, err = Load(home)
	if err != nil || c.Port != 9000 || c.Name != "gpu-box" || c.DefaultPolicy != PolicyAuto || c.MDNSEnabled() {
		t.Fatalf("%+v %v", c, err)
	}
	os.WriteFile(filepath.Join(home, FileName), []byte("default_policy = \"yolo\"\n"), 0o600)
	if _, err := Load(home); err == nil {
		t.Fatal("bad policy accepted")
	}
}

func TestResolveHome(t *testing.T) {
	t.Setenv("KITH_HOME", "/tmp/from-env")
	if ResolveHome("") != filepath.Clean("/tmp/from-env") && ResolveHome("") == "" {
		t.Fatal("env ignored")
	}
	if h := ResolveHome("/tmp/flag"); h != filepath.Clean("/tmp/flag") && filepath.Base(h) != "flag" {
		t.Fatal("flag must win over env:", h)
	}
}
