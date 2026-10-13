// Package config resolves KITH_HOME and loads kith.toml.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	DefaultPort   = 7420
	FileName      = "kith.toml"
	PolicyAsk     = "ask"
	PolicyAuto    = "auto"
	KithVersion   = "1.0.0"
	ProtocolMajor = 1
)

// Config is the content of $KITH_HOME/kith.toml.
type Config struct {
	Port          int    `toml:"port"`
	Name          string `toml:"name"`
	DefaultPolicy string `toml:"default_policy"`
	LogLevel      string `toml:"log_level"`
	// Bind is the TCP listen address (default all interfaces). See DECISIONS.md D-030.
	Bind string `toml:"bind"`
	// MDNS toggles mDNS advertise/browse (default true). See DECISIONS.md D-030.
	MDNS *bool `toml:"mdns"`
}

// DefaultHome returns ~/.kith unless $KITH_HOME is set.
func DefaultHome() string {
	if h := os.Getenv("KITH_HOME"); h != "" {
		return h
	}
	return StandardHome()
}

// StandardHome is ~/.kith regardless of environment.
func StandardHome() string {
	u, err := os.UserHomeDir()
	if err != nil || u == "" {
		return ".kith"
	}
	return filepath.Join(u, ".kith")
}

// ResolveHome picks flag > env > default and makes it absolute.
func ResolveHome(flag string) string {
	h := flag
	if h == "" {
		h = DefaultHome()
	}
	if abs, err := filepath.Abs(h); err == nil {
		h = abs
	}
	return h
}

// EnsureHome creates the home directory with 0700 permissions.
func EnsureHome(home string) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	return os.Chmod(home, 0o700)
}

// Load reads kith.toml from home, applying defaults. A missing file is fine.
func Load(home string) (*Config, error) {
	c := &Config{}
	b, err := os.ReadFile(filepath.Join(home, FileName))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if _, err := toml.Decode(string(b), c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", FileName, err)
		}
	}
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.Name == "" {
		h, _ := os.Hostname()
		c.Name = strings.TrimSuffix(h, ".local")
		if c.Name == "" {
			c.Name = "kith"
		}
	}
	c.DefaultPolicy = strings.ToLower(c.DefaultPolicy)
	if c.DefaultPolicy == "" {
		c.DefaultPolicy = PolicyAsk
	}
	if c.DefaultPolicy != PolicyAsk && c.DefaultPolicy != PolicyAuto {
		return nil, fmt.Errorf("default_policy must be %q or %q", PolicyAsk, PolicyAuto)
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.Bind == "" {
		c.Bind = "0.0.0.0"
	}
	return c, nil
}

// MDNSEnabled reports whether mDNS should run.
func (c *Config) MDNSEnabled() bool { return c.MDNS == nil || *c.MDNS }

// SlogLevel maps log_level to slog.Level.
func (c *Config) SlogLevel() slog.Level {
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
