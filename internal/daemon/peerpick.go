package daemon

import (
	"fmt"
	"strings"
)

// PickPeer resolves a peer reference: a name, or a fingerprint (or its
// display form) prefix. An empty ref means "the" peer: the only paired one,
// or the only one online when several are paired.
func PickPeer(peers []PeerView, ref string) (PeerView, error) {
	if ref == "" {
		if len(peers) == 1 {
			return peers[0], nil
		}
		if len(peers) == 0 {
			return PeerView{}, fmt.Errorf("no paired peers; pair a machine first")
		}
		var online []PeerView
		for _, p := range peers {
			if p.Reachable {
				online = append(online, p)
			}
		}
		if len(online) == 1 {
			return online[0], nil
		}
		names := make([]string, len(peers))
		for i, p := range peers {
			names[i] = p.Name
		}
		return PeerView{}, fmt.Errorf("%d peers are paired (%s); name one", len(peers), strings.Join(names, ", "))
	}
	norm := func(s string) string {
		return strings.ToLower(strings.NewReplacer("-", "", "KITH", "", "kith", "").Replace(s))
	}
	var hits []PeerView
	for _, p := range peers {
		if strings.EqualFold(p.Name, ref) || strings.HasPrefix(p.FP, norm(ref)) || strings.HasPrefix(norm(p.Display), norm(ref)) {
			hits = append(hits, p)
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	if len(hits) == 0 {
		return PeerView{}, fmt.Errorf("no paired peer matches %q", ref)
	}
	return PeerView{}, fmt.Errorf("%q matches %d peers; use the fingerprint", ref, len(hits))
}
