# Acceptance

## 1. Automated matrix (§16)

Run with `go test ./...`. CI runs it on macOS (with `-race`) and Windows.
Python 3 must be on `PATH`. The "training task" is a small Python script that
sleeps, prints epoch lines, and writes `out/model.pt` and `out/metrics.json`.

| §16 requirement | Test(s) |
|---|---|
| pairing success | `daemon.TestPairingSuccess`, `cmd/kith.TestE2ECLIFullFlowBothDirections` (CLI, both screens show the same code), `pairing.TestPairingSuccess` |
| pairing wrong code fails + rate limit | `daemon.TestPairingWrongCodeAndRateLimit`, `pairing.TestPairingWrongCode`, `pairing.TestPairingMITMFingerprintBinding`, `pairing.TestLimiter` |
| identity mismatch rejected | `daemon.TestIdentityMismatchRejected`, `peer.TestIdentityMismatch` |
| discovery (best-effort) + manual-IP pairing | `daemon.TestManualIPAndDiscoveryReprobe` (manual IP over 127.0.0.1; a fake mDNS answer heals a stale `last_addr`), `discovery.TestTXTAndFromService` (fake responder), `discovery.TestMDNSRoundTrip` (real multicast; skipped where unavailable) |
| capability mismatch → REJECTED before transfer | `daemon.TestCapabilityMismatchRejectedBeforeTransfer` (no workspace, no transfer rows; probe-command gate), `daemon.TestApprovalReverifiesCapabilities`, `caps.*` |
| successful task, both directions | `daemon.TestSuccessfulTaskBothDirections`, `cmd/kith.TestE2ECLIFullFlowBothDirections` |
| file integrity (hash match) | same as above, plus `transfer.TestFetchIntegrityBothCodecs`, `transfer.TestHashMismatchIsTransferError` |
| interrupted transfer → resume completes | `daemon.TestInterruptedTransferResumes` (link cut mid-file; resumes from the offset, not from zero), `transfer.TestInterruptedTransferResumes` |
| partition during RUNNING | `daemon.TestPartitionDuringRunning`, `daemon.TestRequesterOfflineSyncAndLogReplay` |
| executor crash (kill -9) → INTERRUPTED → restart / fail | `cmd/kith.TestE2EExecutorKill9/{restart,fail}` (real process; `Process.Kill` = SIGKILL / `TerminateProcess` ≡ `taskkill /F`; orphaned task process verified dead) |
| daemon restart recovery from DB | `daemon.TestDaemonRestartRecoveryPolicies` (fail / restart / resume-if-checkpoint / attempt cap), `daemon.TestPendingApprovalSurvivesRestart`, `cmd/kith.TestE2ERequesterKill9`, `store.TestTaskLifecycleAndIdempotency` |
| cancellation → CANCELLED, process dead | `daemon.TestCancellationKillsProcessTree`, `daemon.TestBusy` |
| process-tree cleanup (grandchild) | `daemon.TestCancellationKillsProcessTree`, `task.TestTerminateKillsWholeTree` |
| artifact integrity + copy-back + remote retain/fetch | `daemon.TestSuccessfulTaskBothDirections`, `daemon.TestArtifactsRetainAndFetch` (incl. `remote (producer offline)`), `artifacts.TestCollectCopyBackAndRetain` |
| duplicate submit returns same record | `daemon.TestDuplicateSubmitReturnsSameRecord`, e2e resubmit |
| audit log accounts for every event | `daemon.TestAuditAccountsForEveryEvent` (also asserts no pairing code / key material in the log) |
| peer policy Ask (approve / deny / 15-min expiry) and Auto | `daemon.TestPolicyAskApproveDenyExpiry` (expiry TTL shortened via `Options.ApprovalTTL`) |
| all six MCP tools | `mcp.TestSixToolsOverMCP` (in-process MCP client over the SDK's in-memory transport) |
| log replay after reconnect | `daemon.TestRequesterOfflineSyncAndLogReplay`, `daemon.TestPartitionDuringRunning` (byte-identical logs, dedup after a second replay) |
| extra | `daemon.TestTimeout`, `daemon.TestNonZeroExitFails`, `peer.TestHeartbeatTimeout`, `peer.TestVersionMismatchGoAway`, `protocol.*` (fuzz-ish), `cmd/kith.TestNoCgoInDependencyGraph`, `cmd/kith.TestNoNonGoalDependencies` |

## 2. Real-hardware test (manual, not CI)

**Hardware.** A Mac (Apple Silicon or Intel) and a Windows 10/11 PC with an
NVIDIA RTX GPU, on the same LAN. Use Ethernet or a non-guest Wi-Fi network.

### 2.1 Prepare

1. **Windows.** Install the NVIDIA driver (check that `nvidia-smi` works).
   Install Python ≥ 3.10 and CUDA torch:
   `pip install torch --index-url https://download.pytorch.org/whl/cu121`.
   Check: `python -c "import torch; print(torch.__version__, torch.cuda.is_available())"`
   should print `2.x.x+cu121 True`.
2. **Mac.** Python 3 (for `fetch_mnist.py`; the reverse test also needs
   `pip3 install torch`).
3. Put the `kith` binary on `PATH` on both machines.
4. **Windows**, in an elevated terminal: `kith daemon install` (enter your
   Windows password when asked), then set the network to **Private**.
5. **Mac:** `kith daemon install`, and allow **Local Network** access.
6. On both machines, `kith doctor` should report no `[FAIL]` lines.

### 2.2 Pair (start the timer: target ≤ 5 minutes of human time to the first completed task)

- [ ] Run `kith pair` on Windows and `kith pair` on the Mac (or `kith pair --ip <windows-ip>`).
- [ ] Both screens show the **same** code; answer `y` on both.
- [ ] `kith peers` on each machine shows the other as `online`, with a caps summary. On the Mac, the Windows entry shows the RTX GPU.
- [ ] On Windows: `kith peers set-policy <mac-name> auto` (or approve each task).

### 2.3 Mac → Windows GPU training

```bash
cd examples/mnist && python3 fetch_mnist.py
```

```bash
kith submit -f task.toml --peer <windows-name>
```

```bash
kith logs <task-id> -f
```

- [ ] Before transfer, Windows verifies Python, torch `+cu121`, the GPU (≥ 4 GB) and the CUDA probe. (Optional negative check: on a CPU-only torch, the task is REJECTED with `NO_MATCH` and the `pip install … whl/cu121` fix.)
- [ ] `dataset.zip` and `train.py` transfer (`kith task <id>` shows TRANSFERRING, then RUNNING).
- [ ] Logs stream to the Mac: `device: NVIDIA GeForce RTX …`, then `epoch 1/5 … test_acc=0.98…`.
- [ ] While it runs, `nvidia-smi` on Windows shows a `python` process using the GPU.
- [ ] The task ends `COMPLETED` on both machines, and `examples/mnist/out/model.pt` exists on the Mac.
- [ ] Hashes match. On the Mac: `shasum -a 256 out/model.pt`. On Windows: `certutil -hashfile %USERPROFILE%\.kith\work\<task-id>\out\model.pt SHA256`. Both equal the `sha256` shown by `kith artifacts <task-id>`.

### 2.4 Resilience spot checks

- [ ] **Partition.** During training, unplug the Mac's network for about 30 s, then reconnect. Training continues on Windows. The Mac's mirror catches up and the full log is present (`kith logs <id>`).
- [ ] **Crash.** During training, run `taskkill /F /IM kith.exe` on Windows; the service restarts it. The task goes INTERRUPTED, then resumes from `out/checkpoint.pt` (log shows `resumed from checkpoint`) and then COMPLETED.
- [ ] **Cancel.** `kith cancel <id>` gives CANCELLED, and `nvidia-smi` shows no leftover python process.

### 2.5 Reverse: Windows → Mac

On Windows, in a copy of `examples/mnist` that contains `dataset.zip`:

```
kith submit -f task-cpu.toml --peer <mac-name>
kith logs <task-id> -f
```

- [ ] The Mac verifies its capabilities, receives the files, trains (CPU), and streams logs. `model.pt` arrives on Windows and the SHA-256 values match (`certutil -hashfile` vs `shasum -a 256`).

### 2.6 Agent path

- [ ] Configure an MCP client with `{"command": "kith", "args": ["mcp"]}` on the Mac. Ask the agent to list peers and submit `examples/mnist/task.toml` via `submit_task` (`spec_file`). It follows `stream_logs` and reports the returned `model.pt` path and sha256 from `get_task` / `fetch_artifact`.

Record results (date, machines, GPU, Kith version, pass/fail per box) below.

| date | Mac | Windows / GPU | result | notes |
|---|---|---|---|---|
| 2026-10-08 | MacBook Air (arm64, macOS 27) | HARI-PC, Win 11 | **pass** | Full test suite green on Windows (Go 1.27.0, Python 3.12.10) incl. Job Object tree kill, kill -9 recovery, named-pipe IPC. |
| 2026-10-11 | MacBook Air | HARI-PC · RTX 3050 6GB Laptop · torch 2.5.1+cu121 | **pass** (§2.2, §2.3) | Paired via `--ip` (codes matched); mDNS did not see the PC (→ D-045). Gate verified torch/GPU/CUDA probe; 11.6 MB transferred; 5 epochs at ~2.8 s, test_acc 0.988; `model.pt` sha256 `72ffa3f9…` identical on both sides. Re-run on a rebuilt Mac daemon (auto-reconnect, no re-pair): test_acc 0.987, hashes match. Kith also delivered an updated kith.exe into the PC's Downloads (hash-checked). |
| 2026-10-13 | MacBook Air | — | Mac-side **pass** | LaunchAgent install (`kith daemon install`) runs the daemon under launchd; CLI installed to `~/.local/bin`. PC not on the Mac's current network → remaining checks listed in `docs/PENDING-WINDOWS.md`. |

Still open on real hardware: §2.4 resilience spot checks, §2.5 reverse
direction, §2.6 agent path, mDNS discovery — see `docs/PENDING-WINDOWS.md`.
