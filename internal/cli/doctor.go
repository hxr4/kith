package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"kith/internal/config"
	"kith/internal/daemon"
	"kith/internal/discovery"
	"kith/internal/svc"
)

type check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail | info
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

func (a *app) doctorCmd() *cobra.Command {
	return &cobra.Command{Use: "doctor", Short: "Diagnose networking, firewall and daemon problems", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := a.doctor(cmd.Context())
			if a.jsonOut {
				a.printJSON(checks)
				return nil
			}
			bad := false
			for _, c := range checks {
				mark := map[string]string{"ok": "[ OK ]", "warn": "[WARN]", "fail": "[FAIL]", "info": "[INFO]"}[c.Status]
				fmt.Fprintf(a.out, "%s %-22s %s\n", mark, c.Name, c.Detail)
				if c.Fix != "" {
					fmt.Fprintf(a.out, "       %-22s fix: %s\n", "", c.Fix)
				}
				if c.Status == "fail" {
					bad = true
				}
			}
			if bad {
				return errSilent
			}
			return nil
		}}
}

func isVPNName(n string) bool {
	n = strings.ToLower(n)
	for _, p := range []string{"utun", "tun", "tap", "wg", "ppp", "ipsec", "vpn", "wireguard", "tailscale", "zerotier"} {
		if strings.HasPrefix(n, p) || strings.Contains(n, p) {
			return true
		}
	}
	return false
}

func (a *app) doctor(ctx context.Context) []check {
	var out []check
	add := func(name, st, detail, fix string) { out = append(out, check{name, st, detail, fix}) }

	// 1. Daemon.
	var info daemon.DoctorInfo
	derr := a.client().Call(ctx, "doctor", nil, &info)
	if derr != nil {
		add("daemon", "fail", "not running", "start it with `kith daemon` or `kith daemon install`")
	} else {
		add("daemon", "ok", fmt.Sprintf("running, pid %d, %s (%s), TCP %d", info.Status.PID, info.Status.Display, info.Status.Name, info.Status.Port), "")
	}
	// 2. IP present + subnet.
	type lan struct {
		ip  net.IP
		net *net.IPNet
		ifc string
	}
	var lans []lan
	var vpns []string
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, ad := range addrs {
			n, ok := ad.(*net.IPNet)
			if !ok || n.IP.To4() == nil || n.IP.IsLinkLocalUnicast() {
				continue
			}
			if isVPNName(i.Name) {
				vpns = append(vpns, i.Name+" "+n.String())
				continue
			}
			lans = append(lans, lan{n.IP.To4(), n, i.Name})
		}
	}
	if len(lans) == 0 {
		add("lan address", "fail", "no non-loopback IPv4 address", "connect both machines to the same LAN / Wi-Fi")
	} else {
		var s []string
		for _, l := range lans {
			s = append(s, fmt.Sprintf("%s %s", l.ifc, l.net.String()))
		}
		add("lan address", "ok", strings.Join(s, ", "), "")
	}
	// 3. VPN route warning.
	if len(vpns) > 0 {
		add("vpn", "warn", "VPN-like interfaces with IPv4: "+strings.Join(vpns, ", "),
			"a full-tunnel VPN can route LAN traffic away; disconnect it or allow local network access")
	} else {
		add("vpn", "ok", "no VPN interfaces with IPv4 addresses", "")
	}
	port := config.DefaultPort
	if derr == nil {
		port = info.Status.Port
	}
	// 4. Listener reachable on the LAN address (own port).
	if derr == nil && len(lans) > 0 {
		addr := net.JoinHostPort(lans[0].ip.String(), strconv.Itoa(port))
		c, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			add("listener", "fail", fmt.Sprintf("cannot connect to own %s: %v", addr, err),
				"check `bind` in kith.toml and local firewall software")
		} else {
			c.Close()
			add("listener", "ok", "TCP "+addr+" accepts connections", "")
		}
	}
	// 5. Firewall rule (Windows).
	if runtime.GOOS == "windows" {
		if ok, _ := svc.FirewallRuleExists(); ok {
			add("firewall", "ok", "inbound rule for TCP 7420 (Private profile) present", "")
		} else {
			add("firewall", "fail", "no inbound firewall rule for TCP 7420",
				`run "kith daemon firewall" from an elevated terminal (the desktop app: Settings > Network > Add firewall rules)`)
		}
		if svc.MDNSRuleExists() {
			add("firewall mdns", "ok", "inbound mDNS (UDP 5353) rule for kith.exe present", "")
		} else {
			add("firewall mdns", "warn", "no inbound UDP 5353 rule for kith.exe: the Mac will not discover this PC via mDNS",
				`run "kith daemon firewall" from an elevated terminal; without it peers must pair by IP address`)
		}
		profiles, err := svc.NetworkProfiles()
		var public []string
		for _, p := range profiles {
			if name, cat, ok := strings.Cut(p, "="); ok && strings.EqualFold(cat, "Public") {
				public = append(public, name)
			}
		}
		switch {
		case err != nil || len(profiles) == 0:
			add("network profile", "info", "the firewall rule only applies on Private networks",
				"Settings > Network & internet > (your network) > Network profile type: Private")
		case len(public) > 0:
			add("network profile", "warn", "network "+strings.Join(public, ", ")+" is Public: the firewall rule only applies on Private networks",
				"Settings > Network & internet > (your network) > Network profile type: Private. Kith picks up the change without a restart.")
		default:
			add("network profile", "ok", "Private ("+strings.Join(profiles, ", ")+")", "")
		}
	}
	// Python and GPU, as this machine advertises them to peers.
	if derr == nil {
		if len(info.Caps.Python) == 0 {
			fix := "install Python 3 and make sure the daemon's PATH finds it (restart the daemon after installing)"
			if runtime.GOOS == "windows" {
				fix = "install Python from python.org, then turn off the python.exe alias in Settings > Apps > Advanced app settings > App execution aliases (the Microsoft Store stub does not count)"
			}
			add("python", "warn", "no Python interpreter found on the daemon's PATH", fix)
		} else {
			add("python", "ok", "Python "+strings.Join(info.Caps.Python, ", "), "")
		}
		if len(info.Caps.GPUs) == 0 {
			add("nvidia-smi", "info", "no NVIDIA GPU visible (nvidia-smi missing or no GPU)",
				"tasks that require a GPU will be rejected here; install the NVIDIA driver if this machine has one")
		} else {
			var gs []string
			for _, g := range info.Caps.GPUs {
				gs = append(gs, fmt.Sprintf("%s %.1f GB", g.Name, g.VRAMGB))
			}
			add("nvidia-smi", "ok", strings.Join(gs, ", "), "")
		}
	}
	// 6. Peers: reachability + AP-isolation heuristic.
	if derr == nil {
		if len(info.Peers) == 0 {
			add("peers", "info", "no paired peers yet", "run `kith pair` on both machines (add --ip <addr> on one if discovery fails)")
		}
		for _, p := range info.Peers {
			name := "peer " + p.Name
			if p.Reachable {
				add(name, "ok", "connected via "+p.LastAddr, "")
				continue
			}
			if p.LastAddr == "" {
				add(name, "warn", "offline; no known address", "make sure its daemon runs; mDNS will find it on the same LAN")
				continue
			}
			c, err := net.DialTimeout("tcp", p.LastAddr, 3*time.Second)
			if err == nil {
				c.Close()
				add(name, "warn", "TCP "+p.LastAddr+" reachable but not connected (reconnect pending, or identity mismatch - see daemon log)", "")
				continue
			}
			host, _, _ := net.SplitHostPort(p.LastAddr)
			sameSubnet := false
			for _, l := range lans {
				if ip := net.ParseIP(host); ip != nil && l.net.Contains(ip) {
					sameSubnet = true
				}
			}
			detail := fmt.Sprintf("cannot reach %s: %v", p.LastAddr, err)
			fix := "start the kith daemon there and check its firewall (`kith doctor` on that machine)"
			if errors.Is(err, syscall.EHOSTUNREACH) || strings.Contains(err.Error(), "no route") || strings.Contains(err.Error(), "unreachable") {
				fix = "host unreachable: if both machines are on the same Wi-Fi, the router may isolate clients (AP/client isolation, common on guest networks) - use a non-guest network or Ethernet"
			} else if sameSubnet && strings.Contains(err.Error(), "timeout") {
				fix = "same subnet but connection timed out: check the peer's firewall (Windows: Private profile rule) or AP/client isolation on the Wi-Fi"
			}
			if !sameSubnet {
				detail += " (different subnet than this machine)"
			}
			add(name, "fail", detail, fix)
		}
	}
	// 7. mDNS.
	es, err := discovery.MDNS{}.Browse(ctx, 2*time.Second)
	switch {
	case err != nil:
		add("mdns", "warn", "mDNS query failed: "+err.Error(), "discovery is best-effort; use `kith pair --ip <addr>`")
	default:
		others := 0
		for _, e := range es {
			if derr == nil && e.MatchesFP(info.Status.FP) {
				continue
			}
			others++
		}
		add("mdns", "ok", fmt.Sprintf("query OK, %d other kith daemon(s) visible", others), "")
	}
	// 8. macOS Local Network permission hint.
	if runtime.GOOS == "darwin" {
		add("local network", "info", "macOS requires Local Network permission for LAN connections",
			"System Settings > Privacy & Security > Local Network > enable kith (or your terminal app)")
	}
	return out
}
