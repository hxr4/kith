// Package discovery advertises and browses _kith._tcp.local. via mDNS.
// mDNS is best-effort: the daemon always also re-probes stored last_addrs
// and supports manual IPs.
package discovery

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/mdns"

	"kith/internal/identity"
)

const Service = "_kith._tcp"

// Entry is one discovered Kith daemon.
type Entry struct {
	Instance  string `json:"instance"`
	Name      string `json:"name"`
	FPDisplay string `json:"fp"`
	Mode      string `json:"mode"`
	Addr      string `json:"addr"` // ip:port
}

// MatchesFP reports whether the entry advertises (a prefix of) fullFP.
func (e Entry) MatchesFP(fullFP string) bool {
	n := identity.NormalizeFP(e.FPDisplay)
	return n != "" && strings.HasPrefix(fullFP, n)
}

// TXT builds the TXT records (§8).
func TXT(name, fp, mode string) []string {
	return []string{"v=1", "fp=" + identity.Display(fp), "name=" + name, "mode=" + mode}
}

// ParseTXT turns key=value records into a map.
func ParseTXT(fields []string) map[string]string {
	m := map[string]string{}
	for _, f := range fields {
		if k, v, ok := strings.Cut(f, "="); ok {
			m[k] = v
		}
	}
	return m
}

// FromService converts an mDNS answer; ok=false if it is not a v=1 Kith entry.
func FromService(s *mdns.ServiceEntry) (Entry, bool) {
	if s == nil || s.AddrV4 == nil || s.Port == 0 {
		return Entry{}, false
	}
	t := ParseTXT(s.InfoFields)
	if t["v"] != "1" || t["fp"] == "" {
		return Entry{}, false
	}
	return Entry{
		Instance:  s.Name,
		Name:      t["name"],
		FPDisplay: t["fp"],
		Mode:      t["mode"],
		Addr:      net.JoinHostPort(s.AddrV4.String(), strconv.Itoa(s.Port)),
	}, true
}

var quiet = log.New(io.Discard, "", 0)

// Advertiser runs an mDNS responder.
type Advertiser struct {
	srv *mdns.Server
}

// Advertise starts answering for this daemon.
func Advertise(name, fp string, port int, mode string) (*Advertiser, error) {
	ips := LocalIPv4s()
	if len(ips) == 0 {
		return nil, fmt.Errorf("no non-loopback IPv4 address")
	}
	inst := "kith-" + fp[:12]
	svc, err := mdns.NewMDNSService(inst, Service, "", inst+".local.", port, ips, TXT(name, fp, mode))
	if err != nil {
		return nil, err
	}
	srv, err := mdns.NewServer(&mdns.Config{Zone: svc, Logger: quiet})
	if err != nil {
		return nil, err
	}
	return &Advertiser{srv: srv}, nil
}

func (a *Advertiser) Close() error {
	if a == nil || a.srv == nil {
		return nil
	}
	return a.srv.Shutdown()
}

// Browser finds Kith daemons. MDNS is the real one; tests inject fakes.
type Browser interface {
	Browse(ctx context.Context, timeout time.Duration) ([]Entry, error)
}

type MDNS struct{}

func (MDNS) Browse(ctx context.Context, timeout time.Duration) ([]Entry, error) {
	ch := make(chan *mdns.ServiceEntry, 32)
	var out []Entry
	seen := map[string]bool{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for s := range ch {
			if e, ok := FromService(s); ok && !seen[e.Instance+e.Addr] {
				seen[e.Instance+e.Addr] = true
				out = append(out, e)
			}
		}
	}()
	err := mdns.Query(&mdns.QueryParam{Service: Service, Domain: "local", Timeout: timeout, Entries: ch,
		DisableIPv6: true, Logger: quiet})
	close(ch)
	<-done
	return out, err
}

// LocalIPv4s lists non-loopback IPv4 addresses on up interfaces.
func LocalIPv4s() []net.IP {
	var out []net.IP
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				out = append(out, n.IP.To4())
			}
		}
	}
	return out
}
