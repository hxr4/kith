package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"kith/internal/config"
)

type cfgKey struct {
	name, kind, help string
	allowed          []string
}

var cfgKeys = []cfgKey{
	{"name", "string", "name shown to peers", nil},
	{"port", "int", "TCP port (default 7420)", nil},
	{"default_policy", "string", "policy for newly paired peers", []string{config.PolicyAsk, config.PolicyAuto}},
	{"log_level", "string", "daemon log level", []string{"debug", "info", "warn", "error"}},
	{"bind", "string", "listen address (default 0.0.0.0)", nil},
	{"mdns", "bool", "mDNS discovery on/off", []string{"true", "false"}},
}

func findKey(k string) (cfgKey, bool) {
	for _, c := range cfgKeys {
		if c.name == k {
			return c, true
		}
	}
	return cfgKey{}, false
}

func keyNames() []string {
	var out []string
	for _, k := range cfgKeys {
		out = append(out, k.name+"\t"+k.help)
	}
	return out
}

func effective(c *config.Config) map[string]string {
	return map[string]string{"name": c.Name, "port": strconv.Itoa(c.Port), "default_policy": c.DefaultPolicy,
		"log_level": c.LogLevel, "bind": c.Bind, "mdns": strconv.FormatBool(c.MDNSEnabled())}
}

func readRaw(path string) (map[string]any, error) {
	raw := map[string]any{}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return raw, nil
		}
		return nil, err
	}
	_, err = toml.Decode(string(b), &raw)
	return raw, err
}

func (a *app) configCmd() *cobra.Command {
	path := func() string { return filepath.Join(config.ResolveHome(a.home), config.FileName) }
	c := &cobra.Command{Use: "config", Short: "Show or change kith.toml settings (list|get|set|unset|path)"}
	list := &cobra.Command{Use: "list", Short: "Show all settings (effective values)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := config.ResolveHome(a.home)
			cfg, err := config.Load(home)
			if err != nil {
				return err
			}
			raw, _ := readRaw(path())
			eff := effective(cfg)
			if a.jsonOut {
				a.printJSON(eff)
				return nil
			}
			var rows [][]string
			for _, k := range cfgKeys {
				src := a.p.dim("default")
				if _, ok := raw[k.name]; ok {
					src = "kith.toml"
				}
				rows = append(rows, []string{k.name, eff[k.name], src, a.p.dim(k.help)})
			}
			a.table([]string{"KEY", "VALUE", "FROM", ""}, rows)
			return nil
		}}
	get := &cobra.Command{Use: "get <key>", Short: "Print one setting", Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return keyNames(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(config.ResolveHome(a.home))
			if err != nil {
				return err
			}
			v, ok := effective(cfg)[args[0]]
			if !ok {
				return fmt.Errorf("unknown key %q (keys: %s)", args[0], strings.Join(plainKeys(), ", "))
			}
			fmt.Fprintln(a.out, v)
			return nil
		}}
	write := func(raw map[string]any) error {
		home := config.ResolveHome(a.home)
		if err := config.EnsureHome(home); err != nil {
			return err
		}
		var buf bytes.Buffer
		keys := make([]string, 0, len(raw))
		for k := range raw {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ordered := map[string]any{}
		for _, k := range keys {
			ordered[k] = raw[k]
		}
		if err := toml.NewEncoder(&buf).Encode(ordered); err != nil {
			return err
		}
		tmp := path() + ".tmp"
		if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
			return err
		}
		// Validate with the real loader before replacing the file.
		probe, _ := os.MkdirTemp("", "kithcfg")
		defer os.RemoveAll(probe)
		os.WriteFile(filepath.Join(probe, config.FileName), buf.Bytes(), 0o600)
		if _, err := config.Load(probe); err != nil {
			os.Remove(tmp)
			return err
		}
		return os.Rename(tmp, path())
	}
	set := &cobra.Command{Use: "set <key> <value>", Short: "Change a setting (restart the daemon to apply)", Args: cobra.ExactArgs(2),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return keyNames(), cobra.ShellCompDirectiveNoFileComp
			}
			if k, ok := findKey(args[0]); ok && len(args) == 1 {
				return k.allowed, cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			k, ok := findKey(args[0])
			if !ok {
				return fmt.Errorf("unknown key %q (keys: %s)", args[0], strings.Join(plainKeys(), ", "))
			}
			var v any = args[1]
			switch k.kind {
			case "int":
				n, err := strconv.Atoi(args[1])
				if err != nil || n < 1 || n > 65535 {
					return fmt.Errorf("%s must be a port number", k.name)
				}
				v = n
			case "bool":
				b, err := strconv.ParseBool(args[1])
				if err != nil {
					return fmt.Errorf("%s must be true or false", k.name)
				}
				v = b
			}
			if k.allowed != nil && k.kind == "string" {
				okv := false
				for _, al := range k.allowed {
					if strings.EqualFold(al, args[1]) {
						okv = true
						v = al
					}
				}
				if !okv {
					return fmt.Errorf("%s must be one of %s", k.name, strings.Join(k.allowed, ", "))
				}
			}
			raw, err := readRaw(path())
			if err != nil {
				return err
			}
			raw[k.name] = v
			if err := write(raw); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "%s = %v\n", k.name, v)
			if _, running := a.daemonRunning(cmd.Context()); running {
				fmt.Fprintln(a.out, "Restart the daemon to apply: kith daemon restart")
			}
			return nil
		}}
	unset := &cobra.Command{Use: "unset <key>", Short: "Revert a setting to its default", Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return keyNames(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readRaw(path())
			if err != nil {
				return err
			}
			delete(raw, args[0])
			if err := write(raw); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "%s reset to default\n", args[0])
			return nil
		}}
	p := &cobra.Command{Use: "path", Short: "Print the config file and home directory paths", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(a.out, path())
			fmt.Fprintln(a.out, config.ResolveHome(a.home))
			return nil
		}}
	c.AddCommand(list, get, set, unset, p)
	return c
}

func plainKeys() []string {
	var out []string
	for _, k := range cfgKeys {
		out = append(out, k.name)
	}
	return out
}
