package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"kith/internal/audit"
	"kith/internal/ipc"
	"kith/internal/protocol"
)

// pairClient is one side's `kith pair` session, driven step by step.
type pairClient struct {
	cn     *ipc.Conn
	codes  chan string
	events chan string
	result chan error
	cancel context.CancelFunc
}

func openPairClient(t *testing.T, n *node, ip string) *pairClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	cn, err := n.cl.Open(ctx, "pair", map[string]string{"ip": ip})
	if err != nil {
		cancel()
		t.Fatalf("%s: open pair: %v", n.name, err)
	}
	pc := &pairClient{cn: cn, codes: make(chan string, 4), events: make(chan string, 8), result: make(chan error, 1), cancel: cancel}
	go func() {
		var pv PeerView
		pc.result <- cn.Wait(func(ev string, data json.RawMessage) error {
			pc.events <- ev
			if ev == "code" {
				var e struct{ Code string }
				json.Unmarshal(data, &e)
				pc.codes <- e.Code
			}
			return nil
		}, &pv)
	}()
	t.Cleanup(func() { cn.Close(); cancel() })
	return pc
}

func (pc *pairClient) code(t *testing.T) string {
	t.Helper()
	select {
	case c := <-pc.codes:
		return c
	case <-time.After(20 * time.Second):
		t.Fatal("no pairing code")
		return ""
	}
}

func (pc *pairClient) confirm(ok bool, code string) error {
	return pc.cn.Send(map[string]any{"confirm": ok, "code": code})
}

// done waits for the session's result; within bounds the other side's
// answer must arrive promptly, not after the code expires.
func (pc *pairClient) done(t *testing.T, within time.Duration) error {
	t.Helper()
	select {
	case err := <-pc.result:
		return err
	case <-time.After(within):
		t.Fatalf("pairing did not end within %s", within)
		return nil
	}
}

// Both people choose each other: the two connections cross, and both ends
// must settle on one of them and pair.
func TestPairingCrossedDials(t *testing.T) {
	a := newNode(t, "mac", nil)
	b := newNode(t, "win", nil)
	pa := openPairClient(t, a, b.addr())
	pb := openPairClient(t, b, a.addr())
	ca, cb := pa.code(t), pb.code(t)
	// Codes from a superseded connection may arrive first; drain to the
	// last one shown on each side.
	settle := func(pc *pairClient, c string) string {
		for {
			select {
			case c = <-pc.codes:
			case <-time.After(1500 * time.Millisecond):
				return c
			}
		}
	}
	ca, cb = settle(pa, ca), settle(pb, cb)
	if ca != cb {
		t.Fatalf("sides show different codes: %s / %s", ca, cb)
	}
	if err := pa.confirm(true, ca); err != nil {
		t.Fatal(err)
	}
	if err := pb.confirm(true, cb); err != nil {
		t.Fatal(err)
	}
	if err := pa.done(t, 20*time.Second); err != nil {
		t.Fatalf("mac: %v", err)
	}
	if err := pb.done(t, 20*time.Second); err != nil {
		t.Fatalf("win: %v", err)
	}
	waitConnected(t, a, b)
}

// Closing the dialog on one machine tells the other one at once, as a
// cancel rather than a code mismatch, and doesn't count as a failed try.
func TestPairingCancelReachesPeer(t *testing.T) {
	a := newNode(t, "mac", nil)
	b := newNode(t, "win", nil)
	pb := openPairClient(t, b, "")
	waitFor(t, 10*time.Second, "pairing window", b.pairingOpen)
	pa := openPairClient(t, a, b.addr())
	code := pa.code(t)
	pb.code(t)
	if err := pa.confirm(true, code); err != nil {
		t.Fatal(err)
	}
	pb.cn.Close() // the Windows user closes the dialog
	err := pa.done(t, 10*time.Second)
	if protocol.CodeOf(err) != protocol.CodeCancelledByPeer || !strings.Contains(err.Error(), "cancelled on win") {
		t.Fatalf("want CANCELLED_BY_PEER from win, got %v", err)
	}
	if !b.d.lim.Allowed("127.0.0.1") || !a.d.lim.Allowed("127.0.0.1") {
		t.Fatal("a cancel was counted as a failed attempt")
	}
	for i := 0; i < 4; i++ { // more cancels never lock pairing
		pb := openPairClient(t, b, "")
		waitFor(t, 10*time.Second, "pairing window", b.pairingOpen)
		pa := openPairClient(t, a, b.addr())
		pa.code(t)
		pb.code(t)
		pb.cn.Close()
		pa.done(t, 10*time.Second)
		waitFor(t, 10*time.Second, "pairing window closed", func() bool { return !b.pairingOpen() })
	}
	pairNodes(t, a, b)
}

// "They don't match" on one machine ends the other side's wait at once.
func TestPairingRejectReachesPeer(t *testing.T) {
	a := newNode(t, "mac", nil)
	b := newNode(t, "win", nil)
	pb := openPairClient(t, b, "")
	waitFor(t, 10*time.Second, "pairing window", b.pairingOpen)
	pa := openPairClient(t, a, b.addr())
	pa.code(t)
	code := pb.code(t)
	if err := pb.confirm(false, code); err != nil {
		t.Fatal(err)
	}
	err := pa.done(t, 10*time.Second)
	if protocol.CodeOf(err) != protocol.CodeAuthFailed || !strings.Contains(err.Error(), "rejected by the user on win") {
		t.Fatalf("want the rejection from win, got %v", err)
	}
	if err := pb.done(t, 10*time.Second); protocol.CodeOf(err) != protocol.CodeAuthFailed {
		t.Fatalf("win: want AUTH_FAILED, got %v", err)
	}
}

// An answer about a code that isn't on screen is ignored; the first side
// to confirm hears that the other side confirmed.
func TestPairingStaleConfirmIgnored(t *testing.T) {
	a := newNode(t, "mac", nil)
	b := newNode(t, "win", nil)
	pb := openPairClient(t, b, "")
	waitFor(t, 10*time.Second, "pairing window", b.pairingOpen)
	pa := openPairClient(t, a, b.addr())
	ca, cb := pa.code(t), pb.code(t)
	if err := pa.confirm(false, "000000"); err != nil { // stale "no"
		t.Fatal(err)
	}
	if err := pa.confirm(true, ca); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for got := false; !got; {
		select {
		case ev := <-pb.events:
			got = ev == "peer-confirmed"
		case <-deadline:
			t.Fatal("win never heard that mac confirmed")
		}
	}
	if err := pb.confirm(true, cb); err != nil {
		t.Fatal(err)
	}
	if err := pa.done(t, 20*time.Second); err != nil {
		t.Fatalf("mac: %v", err)
	}
	if err := pb.done(t, 20*time.Second); err != nil {
		t.Fatalf("win: %v", err)
	}
	for _, e := range auditEvents(t, a) {
		if e.Event == audit.EvPairFail {
			t.Fatalf("stale confirm recorded a failure: %+v", e)
		}
	}
}
