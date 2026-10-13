package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"kith/internal/config"
)

// kithBinary finds the kith CLI the GUI drives for actions that live in the
// CLI (daemon start/install, doctor, config, submit). Packages ship it next
// to the GUI executable; otherwise PATH and the standard install locations.
func kithBinary() (string, error) {
	name := "kith"
	if runtime.GOOS == "windows" {
		name = "kith.exe"
	}
	var cands []string
	if exe, err := os.Executable(); err == nil {
		exe, _ = filepath.EvalSymlinks(exe)
		dir := filepath.Dir(exe)
		cands = append(cands, filepath.Join(dir, name))
		if runtime.GOOS == "darwin" { // Kith.app/Contents/MacOS/kith
			cands = append(cands, filepath.Join(dir, "..", "Resources", name))
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		cands = append(cands, p)
	}
	if h, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(h, ".local", "bin", name))
	}
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		cands = append(cands, filepath.Join(la, "Programs", "kith", name))
	}
	cands = append(cands, "/usr/local/bin/kith", "/usr/bin/kith", "/opt/homebrew/bin/kith")
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", errors.New("the kith command-line tool was not found; install it (it ships with this app) and restart")
}

// runKith runs the kith CLI with the GUI's home and returns its combined
// output. Commands never prompt: interactive steps are handled elsewhere.
func runKith(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	bin, err := kithBinary()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	full := append([]string{"--home", config.ResolveHome(""), "--no-color"}, args...)
	cmd := exec.CommandContext(ctx, bin, full...)
	hideConsole(cmd)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	s := strings.TrimSpace(out.String())
	if err != nil && s != "" {
		return s, errors.New(s)
	}
	return s, err
}
