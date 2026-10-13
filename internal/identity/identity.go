// Package identity owns the device Ed25519 key, its self-signed TLS
// certificate and fingerprints.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const KeyFile = "identity.key"

// Identity is this device's long-term identity.
type Identity struct {
	Priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
	FP   string // full 64-char lowercase hex SHA-256 of the raw public key
	Cert tls.Certificate
}

// LoadOrCreate loads $home/identity.key or generates it (0600) on first run.
func LoadOrCreate(home string) (*Identity, error) {
	path := filepath.Join(home, KeyFile)
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		blk, _ := pem.Decode(b)
		if blk == nil {
			return nil, fmt.Errorf("%s: not PEM", path)
		}
		k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		priv, ok := k.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%s: not an Ed25519 key", path)
		}
		return FromKey(priv)
	case errors.Is(err, fs.ErrNotExist):
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(home, 0o700); err != nil {
			return nil, err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
			return nil, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return nil, err
		}
		return FromKey(priv)
	default:
		return nil, err
	}
}

// FromKey builds an Identity (and a fresh self-signed cert) from a private key.
func FromKey(priv ed25519.PrivateKey) (*Identity, error) {
	pub := priv.Public().(ed25519.PublicKey)
	fp := Fingerprint(pub)
	cert, err := selfSigned(priv, fp)
	if err != nil {
		return nil, err
	}
	return &Identity{Priv: priv, Pub: pub, FP: fp, Cert: cert}, nil
}

func selfSigned(priv ed25519.PrivateKey, fp string) (tls.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "kith " + Display(fp)},
		NotBefore:    time.Now().Add(-24 * time.Hour),
		NotAfter:     time.Now().Add(100 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, priv.Public(), priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv, Leaf: leaf}, nil
}

// Fingerprint is the lowercase hex SHA-256 of a raw Ed25519 public key.
func Fingerprint(pub ed25519.PublicKey) string {
	s := sha256.Sum256(pub)
	return hex.EncodeToString(s[:])
}

// CertFingerprint extracts the Ed25519 key from a DER certificate and returns
// its fingerprint. This is the SPKI pin (see DECISIONS.md D-004).
func CertFingerprint(der []byte) (string, ed25519.PublicKey, error) {
	c, err := x509.ParseCertificate(der)
	if err != nil {
		return "", nil, err
	}
	pub, ok := c.PublicKey.(ed25519.PublicKey)
	if !ok {
		return "", nil, errors.New("peer certificate is not Ed25519")
	}
	return Fingerprint(pub), pub, nil
}

// Display renders the first 8 bytes of a fingerprint as KITH-XXXX-XXXX-XXXX-XXXX.
func Display(fp string) string {
	h := strings.ToUpper(fp)
	if len(h) < 16 {
		return "KITH-" + h
	}
	return fmt.Sprintf("KITH-%s-%s-%s-%s", h[0:4], h[4:8], h[8:12], h[12:16])
}

// NormalizeFP turns a display or hex fingerprint (or a prefix) into lowercase
// hex without separators, for prefix matching.
func NormalizeFP(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "kith-")
	return strings.ReplaceAll(s, "-", "")
}
