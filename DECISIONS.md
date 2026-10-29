# DECISIONS

Choices made where the spec was silent, ambiguous, self-contradictory or
not implementable as written. IDs are referenced from code comments.
Where this records a *tension* with the spec, the spec's intent was kept and
the deviation is the smallest that works.

## Dependencies & toolchain

**D-001 SPAKE2 library.** `github.com/agl/spake2` does not exist (the Go proxy
has no such module). Substituted `salsa.debian.org/vasudev/gospake2`
(pinned pseudo-version `v0.0.0-20210510093858-d91629950ad1`), a pure-Go port of
python-spake2 / magic-wormhole, used in **symmetric (balanced) mode** on the
Ed25519 group. It pulls `golang.org/x/crypto` (pure Go). It uses `math/big`
(not constant-time); acceptable because codes are single-use, valid for 120 s,
and attempts are rate limited. *Tension:* the spec pins a module that cannot
be fetched; this is the closest maintained equivalent. No other crypto code
outside stdlib.

**D-002 ULID.** `github.com/oklog/ulid/v4` does not exist; using
`github.com/oklog/ulid/v2 v2.1.2` (latest).

**D-003 Other pins.** `github.com/Microsoft/go-winio` (canonical casing; the
lowercase path fails module path checks). TOML parsing (unspecified):
`github.com/BurntSushi/toml v1.6.0`. errgroup: `golang.org/x/sync`. Module path
is `kith`. The `go` directive is 1.26 because `modernc.org/sqlite v1.60.1`
requires it (spec says ≥1.23; satisfied). Developed with Go 1.27.1.

## Identity, TLS, pairing

**D-004 Fingerprint & pin.** Fingerprint = lowercase hex SHA-256 of the raw
32-byte Ed25519 public key. The pin compares the key extracted from the peer
certificate's SubjectPublicKeyInfo — equivalent to SPKI pinning for a fixed
algorithm. The self-signed cert is regenerated at every start (only the key is
pinned). "Crockford hex" for the display form is read as uppercase hex:
`KITH-XXXX-XXXX-XXXX-XXXX` = first 8 bytes.

**D-005 Pairing code semantics.** §6 says *each* machine generates and displays
the code and the human confirms both screens match. Implemented as a short
authentication string: both ends derive the 6-char Crockford base32 code from
the TLS 1.3 exporter (`EXPORTER-kith-pairing-code`) of the unpinned session, so
both show the same code, while a relaying MITM (two TLS sessions) shows two
different codes. Each human answers y/N; then balanced SPAKE2 runs with that
code. Transcript = initiator `pair.begin` msg ‖ responder msg. Confirm MAC =
`HMAC-SHA256(K, "kith/1 pair.confirm" ‖ transcript ‖ local_cert_fp ‖ remote_cert_fp)`
over the raw 32-byte fingerprints of the certificates actually seen on that
connection. A failed attempt = MAC mismatch, human "no", or 120 s expiry.

**D-006 Pairing discovery.** While `kith pair` runs, the daemon re-advertises
TXT `mode=pairing`; when both sides are in pairing mode, the side with the
lexicographically smaller fingerprint dials. With `--ip` the local side dials.
The responder must also be running `kith pair` (pairing window); otherwise an
unpaired hello gets `GOAWAY AUTH_FAILED`.

**D-007 Rate limiting** is in memory per remote IP (5 failures / rolling hour)
and is checked on both sides; it resets on daemon restart.

## Protocol

**D-008 Additive fields** (all ignorable by §7.1 rules): `hello.port` (listen
port, used to learn `last_addr` of inbound peers) and `hello.zstd` (compression
negotiation); `task.accept.state/seq`, `task.reject.message/seq`,
`task.state.seq/pending_approval/code/remediation/log_size`;
`sync.state.unknown`. `seq` is the executor's journal sequence number; the
requester drops stale updates by it.

**D-009 LOG_CHUNK seq** is the byte offset of the chunk within the task's log.
"Starts at 0", replay from 0 on reconnect, and dedup become trivial: the
requester appends only bytes beyond its current length.

**D-010 `file.error`** (holder → puller: `task_id, sha256, code, message`) added
so a holder that cannot serve a request (unknown task/hash, read error) fails
the pull with a stable code instead of leaving it hanging.

**D-011 zstd** is applied per FILE_CHUNK (`EncodeAll`, fastest level) when both
hellos set `zstd`; the frame `offset` is the uncompressed offset. CTRL is never
compressed.

**D-012 file.offer.** Input offers travel inside `task.submit` (§7.2: "inputs
are offers"). The requester additionally sends `file.offer` when its mirror
enters TRANSFERRING; the executor checks it against the spec snapshot (logs a
warning on mismatch; the snapshot wins). Artifacts use `artifact.offer`.

**D-013 Heartbeat.** Any received frame counts as liveness; the connection is
closed after 3 × 5 s without receiving anything.

**D-014 Duplicate connections.** Both daemons dial. If two connections to the
same peer coexist, both sides keep the one dialed by the smaller fingerprint
(deterministic, order-independent).

**D-015 sync.** `sync.request` carries the requester's non-terminal mirror IDs
(plus completed tasks with pending copy-back). The executor answers with all
its non-terminal tasks for that peer **and** those IDs (so tasks that finished
during a partition reconcile), plus artifact records. IDs it does not know are
listed in `sync.state.unknown`; the requester re-sends `task.submit`
(idempotent).

## Tasks

**D-016 TRANSFERRING after a restart. Tension.** §9.4 lists TRANSFERRING among
the states moved to INTERRUPTED at startup, but §9.2 defines no
`TRANSFERRING → INTERRUPTED` edge ("no other edges exist") and §11 requires
transfers to resume from SQLite progress. Resolved in favour of §9.2 + §11:
ACCEPTED/TRANSFERRING tasks keep their state across daemon restarts and the
transfer resumes from the `.kithpart` offset. RUNNING/FINALIZING go to
INTERRUPTED exactly as specified; CANCELLING completes to CANCELLED.

**D-017 Cancel by state.** SUBMITTED → REJECTED (`CANCELLED_BY_PEER`);
ACCEPTED → TRANSFERRING → FAILED; TRANSFERRING/INTERRUPTED → FAILED
(`CANCELLED_BY_PEER`); RUNNING → CANCELLING → CANCELLED; FINALIZING and
terminal states: no-op (only legal edges are used).

**D-018 Restart policy cleanup.** "Clean workspace" removes every file that is
not a submitted input, then re-verifies input hashes (inputs are not
re-transferred: INTERRUPTED → TRANSFERRING is not an edge). If the previous
attempt modified an input, the task FAILs with remediation "resubmit". The
policy also applies to FINALIZING interruptions. Attempts start at 1; the
re-run that would be attempt 4 fails.

**D-019 Concurrency.** One active executor task (ACCEPTED…INTERRUPTED). There is
no queue: a submit while busy is rejected `BUSY` with `queue_len` = active
tasks. In `ask` mode the busy check is skipped at submit (a pending task does
not occupy the machine) and enforced at approval.

**D-020 Spec defaults.** `on_interrupt = "fail"` (no surprise re-runs),
`timeout_minutes = 0` (no timeout), `copy_back = true`, `retain_cap_gb = 10`,
`max_total_gb = 25`, `project = "default"`, `name = basename(argv[0])`.
Optional `env.os` / `env.arch` were added so the §10.2 OS/arch checks have
inputs (`os` is a prefix match, e.g. "windows" matches "windows-11").

**D-021 Probe & shell wrappers.** `env.probe` may be a string; it is tokenised
into argv with simple quoting rules (no expansion) and executed directly —
never by a shell — in a scratch dir with a 60 s timeout. Specs are rejected if
any key is a shell-wrapper field (`shell, sh, bash, cmd, powershell, pwsh,
script, shell_command, command_line, commandline`) or `command` is a string.
`argv[0]` itself is not policed (running `bash script.sh` is the user's call).

**D-022 Version matching (§10.1).** The numeric release of the installed
version is compared with Masterminds/semver (pre-release/dev suffixes ignored;
PEP 440 `==`/`~=` mapped to `=`/`~`). If the constraint carries a local tag
(`+cu121`), the installed version must carry exactly that tag; without a tag,
local tags are ignored. So `>=2.3+cu121` accepts `2.3.1+cu121`, rejects
`2.3.0+cpu`, `2.2.0+cu121` and plain `2.3.1`. Remediation for a `cuXXX` tag is
`pip install torch --index-url https://download.pytorch.org/whl/cuXXX`.

**D-023 GPU & disk.** GPU requirement compares **total** VRAM (free VRAM is
transient). Disk precheck measures free space on the executor's
`$KITH_HOME/work` volume.

**D-024 Environment.** Exactly daemon env + `KITH_TASK_ID`. Kith does not set
`PYTHONUNBUFFERED`; examples use `python -u` for live logs.

**D-025 Logs.** Executor log: `$KITH_HOME/logs/<task>.log` (stdout+stderr of
the whole tree, written directly by the child). Re-runs append under a
`=== kith: attempt N ===` separator. The requester mirror uses the same path in
its own home.

**D-026 Requester COMPLETED.** The requester mirror holds COMPLETED as
FINALIZING ("copying artifacts back") until copy-back has finished, so an agent
that sees COMPLETED can immediately read `out/model.pt`. If copy-back fails
after 3 whole-file retries, the mirror still becomes COMPLETED with a detail
pointing to `kith fetch`. The executor's own state is never delayed.

**D-027 IDs.** The CLI generates `tsk_<ULID>` when the TOML has none but does
not write it back; resubmitting that file creates a new task. Put an `id` in
the file to make resubmission idempotent. The daemon generates one for MCP
submissions without an id. Task IDs given to commands may be unique prefixes.

## Daemon, OS integration, interfaces

**D-028 Package `internal/daemon`** holds the orchestration (connections,
executor/requester logic, IPC API). It is not in the §5 listing; every other
§5 package exists with the listed responsibility.

**D-029 Start order.** identity → DB → IPC listener → capability probe →
*journal recovery* → mDNS advertise/browse → TLS listener (recovery placed
before networking so no work is accepted before it completes).

**D-030 Config keys** `bind` (default `0.0.0.0`) and `mdns` (default `true`)
were added to `kith.toml` alongside `port/name/default_policy/log_level`; they
let several daemons share one host (loopback tests) and let users without
multicast turn discovery off.

**D-031 Windows IPC.** `\\.\pipe\kith-ipc` for the default home; other homes use
`\\.\pipe\kith-ipc-<sha256(home)[:12]>` so two daemons can run on one machine.
The pipe DACL allows only SYSTEM and the current user. macOS: `ipc.sock` (0600)
in the 0700 home.

**D-032 Windows service identity. Tension.** §13 says `x/sys/windows/svc`; §6
says tasks run "as your user". A LocalSystem service would run tasks as SYSTEM
(different PATH/Python, wrong privileges). `kith daemon install` therefore
creates the service with the *current user* as its logon account (password
prompted, stored only by the SCM), grants that account `SeServiceLogonRight`
via LSA, adds the firewall rule (TCP 7420, Private profile, scoped to the
kith.exe program) and sets restart-on-failure.

**D-033 Windows process trees.** The child is created `CREATE_SUSPENDED`,
assigned to a Job Object with `KILL_ON_JOB_CLOSE`, then resumed with
`NtResumeProcess` — so no grandchild can be spawned outside the job. If the
daemon dies, the OS closes the job handle and the tree dies. macOS: `Setpgid`;
crash recovery SIGKILLs the stored process group before applying policy.

**D-034 Sleep prevention.** macOS: `caffeinate -i -w <pid>`; Windows:
`SetThreadExecutionState(ES_CONTINUOUS|ES_SYSTEM_REQUIRED)` held on a locked OS
thread; both released when the task process exits.

**D-035 Wake detection.** A wall-clock jump of more than 30 s between 5 s ticks
is treated as wake from sleep: all dialers immediately re-probe `last_addr`
(and mDNS results) instead of waiting out their backoff.

**D-036 CLI additions.** `kith peers set-policy <peer> ask|auto` (the spec
defines per-peer policy but no way to change it), `kith pair --yes` (non-
interactive confirmation, used by tests), `kith version`, global `--json`.

**D-037 MCP.** `submit_task(peer, spec)` also accepts `spec_file` (path to a
task.toml) and `project_dir` (default: the spec file's dir or the MCP
process's cwd). `stream_logs` with `follow=true` long-polls up to 20 s and
returns `next_offset`/`complete`. Tool errors are returned as tool results
with `isError` and the `{code, message, remediation}` JSON. The SDK is pinned
at v1.8.0 and confined to `internal/mcp/mcp.go` (`NewServer`); tool logic
depends only on the `Backend` interface.

**D-038 Test hooks.** `daemon.Options` exposes `Caps` (the `caps.Source`
injection point), `ApprovalTTL`, `TimeoutUnit`, `PairCodeHook` and
`OnFileChunk` to Go tests only. None is reachable from the CLI or config.

**D-039 Holder-side transfer rows.** The serving side records
`direction="out"` rows (bytes sent) in `transfers`; the pulling side's
`direction="in"` rows drive resume, but the `.kithpart` file size is the
authoritative resume offset.

**D-040 mDNS on Windows** shares UDP 5353 with the OS responder and may be
filtered by the firewall; discovery stays best-effort and `kith pair --ip`
plus `last_addr` re-probing are always available.

**D-041 Service PATH.** launchd gives agents a minimal PATH and Windows
services do not see per-user PATH entries, so `python` (Homebrew, per-user
installs, venvs) would not resolve. `kith daemon install` snapshots the
installing shell's `PATH` into the LaunchAgent `EnvironmentVariables` /
the service's `Environment` registry value. Re-run install after changing
toolchains. Task env is still "daemon env + KITH_TASK_ID" (§9.1).

**D-042 Responder hello signals pairing readiness.** On an unpinned
connection the responder's `hello.unpaired` is `true` only if its pairing
window is open and the source IP is under the rate limit. An initiator that
gets `unpaired: false` reads the following GOAWAY and reports its reason
(`AUTH_FAILED … not in pairing mode` / `RATE_LIMITED`) without displaying a
code; this does not count as a failed attempt. (Found in the first real
Mac↔Windows pairing: the Mac showed a code for a PC that was not yet running
`kith pair`.)

**D-043 CLI additions (post-v1 usability).** `kith status`, `kith run` (inline
task spec from flags, follows logs), `kith wait`, `kith daemon
start|stop|restart|logs`, `kith config list|get|set|unset|path`, task filters,
`kith approve` without an ID lists pending tasks, `--version`, `--no-color`,
dynamic shell completion. Exit codes: 0 ok, 1 error, 2 FAILED, 3 CANCELLED,
4 REJECTED, 5 wait timeout. New IPC method `shutdown` (graceful, same path
as SIGTERM). Install via `scripts/install.sh` (→ `~/.local/bin`, zsh
completion) / `scripts/install.ps1`. None of this adds a §2 non-goal.

**D-044 Services are bound to a home.** `kith daemon start/stop/status` only
treat the LaunchAgent / Windows service as "the daemon" when its arguments
name the same `--home`; any other home is managed as a background process.
(Prevents `--home /tmp/x daemon start` from restarting the real service.)

**D-045 Windows mDNS firewall rule.** On the real LAN the Mac never saw the
PC via mDNS although TCP 7420 worked. Windows' built-in "mDNS (UDP-In)" rule
is scoped to the DNS Client service, so inbound UDP 5353 to `kith.exe` was
dropped. `kith daemon install` now also adds "Kith (mDNS UDP 5353)"
(inbound, Private profile, program-scoped) and `kith doctor` checks it.
Verification on the PC is pending (docs/PENDING-WINDOWS.md W3).

**D-046 launchd restart race.** `launchctl bootout` returns before launchd has
finished tearing the job down; an immediate `bootstrap` then fails (found
with the first `kith daemon restart` under the LaunchAgent). `Stop` now waits
until `launchctl print` no longer finds the job, and `Start` retries
`bootstrap` for up to 10 s (or kickstarts if the job is still loaded).

**D-047 Least-privilege service queries (Windows).** `mgr.Connect` asks for
`SC_MANAGER_ALL_ACCESS`, so a non-elevated `kith status` printed "Access is
denied". Status and installation checks now open the SCM with
`SC_MANAGER_CONNECT` and the service with `SERVICE_QUERY_CONFIG |
SERVICE_QUERY_STATUS`, both granted to interactive users. Start/stop still
need elevation and say so.

**D-048 Cancel attribution.** The cancel's origin is recorded: `cancelled by
the requester` or `cancelled on the executor` (a `kith cancel` run on the
machine executing the task). The code stays `CANCELLED_BY_PEER`, which is
accurate from the requester's side. `kith task` shows the task's `pid` while
it runs, because on Windows `nvidia-smi` cannot see session-0 (service)
processes.

**D-049 Mirror timeline = state changes only.** When the executor reports
the same state with new details (pending approval; COMPLETED held as
FINALIZING during copy-back), the requester updates the row in place
instead of appending a transition. This removes the SUBMITTED/SUBMITTED and
FINALIZING/FINALIZING duplicates.

**D-050 ASCII-only CLI text.** Windows PowerShell 5.1 decodes captured
output with the OEM code page, so `—`/`…` became `â€”`/`â€¦`. Kith's own
strings are now ASCII (a hygiene test enforces it); task logs pass through
unchanged. Also: `kith run` names the peer, the "superseded connection"
message after simultaneous redials is debug-level, `install.ps1` checks
elevation first and reports build progress, and `.gitattributes` pins line
endings (a zip copy had dropped `install.sh`'s exec bit; the Makefile now
calls it via `sh`).

**D-051 Linux builds.** `caps/sys_unix.go` used the macOS-only
`unix.SysctlUint64`, and the Linux service stub missed the `Installed(home)`
signature, so Kith did not compile for Linux (found by the first Linux vet,
ahead of the Linux/Raspberry Pi releases). RAM probing is now per-OS
(`sys_darwin.go`, `sys_linux.go`), and CI vets and builds darwin/arm64,
darwin/amd64, windows/amd64, linux/amd64, linux/arm64 and linux/arm (32-bit
Raspberry Pi OS), and runs the test suite on Ubuntu as well. Linux has no
service integration yet (systemd is planned with the Linux release).

**D-052 Linux service and sleep prevention.** `kith daemon install` on Linux
writes a systemd **user** unit (`~/.config/systemd/user/kith.service`;
`KillMode=mixed` so the daemon's graceful shutdown handles task trees; the
installer's PATH is captured; logs are appended to `$KITH_HOME/daemon.log`),
enables and starts it, and enables lingering so it starts at boot without
a login. That is the headless Raspberry Pi mode. Sleep prevention on Linux
uses `systemd-inhibit … tail --pid=<task>` (best effort). Firewalls
(ufw/firewalld) are not managed; the installer prints the rule to add.

**D-053 Desktop apps.** Windows, Linux and the Raspberry Pi use the Wails v3
app in `gui/` (one web frontend, native tray and notifications). macOS gets
a native SwiftUI app in `mac/` (menu bar extra, Liquid Glass materials,
spring-based motion), built as a Swift package and bundled by
`mac/scripts/bundle.sh`. It needs macOS 26 and Xcode 26 to build. Both apps
hold no state: they read the daemon over IPC (`$KITH_HOME/ipc.sock`,
newline-delimited JSON, the same protocol as the CLI) and shell out to the
bundled `kith` for daemon control, config, doctor, check and submit. In the
mac bundle the CLI lives in `Contents/Helpers/kith`, because `MacOS/Kith`
and `MacOS/kith` collide on case-insensitive volumes. Commands run with
the login shell's PATH, so a LaunchAgent installed from the app snapshots a
PATH that finds python. Pairing discovery in the mac app browses
`_kith._tcp` with Network.framework and learns each address with a
throwaway TCP connect. The daemon logs the failed handshake at debug.

**D-054 Preflight before sending.** `kith check -f task.toml [--peer P]`
parses a task, sizes its inputs and lists each static requirement against
the peer's last advertised manifest (`caps.Explain`: pass / fail / unknown).
A package the manifest doesn't list is "unknown", not "fail", because the
executor probes the packages a task names when it arrives; `caps.Check`
stays the authority. The task view over IPC now also carries the spec's
input files, outputs and the same requirement lines, computed against this
machine for received tasks and the peer's manifest for sent ones, so
approval cards show what will run and on what. All fields are additive.

**D-055 Windows / Linux / Pi app redesign.** The Wails frontend follows the
Claude Design "Quiet instrument, Windows edition": Fluent geometry,
Segoe UI Variable and Cascadia Mono, the same state chips and link as the
Mac app. It is plain TypeScript: views return escaped HTML (an `html`
tagged template) and `morphdom` patches the page, so unchanged nodes keep
their focus and animations and only new ones animate in. The log console
and live counters are owned imperatively (`data-keep`). Motion uses only
transform, opacity and filter. A rolling digit runs through the Web
Animations API with a safety cancel, so a paused animation clock (an
occluded window) can never leave a digit invisible.
On Windows the window is frameless with the app's own title bar; the
caption buttons are native non-client regions (Snap layouts work) and
only the empty parts of the title bar drag. Mica sits behind the title
bar and navigation on Windows 11 22H2+, with solid colours elsewhere.
Linux keeps native decorations plus an in-app header bar. The tray icon
has an attached flyout window that sizes itself to its content. Pi lite
mode is one column with a tab bar, 44 px targets, no blur, no pulses and
slower polling. New supporting commands: `kith daemon firewall` (the
"Add firewall rules" button runs it in an Administrator window), and
`kith doctor` now reports the real Windows network profile
(Public/Private), Python and nvidia-smi. A development-only mock backend
(`npm run dev`, `/?mock`) runs the UI in a browser; production builds
exclude it.

**D-056 The mark.** Kith's logo is two offset half-rings, the two
machines, passing a dot, the task, between them (`branding/`). The
geometry lives in one place, `branding/geometry.json`, with a heavier
"small cut" for 32 px and below. Every raster is drawn from it:
`gui/build/icongen` writes the app icon (PNG, a per-size .ico, and an
.icns through iconutil on macOS), the tray icons and the favicon, and the
`generate:icons` build task runs it. The Mac app draws the same geometry
in Swift (`MarkGeometry`). On macOS the app icon is a Liquid Glass icon
(`mac/AppIcon.icon`, from Icon Composer: the halves and the dot as two
glass layers on teal, white halves and a light-teal dot in Dark);
`bundle.sh` compiles it with actool 26+ into Assets.car and falls back to
the flat .icns without it. Its menu bar icon is a template; while a task
runs, the dot glides from one half to the other, narrowing as it passes
so it never touches either.

**D-057 Pairing holds together across two GUIs.** Found pairing Mac and
Windows from the apps. The code arriving no longer navigates the Windows
app to `#/pair`, which had reset its dialog and left both daemons waiting
out the two-minute code. In the daemon:
- **Crossed connections:** when both people choose each other, both ends
  keep the connection dialed by the smaller fingerprint, so both show the
  same code. The losing connection is dropped quietly.
- **Failed dials:** a failed dial ends the session only if no other
  connection is carrying the ceremony.
- **Reading during the comparison:** while the human compares codes, the
  daemon reads the connection, so a cancel or a "they don't match" on the
  other machine ends the wait at once. The first side to confirm also
  shows "<name> confirmed" on the other.
- **Bound confirmations:** a confirmation names the code it answers, and
  an answer about a code no longer on screen is ignored.
- **What counts as a failed attempt:** closing the dialog cancels
  (CANCELLED_BY_PEER) rather than rejecting, and neither a cancel nor an
  expired code counts toward the rate limit.

Clients now classify errors by code, not by message text.

**D-058 Agents follow progress, not logs, and one skill serves every
agent.** MCP gains three tools:
- `check_task`: a preflight against the peer's manifest.
- `task_progress`: state plus parsed metrics. The latest and best values
  per epoch (best is higher for accuracy and F1, lower for loss), the
  current step and a short output tail.
- `wait_task`: blocks until the next epoch (`until=epoch`), any change or
  the finish, 50 s by default and 600 s at most. Most clients time tools
  out near 60 s.

How progress is read:
- **The metrics contract:** a `KITH_METRIC {json}` line per epoch (kinds
  `epoch`, `step`, `start`, `done`). Plain `epoch N/M key=value` lines are
  parsed as a fallback.
- **Incremental reads:** the log is read incrementally and cached per
  task in the MCP process, so polling stays cheap.
- **Optional peer:** `peer` is optional when one machine is paired, or
  only one is online (`daemon.PickPeer`, shared with the CLI).
- **Server instructions:** the server sends MCP instructions, so an agent
  with only the server still knows the workflow.

The skill (`internal/agents/SKILL.md`, the Agent Skills format) is
embedded in the binary. `kith agents install` writes it, plus the MCP
entry with an absolute path to `kith`, into each agent's documented
locations:
- **Claude Code:** via `claude mcp add`.
- **Codex:** one marked block in `config.toml`, with
  `tool_timeout_sec = 660`.
- **Antigravity:** `~/.gemini/config`.
- **Cursor and Gemini CLI:** their JSON files.

JSON is merged with a backup, and uninstall removes exactly what was
added. `integrations/` holds generated plugin bundles; a test keeps them
in sync with the embedded skill.
