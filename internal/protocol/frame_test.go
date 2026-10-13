package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/rand"
	"testing"

	"github.com/oklog/ulid/v2"
)

func newTaskID() string { return "tsk_" + ulid.Make().String() }

func TestCtrlRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	b, err := EncodeCtrl(MsgHello, "m1", Hello{Name: "mac", FP: "ab", KithVer: "1.0.0", Unpaired: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteFrame(FrameCtrl, b); err != nil {
		t.Fatal(err)
	}
	f, err := NewReader(&buf).ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != FrameCtrl {
		t.Fatal("type")
	}
	env, err := DecodeCtrl(f.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var h Hello
	json.Unmarshal(env.Payload, &h)
	if env.Type != MsgHello || env.ID != "m1" || h.Name != "mac" || !h.Unpaired {
		t.Fatalf("%+v %+v", env, h)
	}
}

func TestUnknownTypeAndFieldsTolerated(t *testing.T) {
	env, err := DecodeCtrl([]byte(`{"v":1,"type":"future.thing","id":"x","payload":{"a":1},"extra":true}`))
	if err != nil || env.Type != "future.thing" {
		t.Fatal(err)
	}
	var h Hello
	if err := json.Unmarshal([]byte(`{"name":"n","fp":"f","kith_ver":"1","new_field":[1,2]}`), &h); err != nil {
		t.Fatal(err)
	}
}

func TestVersionChecks(t *testing.T) {
	_, err := DecodeCtrl([]byte(`{"v":2,"type":"hello","id":"x"}`))
	if CodeOf(err) != CodeVersionMismatch {
		t.Fatal(err)
	}
	_, err = DecodeCtrl([]byte(`{"type":"hello","id":"x"}`))
	if CodeOf(err) != CodeProtocolError {
		t.Fatal("missing v must be a protocol error, got", err)
	}
}

func TestFileAndLogChunkRoundtrip(t *testing.T) {
	id := newTaskID()
	sha := sha256.Sum256([]byte("x"))
	h, err := EncodeFileChunkHeader(id, sha, 12345)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.WriteFrame(FrameFileChunk, h, []byte("payload"))
	lh, _ := EncodeLogChunkHeader(id, 77)
	w.WriteFrame(FrameLogChunk, lh, []byte("epoch 1\n"))
	r := NewReader(&buf)
	f, _ := r.ReadFrame()
	c, err := DecodeFileChunk(f.Payload)
	if err != nil || c.TaskID != id || c.SHA256 != sha || c.Offset != 12345 || string(c.Data) != "payload" {
		t.Fatalf("%+v %v", c, err)
	}
	f, _ = r.ReadFrame()
	l, err := DecodeLogChunk(f.Payload)
	if err != nil || l.TaskID != id || l.Seq != 77 || string(l.Data) != "epoch 1\n" {
		t.Fatalf("%+v", l)
	}
}

func TestLimits(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteFrame(FrameCtrl, make([]byte, MaxCtrlSize+1)); err != ErrFrameTooLarge {
		t.Fatal("ctrl over 1MiB accepted")
	}
	// Forged oversized header must be rejected without allocating it.
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[:4], 1<<31)
	if _, err := NewReader(bytes.NewReader(hdr[:])).ReadFrame(); err == nil {
		t.Fatal("expected error")
	}
	// Short chunk payloads are protocol errors, not panics.
	if _, err := DecodeFileChunk([]byte{1, 2}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := DecodeLogChunk(nil); err == nil {
		t.Fatal("expected error")
	}
	if _, err := TaskIDBytes("tsk_bad"); err == nil {
		t.Fatal("expected error")
	}
}

// Fuzz-ish: random byte streams must never panic the decoder.
func TestRandomStreamsNoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		n := rng.Intn(200)
		b := make([]byte, n)
		rng.Read(b)
		if n >= 4 && rng.Intn(2) == 0 {
			binary.BigEndian.PutUint32(b[:4], uint32(rng.Intn(n)))
		}
		r := NewReader(bytes.NewReader(b))
		for {
			f, err := r.ReadFrame()
			if err != nil {
				break
			}
			switch f.Type {
			case FrameCtrl:
				DecodeCtrl(f.Payload)
			case FrameFileChunk:
				DecodeFileChunk(f.Payload)
			case FrameLogChunk:
				DecodeLogChunk(f.Payload)
			}
		}
	}
}

func FuzzReadFrame(f *testing.F) {
	f.Add([]byte{0, 0, 0, 2, 1, '{'})
	f.Fuzz(func(t *testing.T, b []byte) {
		r := NewReader(bytes.NewReader(b))
		for {
			fr, err := r.ReadFrame()
			if err != nil {
				if err == io.EOF {
					return
				}
				return
			}
			DecodeCtrl(fr.Payload)
			DecodeFileChunk(fr.Payload)
			DecodeLogChunk(fr.Payload)
		}
	})
}
