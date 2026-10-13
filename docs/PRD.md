# Kith — Product Requirements (v1 → v2)

**One line:** Kith turns two computers you own into one machine for your AI
agents. An agent on the Mac can hand a job to the Windows GPU, and get the
results back, without SSH, cloud or copy-pasting.

---

## 1. Why (the need)

| Problem today | Kith's answer |
|---|---|
| Your laptop has the agent; your desktop has the GPU. | Agents delegate work across machines. |
| Moving work means SSH, shared folders or a cloud VM, with set-up and secrets everywhere. | One pairing (6-character code), then it just works on the LAN. |
| Agents can't safely "use the other computer". | Six narrow MCP tools; every job is checked before it runs. |
| Jobs die when Wi-Fi blips or a machine restarts. | Durable task state, resumable transfers, crash recovery. |
| You find out the GPU lacks the right torch only after copying 20 GB. | Capabilities are verified **before** any file moves, with the exact fix. |

## 2. Users

- **Primary:** one person with two machines (e.g. a Mac for daily work and a
  Windows PC with an NVIDIA GPU) who uses AI coding agents.
- **Actors:** the human (pairs, approves) and agents (submit, watch, fetch via
  MCP).

## 3. Goals / non-goals

**Goals:** local-first, two peers, secure by default, survives network and
crash failures, agent-friendly, one binary per OS.

**Non-goals (v1):** cloud/relay, SSH, more than two peers, containers or
sandboxing, installing packages, scheduling queues, web UI or dashboards
(the GUI is planned for v2).

## 4. What works today (v1)

✅ verified on real hardware · 🧪 verified by automated tests ·
⏳ pending a Windows run (`docs/PENDING-WINDOWS.md`)

| Capability | Status |
|---|---|
| Pair two machines with a matching code (SPAKE2 + pinned TLS) | ✅ |
| Capability gate (OS, Python, packages incl. `+cu121`, GPU VRAM, disk, probe) | ✅ |
| Mac → Windows GPU training; logs stream live; `model.pt` returns, hash matches | ✅ |
| Auto-reconnect after a daemon restart, no re-pairing | ✅ |
| Ask/auto approval policy, 15-minute expiry | 🧪 |
| Resumable transfers (cut link mid-file → resumes, not restarts) | 🧪 |
| Partition: executor keeps running, requester catches up, logs byte-identical | ✅ |
| Crash (`kill -9`) → INTERRUPTED → fail / restart / resume-from-checkpoint | ✅ |
| Cancel kills the whole process tree (pgid / Job Object) | ✅ |
| Large outputs kept on the executor, fetched on demand | 🧪 |
| Audit log of every event (no codes or keys logged) | 🧪 |
| MCP server with 6 tools for agents | 🧪 |
| macOS LaunchAgent; Windows service + firewall | ✅ |
| Installable CLI (`kith` in zsh / PowerShell, completion, `run`, `status`, `wait`, …) | ✅ |
| Reverse direction Windows → Mac | ✅ |
| mDNS discovery (pair without typing an IP) | ✅ (after D-045) |

Tests: 18 packages, all green on macOS (with `-race`) and Windows.

## 5. How it works (short)

```
 Mac                                   Windows
 kith CLI / MCP ─IPC─ kith daemon ══TLS 1.3, Kith/1══ kith daemon ─ process (GPU)
                        SQLite · audit                SQLite · audit
```

- **One binary, three faces.** `kith daemon` owns all state and networking;
  the CLI and `kith mcp` are thin clients over a local socket or named pipe.
- **Identity.** Each machine has an Ed25519 key; its fingerprint is its name
  on the wire.
- **Pairing.** Both screens show a code derived from the TLS session. The
  human compares the codes, then SPAKE2 proves them, bound to both
  certificates. After that, connections are pinned: an impostor is rejected
  loudly.
- **Submit.** The requester sends a spec: an argv command (never a shell),
  input hashes and requirements. The executor checks its capabilities first,
  then accepts, rejects (with a fix) or waits for approval.
- **Transfer.** The receiver pulls files in 1 MiB chunks (zstd); SHA-256
  must match. Progress lives in SQLite, so transfers resume.
- **Run.** The command runs in an isolated workspace directory; the whole
  process tree is killable; sleep is blocked while it runs; logs stream with
  byte offsets.
- **Durability.** Every state change is written to SQLite *before* it is
  announced. The executor is the source of truth; on reconnect it re-sends
  state and replays logs.
- **Recovery.** After a crash, a running task becomes INTERRUPTED, then its
  policy applies: fail, restart, or resume from a checkpoint (max 3
  attempts).
- **Artifacts.** Outputs are hashed and copied back into your project folder,
  or kept on the executor if they're too big.

## 6. Interfaces

- **CLI:** `pair`, `run`, `submit`, `status`, `tasks`, `logs -f`, `wait`,
  `cancel`, `approve`, `artifacts`, `fetch`, `daemon start|stop|install`,
  `config`, `doctor`. Exit codes encode the result. See `docs/CLI.md`.
- **MCP (agents):** `list_peers`, `submit_task`, `get_task`, `stream_logs`,
  `fetch_artifact`, `cancel_task`.
- **Wire:** Kith/1 (`docs/kith1.md`).

## 7. Success metrics

| Metric | Target | Now |
|---|---|---|
| Pair → first completed task (human time) | ≤ 5 min | ~3.5 min (11 Oct: first `kith pair` 10:46 → task COMPLETED 10:49 UTC) |
| Bytes moved before a capability rejection | 0 | 0 (tested) |
| Artifact hash mismatches | 0 | 0 |
| Tasks lost to network blips / daemon restarts | 0 | 0 in tests |

## 8. Risks & open items

- **No sandbox:** pairing grants remote code execution as you. Mitigated by
  the `ask` default, the audit log and pinned identities.
- **mDNS is unreliable** on some networks; `--ip` and `last_addr` re-probing
  are the fallback.
- **Windows service needs your password** (so tasks run as you, not as
  SYSTEM).
- **Pending:** GitHub/CI (W10); round-6 re-checks of the report-5 fixes.

## 9. Roadmap

1. **Close v1:** W1–W9 passed on real hardware; push to GitHub so CI runs.
2. **v2 — GUI** (a separate app, same IPC as the CLI):
   approve/deny notifications → pairing screen (big codes) → peers and GPUs →
   task list with live logs, artifacts and cancel.
3. **Later (candidates):** more than two peers, a task queue instead of BUSY,
   optional sandboxing, prebuilt signed releases.
