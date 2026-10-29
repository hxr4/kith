// Package transfer implements receiver-driven, resumable file pulls over a
// Kith/1 connection (§11). The puller requests an offset; the holder streams
// 1 MiB FILE_CHUNK frames; the whole-file SHA-256 is the integrity authority.
package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/klauspost/compress/zstd"

	"kith/internal/peer"
	"kith/internal/protocol"
)

var (
	encoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(2))
	decoder, _ = zstd.NewReader(nil, zstd.WithDecoderMaxMemory(4<<20), zstd.WithDecoderConcurrency(2))
)

// PartSuffix marks incomplete downloads.
const PartSuffix = ".kithpart"

// Manager routes incoming FILE_CHUNK frames to the active pull of each
// connection (one active pull per connection direction).
type Manager struct {
	mu     sync.Mutex
	active map[*peer.Conn]*pull
}

func NewManager() *Manager { return &Manager{active: map[*peer.Conn]*pull{}} }

type pull struct {
	taskID   string
	path     string
	sha      [32]byte
	size     int64
	f        *os.File
	off      int64
	h        hash.Hash
	zstd     bool
	progress func(int64)
	lastRep  int64
	done     chan error
	finished bool
}

// Progress is called with bytes done (throttled, every 8 MiB and at the end).
type Progress func(done int64)

// SHAFile hashes a file.
func SHAFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func decodeSHA(s string) ([32]byte, error) {
	var out [32]byte
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return out, fmt.Errorf("bad sha256 %q", s)
	}
	copy(out[:], b)
	return out, nil
}

// Fetch pulls one file from the peer into dest, resuming from any existing
// dest+".kithpart". On success the file is verified and atomically renamed.
// A hash mismatch deletes the partial file and returns TRANSFER_ERROR.
func (m *Manager) Fetch(ctx context.Context, c *peer.Conn, taskID string, f protocol.FileOffer, dest string, progress Progress) error {
	sha, err := decodeSHA(f.SHA256)
	if err != nil {
		return protocol.Errf(protocol.CodeTransferError, "%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	c.PullMu.Lock()
	defer c.PullMu.Unlock()

	part := dest + PartSuffix
	fh, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			fh.Close()
		}
	}()
	st, err := fh.Stat()
	if err != nil {
		return err
	}
	off := st.Size()
	if off > f.Size {
		if err := fh.Truncate(0); err != nil {
			return err
		}
		off = 0
	}
	// Re-hash what we already have so the final check stays whole-file.
	h := sha256.New()
	if off > 0 {
		if _, err := io.Copy(h, io.NewSectionReader(fh, 0, off)); err != nil {
			return err
		}
	}
	if _, err := fh.Seek(off, io.SeekStart); err != nil {
		return err
	}
	p := &pull{taskID: taskID, path: f.Path, sha: sha, size: f.Size, f: fh, off: off, h: h, zstd: c.Zstd, progress: progress,
		lastRep: off, done: make(chan error, 1)}
	if off < f.Size {
		m.mu.Lock()
		if m.active[c] != nil {
			m.mu.Unlock()
			return protocol.Errf(protocol.CodeInternal, "pull already active on connection")
		}
		m.active[c] = p
		m.mu.Unlock()
		defer func() {
			m.mu.Lock()
			if m.active[c] == p {
				delete(m.active, c)
			}
			m.mu.Unlock()
		}()
		if _, err := c.Send(protocol.MsgFileRequest, protocol.FileRequest{TaskID: taskID, SHA256: f.SHA256, Offset: off}); err != nil {
			return protocol.Errf(protocol.CodePeerUnreachable, "%v", err)
		}
		select {
		case err := <-p.done:
			if err != nil {
				return err
			}
		case <-c.Done():
			return protocol.Errf(protocol.CodePeerUnreachable, "connection lost during transfer of %s (will resume)", f.Path)
		case <-ctx.Done():
			return protocol.Errf(protocol.CodeCancelledByPeer, "transfer of %s cancelled", f.Path)
		}
	}
	if progress != nil {
		progress(f.Size)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != f.SHA256 {
		fh.Close()
		closed = true
		os.Remove(part)
		c.Send(protocol.MsgFileDone, protocol.FileDone{TaskID: taskID, SHA256: f.SHA256, OK: false})
		return protocol.Errf(protocol.CodeTransferError, "hash mismatch for %s: want %s got %s (did it change after the task was submitted?)", f.Path, f.SHA256[:12], got[:12]).
			WithFix("don't edit input files until the transfer finishes; resubmit the task")
	}
	if err := fh.Sync(); err != nil {
		return err
	}
	fh.Close()
	closed = true
	os.Remove(dest) // Windows rename does not replace
	if err := os.Rename(part, dest); err != nil {
		return err
	}
	c.Send(protocol.MsgFileDone, protocol.FileDone{TaskID: taskID, SHA256: f.SHA256, OK: true})
	return nil
}

// OnChunk is called from the connection reader for every FILE_CHUNK.
func (m *Manager) OnChunk(c *peer.Conn, ch protocol.FileChunk) {
	m.mu.Lock()
	p := m.active[c]
	m.mu.Unlock()
	if p == nil || p.finished || p.taskID != ch.TaskID || p.sha != ch.SHA256 {
		return // stale chunk from an abandoned transfer
	}
	fail := func(err error) {
		p.finished = true
		p.done <- err
	}
	if int64(ch.Offset) != p.off {
		fail(protocol.Errf(protocol.CodeTransferError, "out-of-order chunk at %d, expected %d", ch.Offset, p.off))
		return
	}
	data := ch.Data
	if p.zstd {
		d, err := decoder.DecodeAll(data, nil)
		if err != nil {
			fail(protocol.Errf(protocol.CodeTransferError, "zstd: %v", err))
			return
		}
		data = d
	}
	if p.off+int64(len(data)) > p.size {
		fail(protocol.Errf(protocol.CodeTransferError, "%s is larger than when the task was submitted (%d bytes offered); it changed on the sending machine", p.path, p.size).
			WithFix("don't edit input files until the transfer finishes; resubmit the task"))
		return
	}
	if _, err := p.f.Write(data); err != nil {
		fail(protocol.Errf(protocol.CodeDiskFull, "write: %v", err))
		return
	}
	p.h.Write(data)
	p.off += int64(len(data))
	if p.progress != nil && p.off-p.lastRep >= 8<<20 {
		p.lastRep = p.off
		p.progress(p.off)
	}
	if p.off == p.size {
		fail(nil)
	}
}

// OnError fails the active pull for a file.error from the holder.
func (m *Manager) OnError(c *peer.Conn, fe protocol.FileError) {
	m.mu.Lock()
	p := m.active[c]
	m.mu.Unlock()
	if p == nil || p.finished || p.taskID != fe.TaskID || hex.EncodeToString(p.sha[:]) != fe.SHA256 {
		return
	}
	p.finished = true
	p.done <- &protocol.Error{Code: fe.Code, Message: fe.Message}
}

// Serve streams path from offset to the peer (holder side of
// file.request). It returns the number of bytes sent.
func Serve(ctx context.Context, c *peer.Conn, taskID, shaHex, path string, offset int64) (int64, error) {
	sha, err := decodeSHA(shaHex)
	if err != nil {
		return 0, err
	}
	c.PushMu.Lock()
	defer c.PushMu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	buf := make([]byte, protocol.ChunkSize)
	off := offset
	for {
		if ctx.Err() != nil {
			return off - offset, ctx.Err()
		}
		n, rerr := io.ReadFull(f, buf)
		if n > 0 {
			hdr, err := protocol.EncodeFileChunkHeader(taskID, sha, uint64(off))
			if err != nil {
				return off - offset, err
			}
			data := buf[:n]
			if c.Zstd {
				data = encoder.EncodeAll(data, make([]byte, 0, n/2))
			}
			if err := c.SendFrame(protocol.FrameFileChunk, hdr, data); err != nil {
				return off - offset, err
			}
			off += int64(n)
		}
		if rerr == io.EOF || errors.Is(rerr, io.ErrUnexpectedEOF) {
			return off - offset, nil
		}
		if rerr != nil {
			return off - offset, rerr
		}
	}
}
