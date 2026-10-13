package transfer

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kith/internal/identity"
	"kith/internal/peer"
	"kith/internal/protocol"
)

const tid = "tsk_01J9Z3M4N5P6Q7R8S9T0V1W2X3"

type holder struct {
	path    string
	offsets []int64
	mu      sync.Mutex
}

func (h *holder) HandleCtrl(c *peer.Conn, env protocol.Envelope) {
	if env.Type == protocol.MsgFileRequest {
		var r protocol.FileRequest
		json.Unmarshal(env.Payload, &r)
		h.mu.Lock()
		h.offsets = append(h.offsets, r.Offset)
		h.mu.Unlock()
		go Serve(context.Background(), c, r.TaskID, r.SHA256, h.path, r.Offset)
	}
}
func (h *holder) HandleFileChunk(*peer.Conn, protocol.FileChunk) {}
func (h *holder) HandleLogChunk(*peer.Conn, protocol.LogChunk)   {}

type puller struct {
	m        *Manager
	cutAfter int32 // close the connection after this many chunks (0 = never)
	n        atomic.Int32
}

func (p *puller) HandleCtrl(*peer.Conn, protocol.Envelope) {}
func (p *puller) HandleFileChunk(c *peer.Conn, ch protocol.FileChunk) {
	if p.cutAfter > 0 && p.n.Add(1) > p.cutAfter {
		c.Close(nil)
		return
	}
	p.m.OnChunk(c, ch)
}
func (p *puller) HandleLogChunk(*peer.Conn, protocol.LogChunk) {}

func setup(t *testing.T, h *holder, zstd bool) func(*puller) *peer.Conn {
	a, _ := identity.LoadOrCreate(t.TempDir())
	b, _ := identity.LoadOrCreate(t.TempDir())
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			nc, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				c, err := peer.Accept(context.Background(), b, nc, func(string) protocol.Hello { return protocol.Hello{FP: b.FP, KithVer: "1.0.0", Zstd: zstd} }, slog.Default())
				if err == nil {
					c.Run(h)
				}
			}()
		}
	}()
	return func(p *puller) *peer.Conn {
		c, err := peer.Dial(context.Background(), a, l.Addr().String(), b.FP, protocol.Hello{FP: a.FP, KithVer: "1.0.0", Zstd: zstd}, slog.Default())
		if err != nil {
			t.Fatal(err)
		}
		go c.Run(p)
		return c
	}
}

func mkfile(t *testing.T, n int) (string, protocol.FileOffer) {
	p := filepath.Join(t.TempDir(), "dataset.bin")
	b := make([]byte, n)
	rand.Read(b)
	copy(b, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")) // a bit compressible
	os.WriteFile(p, b, 0o644)
	sha, size, _ := SHAFile(p)
	return p, protocol.FileOffer{Path: "data/dataset.bin", Size: size, SHA256: sha}
}

func TestFetchIntegrityBothCodecs(t *testing.T) {
	for _, z := range []bool{false, true} {
		src, off := mkfile(t, 3*protocol.ChunkSize+123)
		dial := setup(t, &holder{path: src}, z)
		m := NewManager()
		c := dial(&puller{m: m})
		dest := filepath.Join(t.TempDir(), "ws", "data", "dataset.bin")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		if err := m.Fetch(ctx, c, tid, off, dest, nil); err != nil {
			t.Fatal(z, err)
		}
		cancel()
		sha, _, _ := SHAFile(dest)
		if sha != off.SHA256 {
			t.Fatal("hash mismatch")
		}
		if _, err := os.Stat(dest + PartSuffix); !os.IsNotExist(err) {
			t.Fatal("part file left behind")
		}
	}
}

func TestFetchEmptyFile(t *testing.T) {
	src, off := mkfile(t, 0)
	dial := setup(t, &holder{path: src}, true)
	m := NewManager()
	c := dial(&puller{m: m})
	dest := filepath.Join(t.TempDir(), "empty")
	if err := m.Fetch(context.Background(), c, tid, off, dest, nil); err != nil {
		t.Fatal(err)
	}
}

func TestInterruptedTransferResumes(t *testing.T) {
	src, off := mkfile(t, 6*protocol.ChunkSize)
	h := &holder{path: src}
	dial := setup(t, h, true)
	m := NewManager()
	dest := filepath.Join(t.TempDir(), "d.bin")
	c1 := dial(&puller{m: m, cutAfter: 2})
	err := m.Fetch(context.Background(), c1, tid, off, dest, nil)
	if protocol.CodeOf(err) != protocol.CodePeerUnreachable {
		t.Fatal("expected interruption, got", err)
	}
	st, _ := os.Stat(dest + PartSuffix)
	if st == nil || st.Size() != 2*protocol.ChunkSize {
		t.Fatalf("partial size %v", st)
	}
	c2 := dial(&puller{m: m})
	if err := m.Fetch(context.Background(), c2, tid, off, dest, nil); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.offsets) != 2 || h.offsets[0] != 0 || h.offsets[1] != 2*protocol.ChunkSize {
		t.Fatalf("resume offsets %v", h.offsets)
	}
	sha, _, _ := SHAFile(dest)
	if sha != off.SHA256 {
		t.Fatal("resumed file hash mismatch")
	}
}

func TestHashMismatchIsTransferError(t *testing.T) {
	src, off := mkfile(t, protocol.ChunkSize+10)
	// The holder's file changes after the hash snapshot.
	os.WriteFile(src, make([]byte, protocol.ChunkSize+10), 0o644)
	dial := setup(t, &holder{path: src}, false)
	m := NewManager()
	c := dial(&puller{m: m})
	dest := filepath.Join(t.TempDir(), "x")
	err := m.Fetch(context.Background(), c, tid, off, dest, nil)
	if protocol.CodeOf(err) != protocol.CodeTransferError {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest + PartSuffix); !os.IsNotExist(err) {
		t.Fatal("bad partial must be deleted so a retry re-transfers the whole file")
	}
}
