// Package peer implements Kith/1 connections: TLS 1.3 with SPKI pinning,
// the framed CTRL/data channel, request/response matching and heartbeats.
package peer

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"

	"kith/internal/identity"
	"kith/internal/protocol"
)

// IdentityMismatchError is returned when a pinned peer presents a different key.
type IdentityMismatchError struct {
	Expected, Got string
}

func (e *IdentityMismatchError) Error() string {
	return fmt.Sprintf("IDENTITY MISMATCH: expected peer %s but it presented %s. If that machine was reinstalled, run `kith unpair %s` and pair again; otherwise something is impersonating it",
		identity.Display(e.Expected), identity.Display(e.Got), identity.Display(e.Expected))
}

func verifyEd25519(raw [][]byte) (string, ed25519.PublicKey, error) {
	if len(raw) == 0 {
		return "", nil, errors.New("peer sent no certificate")
	}
	return identity.CertFingerprint(raw[0])
}

// ServerTLS accepts any Ed25519 client certificate; the daemon decides after
// the handshake whether it is a pinned peer or a pairing attempt.
func ServerTLS(id *identity.Identity) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{id.Cert},
		ClientAuth:   tls.RequireAnyClientCert,
		NextProtos:   []string{protocol.ALPN},
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			_, _, err := verifyEd25519(raw)
			return err
		},
	}
}

// ClientTLS dials with pinning when expectFP != "" (paired session) and
// without pinning otherwise (pairing ceremony, §6 step 2).
func ClientTLS(id *identity.Identity, expectFP string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{id.Cert},
		InsecureSkipVerify: true, // replaced by VerifyPeerCertificate pinning below
		NextProtos:         []string{protocol.ALPN},
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			fp, _, err := verifyEd25519(raw)
			if err != nil {
				return err
			}
			if expectFP != "" && fp != expectFP {
				return &IdentityMismatchError{Expected: expectFP, Got: fp}
			}
			return nil
		},
	}
}

// PeerFP returns the fingerprint of the certificate presented on cs.
func PeerFP(cs tls.ConnectionState) (string, ed25519.PublicKey, error) {
	if len(cs.PeerCertificates) == 0 {
		return "", nil, errors.New("no peer certificate")
	}
	return identity.CertFingerprint(cs.PeerCertificates[0].Raw)
}
