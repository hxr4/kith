# Kith/1 wire protocol

Implemented in `internal/protocol` (codec, messages), `internal/peer`
(TLS, handshake, heartbeat), `internal/pairing` and `internal/transfer`.
Decisions referenced as D-xxx are in `DECISIONS.md`.

## Transport

* TCP, default port **7420** (`port` in `kith.toml`).
* **TLS 1.3 only**, ALPN `kith/1`. Both sides present a self-signed
  certificate for their Ed25519 identity key; client certificates are required.
* **Pinned session:** the peer's fingerprint — SHA-256 of the raw Ed25519
  public key in the certificate — must equal the stored peer record. Mismatch
  → the dialer aborts the handshake (identity mismatch, audited); an
  unknown fingerprint without `unpaired` → `GOAWAY AUTH_FAILED`.
* **Pairing session:** same TLS, no pinning; both hellos carry `unpaired: true`.
  A responder sets `unpaired: true` only while it is ready to pair (pairing
  window open, source IP not rate limited); otherwise it answers
  `unpaired: false` followed by a GOAWAY with the reason, so the dialer fails
  before any code is shown.
  After pairing the connection is closed and a new pinned one is dialed.
* Connections are symmetric: either side may act as requester or executor.

## Framing

```
[4B big-endian length L][1B type][L-1 bytes payload]     (L counts the type byte)
```

| type | name | payload |
|---|---|---|
| `0x01` | CTRL | JSON envelope, ≤ 1 MiB, never compressed |
| `0x02` | FILE_CHUNK | `task_id`(16 B raw ULID) ‖ `file_sha256`(32 B) ‖ `offset`(8 B BE, uncompressed offset) ‖ data (zstd frame if negotiated) |
| `0x03` | LOG_CHUNK | `task_id`(16 B) ‖ `seq`(8 B BE = byte offset in the log) ‖ data |
| `0x04` | PING | 8 B BE unix-nano timestamp |
| `0x05` | GOAWAY | JSON `{"code","message"}`; sender closes |

Maximum frame payload: 4 MiB + 1 KiB (CTRL: 1 MiB). Larger lengths are a
protocol error. Unknown frame types are ignored.

`task_id` on the wire as text is `tsk_<26-char ULID>`; in binary frames it is
the 16 raw ULID bytes.

## CTRL envelope

```json
{"v":1,"type":"task.state","id":"7f0c…","payload":{…}}
```

* `v` is required in every envelope; missing → `PROTOCOL_ERROR`;
  `v != 1` → `GOAWAY VERSION_MISMATCH` with a readable message.
* Replies echo the request's `id`.
* `snake_case` fields, RFC 3339 UTC timestamps, sizes in bytes, lowercase hex
  SHA-256.
* Unknown `type` → logged (debug) and ignored. Unknown fields → ignored.

## Session start

1. TLS handshake (ALPN `kith/1`).
2. Both send `hello` first:
   `{"name","fp","kith_ver","unpaired"?,"port"?,"zstd"?}`.
   `fp` must equal the certificate fingerprint, else `GOAWAY AUTH_FAILED`.
   Differing major `kith_ver` → `GOAWAY VERSION_MISMATCH`.
   `zstd` is enabled iff both hellos set it.
3. Pinned sessions: both send `caps` (manifest) and `sync.request`.

## Messages

| type | dir | payload |
|---|---|---|
| `hello` | both | see above |
| `caps` | both | manifest: `os, arch, python[], packages{}, gpus[{name,vram_gb,free_vram_gb}], free_disk_gb, ram_gb, mode, queue_len, zstd, kith_ver` |
| `task.submit` | req→exe | `{"spec": {id, project, name, command[], inputs{files[], max_total_gb, offers[{path,size,sha256}]}, env{os?,arch?,python?,packages{}, gpu{vram_gb,required}, probe[]}, outputs{include[], copy_back, retain_cap_gb}, policy{on_interrupt, checkpoint{pattern}, timeout_minutes}}}` — inputs are offers, not bytes |
| `task.accept` | exe→req (reply) | `task_id, accepted_at, pending_approval, state, seq` |
| `task.reject` | exe→req (reply) | `task_id, code, message, remediation?, queue_len?, seq` |
| `file.offer` | holder→puller | `task_id, files[{path,size,sha256}]` (D-012) |
| `file.request` | puller→holder | `task_id, sha256, offset` |
| `file.done` | puller→holder | `task_id, sha256, ok` |
| `file.error` | holder→puller | `task_id, sha256, code, message` (D-010) |
| `task.state` | exe→req | `task_id, state, detail?, attempt, ts, seq, pending_approval?, code?, remediation?, log_size?` |
| `task.cancel` | req→exe | `task_id` |
| `artifact.offer` | exe→req | `task_id, artifacts[{path,size,sha256,remote}]` — `remote:true` = retained on the executor |
| `sync.request` | both | `task_ids[]` the sender mirrors as unfinished |
| `sync.state` | both (reply) | `tasks[task.state…], artifacts[artifact.offer…], unknown[]` |
| `pair.begin` | both | `msg` (base64 SPAKE2 message) |
| `pair.confirm` | both | `mac` (base64 HMAC-SHA256) |
| `pair.identities` | both | `name, pubkey, fingerprint, kith_ver, port` |

Log streaming uses LOG_CHUNK frames (no CTRL message); `seq` starts at 0 and
is replayed from 0 after every reconnect; receivers deduplicate by offset.

## Semantics

* **Submit.** The executor journals SUBMITTED, verifies capabilities (OS,
  arch, Python, packages, GPU, disk ≥ inputs + 1 GiB, concurrency, probe) and
  replies `task.accept` (auto policy → ACCEPTED; ask policy →
  `pending_approval: true`) or `task.reject`. No bytes move before this.
  Resubmitting an existing `id` returns the existing record.
* **Transfers are receiver-driven pulls.** The puller sends `file.request`
  with the offset it already holds; the holder streams FILE_CHUNKs (1 MiB of
  plaintext each) to EOF. The puller verifies the whole-file SHA-256 and sends
  `file.done`; mismatch → `TRANSFER_ERROR`, the partial file is deleted and the
  whole file is re-requested (up to 3 times). One active transfer per direction
  per connection. Interrupted pulls resume from the partial file's size.
* **Execution** state changes are journaled before `task.state` is sent.
  Artifacts are offered (`artifact.offer`) before `COMPLETED`; the requester
  pulls the non-retained ones with the same file mechanism.
* **Heartbeat.** PING every 5 s; nothing received for 15 s → close TCP; the
  peer is unreachable, task states are unchanged.
* **Reconnect** backoff 1 s, 2 s, 5 s, 15 s, then 60 s ± 20 %. Wake from
  sleep re-probes immediately.
* **Sync.** On every (re)connect each side sends `sync.request`; the
  executor answers with `sync.state` and replays logs from seq 0.

## States

```
SUBMITTED → ACCEPTED | REJECTED
ACCEPTED → TRANSFERRING
TRANSFERRING → RUNNING | FAILED
RUNNING → FINALIZING | CANCELLING | FAILED | INTERRUPTED
FINALIZING → COMPLETED | FAILED | INTERRUPTED
CANCELLING → CANCELLED | FAILED
INTERRUPTED → RUNNING | FAILED          (attempt+1, max 3)
terminal: COMPLETED | FAILED | CANCELLED | REJECTED
```

## Error codes

`AUTH_FAILED · PROTOCOL_ERROR · VERSION_MISMATCH · NO_MATCH · BUSY ·
POLICY_DENIED · DISK_FULL · TRANSFER_ERROR · NOT_FOUND · PEER_UNREACHABLE ·
RATE_LIMITED · CANCELLED_BY_PEER · TIMEOUT · INTERNAL`

Every error is `{code, message, remediation?}`; `BUSY` adds `queue_len`.

## Pairing ceremony (unpinned session)

1. Both hellos have `unpaired: true`; the responder must be in its pairing
   window (`kith pair` running), and the source IP must have < 5 failures in
   the last hour (else `GOAWAY RATE_LIMITED`).
2. Code = Crockford base32 of the first 30 bits of TLS exporter
   `EXPORTER-kith-pairing-code` (32 bytes). Both humans confirm equality
   within 120 s.
3. Symmetric SPAKE2 (Ed25519 group, identity `kith/1 pairing`) with the code;
   exchange `pair.begin`.
4. Exchange `pair.confirm` with
   `HMAC-SHA256(K, "kith/1 pair.confirm" ‖ msg_initiator ‖ msg_responder ‖ local_cert_fp ‖ remote_cert_fp)`
   (fingerprints as 32 raw bytes, from the certificates seen on this
   connection). Mismatch → `GOAWAY AUTH_FAILED`.
5. Exchange `pair.identities` (fingerprint must match the certificate and
   SHA-256(pubkey)); persist; close; reconnect pinned.
