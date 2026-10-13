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

One static binary per platform (`CGO_ENABLED=0`):

```bash
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o dist/kith-darwin-arm64 ./cmd/kith
```

```bash
CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -o dist/kith-darwin-amd64 ./cmd/kith
```

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/kith.exe ./cmd/kith
```

Put the binary on your `PATH` on both machines.

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

**4. Submit.** On the requester, from a project directory with a `task.toml`
(see [`examples/mnist`](examples/mnist)):

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

## For AI agents (MCP)

`kith mcp` exposes exactly six tools: `list_peers`, `submit_task`, `get_task`,
`stream_logs`, `fetch_artifact` and `cancel_task`. Pairing is human-only.
Example client configuration:

```json
{ "mcpServers": { "kith": { "command": "kith", "args": ["mcp"] } } }
```

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
