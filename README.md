# Kith

Kith is a local-first task fabric for two paired computers on the same LAN.
An AI agent (or a human) on one machine submits a task to the other machine:
a set of files, an argv command and its requirements. The other machine checks
that it can run the task **before any file moves**, pulls the inputs and runs
the command. It streams the logs back and returns the declared outputs, with
SHA-256 verified end to end.

The main use: an agent on a Mac trains a model on the Windows box's NVIDIA GPU
and gets `model.pt` back. The same mechanism works in reverse.

No SSH, no cloud, no relay, no internet requirement. You pair the two machines
once; after that, delegation follows each machine's per-peer policy.

> **Security: read this.** Pairing grants the other machine **remote code
> execution on your machine, as your user**, subject to the peer policy
> (`ask` by default: every task waits for `kith approve`). Kith v1 does **not**
> sandbox tasks. Pair only machines you control, and keep `ask` unless you
> trust everything that can submit from the other side, including its agents.

## Install

**macOS** (needs Go; installs `~/.local/bin/kith` + zsh tab completion):

```bash
make install
```

**Windows** (PowerShell; installs to `%LOCALAPPDATA%\Programs\kith`, adds it
to PATH and PowerShell completion):

```powershell
powershell -ExecutionPolicy Bypass -File scripts\install.ps1
```

Then `kith --version` and `kith status`. Full command reference:
[`docs/CLI.md`](docs/CLI.md). Prebuilt static binaries: `make dist`.

## Desktop apps

| platform | app | build |
|---|---|---|
| macOS 26+ | native SwiftUI menu bar app (`mac/`) | `make mac-app` (Xcode 26+) → `mac/build/Kith.app` |
| Windows, Linux, Raspberry Pi | Wails v3 tray app (`gui/`): Fluent window with its own title bar, tray flyout, Pi lite mode | `release.yml`, or `wails3 task <os>:package` in `gui/`; UI development: `npm run dev` in `gui/frontend`, then open `/?mock` |

Both are thin clients of the local daemon (the IPC socket plus the bundled
`kith` CLI) and bundle the CLI, so one install gives you the app, the
daemon and `kith`. On macOS the app lives in the menu bar: peers, approvals
with Approve / Deny (also as notifications), running tasks with their latest
log line. The window has Overview, Tasks (live logs, artifacts, cancel),
Approvals, Machines (policy, unpair), Diagnostics, the pairing ceremony and
a New task sheet that preflights a `kith.toml` against the peer before
sending it. Drop a `kith.toml` (or a folder with one) on the window or the
Dock icon to send it.

## Quickstart (≈ 5 minutes)

**1. Start the daemon on both machines.** For a quick try, run it in the
foreground:

```bash
kith daemon
```

Or install it as a service. On macOS this creates a LaunchAgent. On Windows,
run it in an elevated terminal: it creates a service that runs as your user,
plus a firewall rule for TCP 7420 on the Private profile. On macOS, allow
"Local Network" access when asked.

```bash
kith daemon install
```

**2. Pair.** Run `kith pair` on both machines at about the same time. If mDNS
discovery doesn't work on your network, give one side the other's IP:

```bash
kith pair --ip 192.168.1.50
```

Both screens show the same 6-character code. Confirm with `y` on each machine
only if the codes match. `kith peers` should then list the other machine as
`online`.

**3. Policy.** New peers default to `ask`. On the executor, either approve
each task with `kith approve <id>`, or switch that peer to auto:

```bash
kith peers set-policy <name> auto
```

**4. Run something.** The quickest way is inline:

```bash
kith run --peer <name> -i train.py -i dataset.zip -o 'out/*' --gpu 4 -- python -u train.py
```

Or submit a `task.toml` (see [`examples/mnist`](examples/mnist)):

```bash
kith submit -f task.toml --peer <name>
```

```bash
kith logs <task-id> -f
```

When the task reaches `COMPLETED`, its outputs (e.g. `out/model.pt`) are
already in your project directory and their hashes have been verified.
`kith task <id>` shows the timeline and artifacts.

## Task spec

```toml
[task]
id = "tsk_01J9Z3M4N5P6Q7R8S9T0V1W2X3"   # optional; makes resubmission idempotent
project = "mnist-demo"
name = "train"
command = ["python", "-u", "train.py", "--epochs", "5"]   # argv only — never a shell string

[task.inputs]
files = ["train.py", "dataset.zip"]   # relative to the task.toml dir; globs allowed
max_total_gb = 25

[task.env]                            # verified on the executor, never installed
python = ">=3.10"
packages = { torch = ">=2.3+cu121" }
gpu = { vram_gb = 4, required = true }
probe = "python -c \"import torch; assert torch.cuda.is_available()\""

[task.outputs]
include = ["out/model.pt", "out/metrics.json", "logs/**/*.log"]
copy_back = true                      # outputs > retain_cap_gb stay on the executor
retain_cap_gb = 10

[task.policy]
on_interrupt = "restart"              # fail | restart | resume-if-checkpoint
checkpoint = { pattern = "out/checkpoint*.pt" }
timeout_minutes = 720
```

If a requirement is not met, the task is rejected before anything transfers,
with a fix:

```
NO_MATCH: torch>=2.3+cu121 not found (found 2.3.0+cpu).
Fix: pip install torch --index-url https://download.pytorch.org/whl/cu121
```

Tasks run with the daemon's environment plus `KITH_TASK_ID`, with the task
workspace as the working directory. Use `python -u` (or `flush=True`) to get
live logs.

## Commands

```
kith status                              overview: daemon, peers, tasks, approvals
kith run [flags] -- <cmd> [args...]      run on a peer without a task.toml
kith wait <ID>                           block until done (exit code = result)
kith daemon start|stop|restart|logs      background control
kith config list|get|set|unset           kith.toml settings
kith daemon [--home DIR]                 run in foreground
kith daemon install|uninstall|status
kith pair [--ip ADDR]                    interactive pairing (run on both machines)
kith peers | kith peers set-policy <peer> ask|auto | kith unpair <FP>
kith id | kith probe
kith submit -f task.toml --peer <name|FP>
kith tasks | kith task <ID> | kith logs <ID> [-f] | kith cancel <ID>
kith artifacts [<task>] | kith fetch <task> <path>
kith approve <ID> | kith deny <ID>
kith doctor                              network / firewall / daemon diagnostics
kith mcp                                 MCP server on stdio, for AI agents
```

`--home` overrides `$KITH_HOME` (default `~/.kith`); `--json` gives
machine-readable output.

## For AI agents

Tell an agent "I have a Kith-connected computer". It can then run jobs on
the other machine and follow them epoch by epoch. One command connects
Claude Code, Codex, Antigravity, Cursor and Gemini CLI:

```bash
kith agents install     # MCP server + the Kith skill, in each agent's own config
kith agents             # what's connected
```

`kith mcp` serves these tools:
- `list_peers`, `check_task` (preflight), `submit_task` and `list_tasks`
- `task_progress` and `wait_task`, which report live metrics (epochs,
  accuracy, F1, loss) parsed from `KITH_METRIC {...}` lines or from plain
  `epoch 3/20 loss=0.4` output
- `get_task`, `stream_logs`, `fetch_artifact` and `cancel_task`

Pairing stays human-only. For other MCP clients, `kith agents print`
gives the entry. See [integrations/](integrations/) for plugin bundles and
details, and [examples/cifar-bench](examples/cifar-bench) for a GPU
benchmark sized for a 6 GB card.

## Configuration — `$KITH_HOME/kith.toml`

```toml
port = 7420
name = "studio-mac"
default_policy = "ask"    # policy given to newly paired peers
log_level = "info"
# bind = "0.0.0.0"        # listen address
# mdns = true             # set false on networks without multicast
```

## Files in `$KITH_HOME`

`identity.key` (Ed25519, 0600) · `state.db` (SQLite, WAL) · `audit.log`
(append-only JSONL) · `logs/<task>.log` · `work/<task>/` (executor workspaces)
· `daemon.log` (when running as a service).

## Development

```bash
go vet ./... && go test ./...
```

The tests include a loopback acceptance harness: two real daemons with
separate homes over real TCP on 127.0.0.1, including `kill -9` crash tests.
They need Python 3 on `PATH`. See [`docs/ACCEPTANCE.md`](docs/ACCEPTANCE.md),
[`docs/kith1.md`](docs/kith1.md) (wire protocol) and
[`DECISIONS.md`](DECISIONS.md).
