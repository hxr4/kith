package cli

import (
	"testing"

	"kith/internal/daemon"
)

func TestPickPeer(t *testing.T) {
	peers := []daemon.PeerView{
		{FP: "88ee257ad23b41fd", Display: "KITH-88EE-257A-D23B-41FD", Name: "HARI-PC"},
		{FP: "3f1a9c0277d4b8e1", Display: "KITH-3F1A-9C02-77D4-B8E1", Name: "studio-mini"},
	}
	for ref, want := range map[string]string{"hari-pc": "HARI-PC", "KITH-3F1A": "studio-mini", "88ee": "HARI-PC"} {
		p, err := pickPeer(peers, ref)
		if err != nil || p.Name != want {
			t.Errorf("pickPeer(%q) = %q, %v; want %q", ref, p.Name, err, want)
		}
	}
	if _, err := pickPeer(peers, ""); err == nil {
		t.Error("an empty ref with two peers must ask for --peer")
	}
	if p, err := pickPeer(peers[:1], ""); err != nil || p.Name != "HARI-PC" {
		t.Errorf("an empty ref with one peer picks it: %v %v", p.Name, err)
	}
	if _, err := pickPeer(peers, "nope"); err == nil {
		t.Error("unknown ref must fail")
	}
}
