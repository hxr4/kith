package pairing

import (
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"kith/internal/identity"
	"kith/internal/protocol"
)

type pipeCh struct {
	in  chan protocol.Envelope
	out chan protocol.Envelope
}

func (p pipeCh) Send(typ string, v any) error {
	b, _ := json.Marshal(v)
	select {
	case p.out <- protocol.Envelope{V: 1, Type: typ, Payload: b}:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("send timeout")
	}
}

func (p pipeCh) Recv() (protocol.Envelope, error) {
	select {
	case e := <-p.in:
		return e, nil
	case <-time.After(5 * time.Second):
		return protocol.Envelope{}, errors.New("recv timeout")
	}
}

func pair(t *testing.T, codeA, codeB string, fpSeenByA, fpSeenByB string) (error, error) {
	a, _ := identity.LoadOrCreate(t.TempDir())
	b, _ := identity.LoadOrCreate(t.TempDir())
	if fpSeenByA == "" {
		fpSeenByA = b.FP
	}
	if fpSeenByB == "" {
		fpSeenByB = a.FP
	}
	ab, ba := make(chan protocol.Envelope, 8), make(chan protocol.Envelope, 8)
	errs := make(chan error, 2)
	go func() {
		_, err := Run(pipeCh{in: ba, out: ab}, codeA, true, a.FP, fpSeenByA,
			protocol.PairIdentities{Name: "a", PubKey: a.Pub, FP: a.FP, KithVer: "1.0.0"})
		errs <- err
	}()
	_, errB := Run(pipeCh{in: ab, out: ba}, codeB, false, b.FP, fpSeenByB,
		protocol.PairIdentities{Name: "b", PubKey: b.Pub, FP: b.FP, KithVer: "1.0.0"})
	return <-errs, errB
}

func TestPairingSuccess(t *testing.T) {
	ea, eb := pair(t, "7K3QMX", "7K3QMX", "", "")
	if ea != nil || eb != nil {
		t.Fatal(ea, eb)
	}
}

func TestPairingWrongCode(t *testing.T) {
	ea, eb := pair(t, "7K3QMX", "7K3QMY", "", "")
	if protocol.CodeOf(ea) != protocol.CodeAuthFailed && protocol.CodeOf(eb) != protocol.CodeAuthFailed {
		t.Fatal("wrong code paired", ea, eb)
	}
}

// A relay that knows the code but terminates TLS itself presents its own
// certificate, so the fingerprints bound into the MAC differ.
func TestPairingMITMFingerprintBinding(t *testing.T) {
	mitm, _ := identity.LoadOrCreate(t.TempDir())
	ea, eb := pair(t, "7K3QMX", "7K3QMX", mitm.FP, "")
	if ea == nil && eb == nil {
		t.Fatal("MITM with different cert fingerprint was accepted")
	}
}

func TestCodeFormat(t *testing.T) {
	re := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{6}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		b := []byte{byte(i), byte(i * 7), byte(i * 13), byte(i * 31)}
		c := CodeFromBytes(b)
		if !re.MatchString(c) {
			t.Fatal(c)
		}
		seen[c] = true
	}
	if len(seen) < 190 {
		t.Fatal("codes not well distributed")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLimiter()
	l.Now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if !l.Allowed("10.0.0.9") {
			t.Fatal("blocked early at", i)
		}
		l.Fail("10.0.0.9")
	}
	if l.Allowed("10.0.0.9") {
		t.Fatal("6th attempt allowed")
	}
	if !l.Allowed("10.0.0.10") {
		t.Fatal("other IP blocked")
	}
	now = now.Add(61 * time.Minute)
	if !l.Allowed("10.0.0.9") {
		t.Fatal("window did not expire")
	}
}
