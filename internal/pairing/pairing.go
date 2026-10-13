// Package pairing implements the one-time human pairing ceremony (§6):
// a short code derived from the (unpinned) TLS session that both humans
// compare, then balanced SPAKE2 over that code with a confirmation MAC that
// binds both certificate fingerprints.
package pairing

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	spake2 "salsa.debian.org/vasudev/gospake2"

	"kith/internal/identity"
	"kith/internal/protocol"
)

const (
	CodeLen      = 6
	CodeValidity = 120 * time.Second
	exporterLbl  = "EXPORTER-kith-pairing-code"
	spakeID      = "kith/1 pairing"
	confirmLbl   = "kith/1 pair.confirm"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// CodeFromState derives the 6-char Crockford base32 code from the TLS
// exporter. Both ends of the same TLS session compute the same code; a
// relaying MITM holds two sessions and therefore shows two different codes.
func CodeFromState(cs tls.ConnectionState) (string, error) {
	ekm, err := cs.ExportKeyingMaterial(exporterLbl, nil, 32)
	if err != nil {
		return "", err
	}
	return CodeFromBytes(ekm), nil
}

// CodeFromBytes encodes the first 30 bits of b.
func CodeFromBytes(b []byte) string {
	v := binary.BigEndian.Uint32(b[:4]) >> 2
	out := make([]byte, CodeLen)
	for i := CodeLen - 1; i >= 0; i-- {
		out[i] = crockford[v&31]
		v >>= 5
	}
	return string(out)
}

// Channel is the minimal message channel the ceremony needs.
type Channel interface {
	Send(typ string, payload any) error
	Recv() (protocol.Envelope, error)
}

func recvType(ch Channel, typ string, v any) error {
	env, err := ch.Recv()
	if err != nil {
		return err
	}
	if env.Type != typ {
		return protocol.Errf(protocol.CodeProtocolError, "pairing: expected %s, got %s", typ, env.Type)
	}
	if err := json.Unmarshal(env.Payload, v); err != nil {
		return protocol.Errf(protocol.CodeProtocolError, "pairing: bad %s: %v", typ, err)
	}
	return nil
}

func fpBytes(fp string) []byte {
	b, _ := hex.DecodeString(fp)
	return b
}

// ConfirmMAC = HMAC-SHA256(key, label ‖ transcript ‖ local_cert_fp ‖ remote_cert_fp).
func ConfirmMAC(key, transcript []byte, localFP, remoteFP string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(confirmLbl))
	m.Write(transcript)
	m.Write(fpBytes(localFP))
	m.Write(fpBytes(remoteFP))
	return m.Sum(nil)
}

// Run executes SPAKE2 + confirmation + identity exchange. initiator decides
// transcript order. localFP/remoteFP are the fingerprints of the
// certificates actually seen on this TLS connection.
func Run(ch Channel, code string, initiator bool, localFP, remoteFP string, me protocol.PairIdentities) (protocol.PairIdentities, error) {
	var none protocol.PairIdentities
	s := spake2.SPAKE2Symmetric(spake2.NewPassword(code), spake2.NewIdentityS(spakeID))
	mine := s.Start()
	if err := ch.Send(protocol.MsgPairBegin, protocol.PairBegin{Msg: mine}); err != nil {
		return none, err
	}
	var theirs protocol.PairBegin
	if err := recvType(ch, protocol.MsgPairBegin, &theirs); err != nil {
		return none, err
	}
	key, err := s.Finish(theirs.Msg)
	if err != nil {
		return none, protocol.Errf(protocol.CodeAuthFailed, "pairing failed: %v", err)
	}
	var transcript []byte
	if initiator {
		transcript = append(append(transcript, mine...), theirs.Msg...)
	} else {
		transcript = append(append(transcript, theirs.Msg...), mine...)
	}
	if err := ch.Send(protocol.MsgPairConfirm, protocol.PairConfirm{MAC: ConfirmMAC(key, transcript, localFP, remoteFP)}); err != nil {
		return none, err
	}
	var conf protocol.PairConfirm
	if err := recvType(ch, protocol.MsgPairConfirm, &conf); err != nil {
		return none, err
	}
	if !hmac.Equal(conf.MAC, ConfirmMAC(key, transcript, remoteFP, localFP)) {
		return none, protocol.Errf(protocol.CodeAuthFailed, "pairing codes did not match (or the connection is being intercepted)").
			WithFix("run `kith pair` again on both machines and compare the codes carefully")
	}
	if err := ch.Send(protocol.MsgPairIdentities, me); err != nil {
		return none, err
	}
	var them protocol.PairIdentities
	if err := recvType(ch, protocol.MsgPairIdentities, &them); err != nil {
		return none, err
	}
	if them.FP != remoteFP || identity.Fingerprint(them.PubKey) != remoteFP {
		return none, protocol.Errf(protocol.CodeAuthFailed, "pairing: peer identity does not match its certificate")
	}
	return them, nil
}

// Limiter enforces max failed attempts per source IP per window (§6 step 6).
type Limiter struct {
	Max    int
	Window time.Duration
	Now    func() time.Time

	mu    sync.Mutex
	fails map[string][]time.Time
}

func NewLimiter() *Limiter { return &Limiter{Max: 5, Window: time.Hour, Now: time.Now} }

func (l *Limiter) prune(ip string) []time.Time {
	cut := l.Now().Add(-l.Window)
	var keep []time.Time
	for _, t := range l.fails[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if l.fails == nil {
		l.fails = map[string][]time.Time{}
	}
	l.fails[ip] = keep
	return keep
}

// Allowed reports whether ip may attempt pairing now.
func (l *Limiter) Allowed(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(ip)) < l.Max
}

// Fail records a failed attempt from ip.
func (l *Limiter) Fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[ip] = append(l.prune(ip), l.Now())
}

// RateLimitedError is the stable error for exhausted attempts.
func RateLimitedError(ip string) *protocol.Error {
	return protocol.Errf(protocol.CodeRateLimited, "too many failed pairing attempts from %s", ip).
		WithFix("wait up to an hour before trying again")
}
