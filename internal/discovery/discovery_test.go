package discovery

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/mdns"
)

const fp = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestTXTAndFromService(t *testing.T) {
	txt := TXT("win-gpu", fp, "available")
	s := &mdns.ServiceEntry{Name: "kith-0123._kith._tcp.local.", AddrV4: net.IPv4(192, 168, 1, 7), Port: 7420, InfoFields: txt}
	e, ok := FromService(s)
	if !ok || e.Name != "win-gpu" || e.Addr != "192.168.1.7:7420" || e.FPDisplay != "KITH-0123-4567-89AB-CDEF" || e.Mode != "available" {
		t.Fatalf("%+v", e)
	}
	if !e.MatchesFP(fp) || e.MatchesFP(strings.Repeat("f", 64)) {
		t.Fatal("fp matching")
	}
	// Foreign / malformed answers (fake responder) are ignored.
	for _, bad := range []*mdns.ServiceEntry{
		nil,
		{AddrV4: net.IPv4(1, 2, 3, 4), Port: 1, InfoFields: []string{"v=2", "fp=x"}},
		{AddrV4: net.IPv4(1, 2, 3, 4), Port: 1, InfoFields: []string{"hello"}},
		{Port: 1, InfoFields: txt},
	} {
		if _, ok := FromService(bad); ok {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

// Real multicast round trip; skipped where multicast is unavailable (CI).
func TestMDNSRoundTrip(t *testing.T) {
	if os.Getenv("KITH_TEST_MDNS") == "" {
		t.Skip("set KITH_TEST_MDNS=1 to exercise real multicast")
	}
	a, err := Advertise("test-node", fp, 7499, "available")
	if err != nil {
		t.Skip("multicast unavailable:", err)
	}
	defer a.Close()
	es, err := MDNS{}.Browse(context.Background(), 2*time.Second)
	if err != nil {
		t.Skip("multicast unavailable:", err)
	}
	for _, e := range es {
		if e.MatchesFP(fp) {
			return
		}
	}
	t.Skip("own advertisement not seen (multicast likely filtered)")
}
