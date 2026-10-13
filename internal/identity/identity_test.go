package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

func TestPersistenceAndStability(t *testing.T) {
	home := t.TempDir()
	a, err := LoadOrCreate(home)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(home)
	if err != nil {
		t.Fatal(err)
	}
	if a.FP != b.FP {
		t.Fatalf("fingerprint changed across loads: %s vs %s", a.FP, b.FP)
	}
	if len(a.FP) != 64 {
		t.Fatalf("fp length %d", len(a.FP))
	}
	sum := sha256.Sum256(a.Pub)
	if hex.EncodeToString(sum[:]) != a.FP {
		t.Fatal("fp is not sha256(raw pubkey)")
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(home, KeyFile))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("key perms %v", st.Mode().Perm())
		}
	}
	// Cert pins to the same fingerprint even though the cert is regenerated.
	fp1, _, err := CertFingerprint(a.Cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	fp2, _, _ := CertFingerprint(b.Cert.Certificate[0])
	if fp1 != a.FP || fp2 != a.FP {
		t.Fatal("cert fingerprint mismatch")
	}
}

func TestDistinctHomesDistinctIdentities(t *testing.T) {
	a, _ := LoadOrCreate(t.TempDir())
	b, _ := LoadOrCreate(t.TempDir())
	if a.FP == b.FP {
		t.Fatal("two homes produced the same identity")
	}
}

func TestDisplay(t *testing.T) {
	d := Display("0123456789abcdef" + "00000000000000000000000000000000000000000000000")
	if d != "KITH-0123-4567-89AB-CDEF" {
		t.Fatal(d)
	}
	a, _ := LoadOrCreate(t.TempDir())
	if !regexp.MustCompile(`^KITH-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}$`).MatchString(Display(a.FP)) {
		t.Fatal(Display(a.FP))
	}
	if NormalizeFP(Display(a.FP)) != a.FP[:16] {
		t.Fatal("normalize")
	}
}

func TestCorruptKeyRejected(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, KeyFile), []byte("garbage"), 0o600)
	if _, err := LoadOrCreate(home); err == nil {
		t.Fatal("expected error")
	}
}
