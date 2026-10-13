// Package protocol implements the Kith/1 wire format (docs/kith1.md).
package protocol

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/oklog/ulid/v2"
)

// Frame types.
const (
	FrameCtrl      byte = 0x01
	FrameFileChunk byte = 0x02
	FrameLogChunk  byte = 0x03
	FramePing      byte = 0x04
	FrameGoAway    byte = 0x05
)

const (
	Version      = 1
	ALPN         = "kith/1"
	MaxCtrlSize  = 1 << 20      // 1 MiB
	ChunkSize    = 1 << 20      // 1 MiB transfer chunks
	MaxFrameSize = 4<<20 + 1024 // data frames: chunk + header + zstd worst case
)

// Frame is one decoded frame.
type Frame struct {
	Type    byte
	Payload []byte
}

// ErrFrameTooLarge is returned for frames exceeding the limits.
var ErrFrameTooLarge = errors.New("frame too large")

// Writer serialises frames; safe for concurrent use.
type Writer struct {
	mu sync.Mutex
	w  *bufio.Writer
}

func NewWriter(w io.Writer) *Writer { return &Writer{w: bufio.NewWriterSize(w, 64<<10)} }

// WriteFrame writes one frame atomically and flushes.
func (fw *Writer) WriteFrame(typ byte, parts ...[]byte) error {
	n := 1
	for _, p := range parts {
		n += len(p)
	}
	if n-1 > MaxFrameSize || (typ == FrameCtrl && n-1 > MaxCtrlSize) {
		return ErrFrameTooLarge
	}
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[:4], uint32(n))
	hdr[4] = typ
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if _, err := fw.w.Write(hdr[:]); err != nil {
		return err
	}
	for _, p := range parts {
		if _, err := fw.w.Write(p); err != nil {
			return err
		}
	}
	return fw.w.Flush()
}

// Reader decodes frames. Not safe for concurrent use.
type Reader struct {
	r *bufio.Reader
}

func NewReader(r io.Reader) *Reader { return &Reader{r: bufio.NewReaderSize(r, 64<<10)} }

// ReadFrame reads the next frame. The length prefix counts the type byte.
func (fr *Reader) ReadFrame() (Frame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(fr.r, hdr[:]); err != nil {
		return Frame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n < 1 {
		return Frame{}, fmt.Errorf("%w: empty frame", ErrFrameTooLarge)
	}
	if n-1 > MaxFrameSize {
		return Frame{}, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(fr.r, buf); err != nil {
		return Frame{}, err
	}
	f := Frame{Type: buf[0], Payload: buf[1:]}
	if f.Type == FrameCtrl && len(f.Payload) > MaxCtrlSize {
		return Frame{}, ErrFrameTooLarge
	}
	return f, nil
}

// Envelope wraps every CTRL payload.
type Envelope struct {
	V       int             `json:"v"`
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// EncodeCtrl marshals an envelope.
func EncodeCtrl(typ, id string, payload any) ([]byte, error) {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	return json.Marshal(Envelope{V: Version, Type: typ, ID: id, Payload: raw})
}

// DecodeCtrl parses an envelope. A missing "v" is a protocol error.
func DecodeCtrl(b []byte) (Envelope, error) {
	var probe struct {
		V *int `json:"v"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return Envelope{}, Errf(CodeProtocolError, "bad envelope: %v", err)
	}
	if probe.V == nil {
		return Envelope{}, Errf(CodeProtocolError, "envelope missing \"v\"")
	}
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return Envelope{}, Errf(CodeProtocolError, "bad envelope: %v", err)
	}
	if e.V != Version {
		return e, Errf(CodeVersionMismatch, "peer speaks Kith/%d, this build speaks Kith/%d", e.V, Version).
			WithFix("install the same major Kith version on both machines")
	}
	return e, nil
}

// TaskIDBytes converts "tsk_<ULID>" to its 16 raw bytes.
func TaskIDBytes(id string) ([16]byte, error) {
	if len(id) != 30 || id[:4] != "tsk_" {
		return [16]byte{}, fmt.Errorf("bad task id %q", id)
	}
	u, err := ulid.ParseStrict(id[4:])
	if err != nil {
		return [16]byte{}, fmt.Errorf("bad task id %q: %w", id, err)
	}
	return u, nil
}

// TaskIDString converts 16 raw bytes to "tsk_<ULID>".
func TaskIDString(b [16]byte) string { return "tsk_" + ulid.ULID(b).String() }

// FileChunk is the FILE_CHUNK payload.
type FileChunk struct {
	TaskID string
	SHA256 [32]byte
	Offset uint64
	Data   []byte
}

// EncodeFileChunk returns the header parts (data is appended separately to avoid copies).
func EncodeFileChunkHeader(taskID string, sha [32]byte, offset uint64) ([]byte, error) {
	tid, err := TaskIDBytes(taskID)
	if err != nil {
		return nil, err
	}
	h := make([]byte, 16+32+8)
	copy(h[:16], tid[:])
	copy(h[16:48], sha[:])
	binary.BigEndian.PutUint64(h[48:], offset)
	return h, nil
}

func DecodeFileChunk(p []byte) (FileChunk, error) {
	if len(p) < 56 {
		return FileChunk{}, Errf(CodeProtocolError, "short FILE_CHUNK")
	}
	var tid [16]byte
	copy(tid[:], p[:16])
	var c FileChunk
	c.TaskID = TaskIDString(tid)
	copy(c.SHA256[:], p[16:48])
	c.Offset = binary.BigEndian.Uint64(p[48:56])
	c.Data = p[56:]
	return c, nil
}

// LogChunk is the LOG_CHUNK payload. Seq is the byte offset of Data within
// the task's log stream (see DECISIONS.md D-009).
type LogChunk struct {
	TaskID string
	Seq    uint64
	Data   []byte
}

func EncodeLogChunkHeader(taskID string, seq uint64) ([]byte, error) {
	tid, err := TaskIDBytes(taskID)
	if err != nil {
		return nil, err
	}
	h := make([]byte, 24)
	copy(h[:16], tid[:])
	binary.BigEndian.PutUint64(h[16:], seq)
	return h, nil
}

func DecodeLogChunk(p []byte) (LogChunk, error) {
	if len(p) < 24 {
		return LogChunk{}, Errf(CodeProtocolError, "short LOG_CHUNK")
	}
	var tid [16]byte
	copy(tid[:], p[:16])
	return LogChunk{TaskID: TaskIDString(tid), Seq: binary.BigEndian.Uint64(p[16:24]), Data: p[24:]}, nil
}
