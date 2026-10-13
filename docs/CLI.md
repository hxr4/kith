# kith CLI reference

One binary is the daemon, the CLI and the MCP server. Every command except
`daemon` (foreground), `mcp` and `config` talks to the local daemon over IPC.

## Install

macOS (builds from source; needs Go):

```bash
make install
```

This installs `~/.local/bin/kith`, writes zsh completion to
`~/.local/share/zsh/site-functions/_kith`, and adds a marked
`# >>> kith >>>` block to `~/.zshrc`. Re-run it after pulling changes; if
the daemon is running, apply the update with `kith daemon restart`. Remove
everything with `make uninstall` (your `~/.kith` data is kept).

Windows (PowerShell; builds from source, or pass `-Binary <kith.exe>`):

```powershell
powershell -ExecutionPolicy Bypass -File scripts\install.ps1
```

This installs `%LOCALAPPDATA%\Programs\kith\kith.exe`, adds it to your user
PATH, and adds tab completion to `$PROFILE`. If the service is running, run
it from an elevated terminal so it can stop and restart the service.

## Global flags

| flag | |
|---|---|
| `--home DIR` | Kith home (default `$KITH_HOME`, else `~/.kith`) |
| `--json` | machine-readable output |
| `--no-color` | plain output (also `NO_COLOR=1`; colors are off when piped) |
| `--version`, `-v` | version + git commit |

## Commands

### Tasks
| command | |
|---|---|
| `kith run [flags] -- <cmd> [args…]` | run a command on a peer without a task.toml; streams logs; exits with the task result |
| `kith submit -f task.toml [--peer P]` | submit a TOML task spec |
| `kith tasks [--active] [--pending] [--state S] [--peer P] [--role in\|out] [-n N\|--all]` | list tasks |
| `kith task <ID>` | timeline, argv, artifacts, detail and fix |
| `kith logs <ID> [-f]` | task output (`-f` follows until it finishes) |
| `kith wait <ID> [--timeout 30m]` | block until finished |
| `kith cancel <ID>` | cancel and wait for the final state |
| `kith approve [<ID>]` | approve a pending task; without an ID, list what is waiting |
| `kith deny <ID>` | reject a pending task (`POLICY_DENIED`) |
| `kith artifacts [<task>]` | registry: local / remote / remote (producer offline) |
| `kith fetch <task> <path>` | pull a retained artifact, verify sha256 |

`kith run` flags: `--peer`, `-i/--input` (repeatable, globs), `-o/--output`
(repeatable), `--dir` (default: current directory), `--python '>=3.10'`,
`--pkg 'torch>=2.3+cu121'` (repeatable), `--gpu <GB>`, `--os macos|windows`,
`--arch`, `--probe '<cmd>'`, `--on-interrupt fail|restart|resume-if-checkpoint`,
`--checkpoint <glob>`, `--timeout <min>`, `--retain-cap <GB>`,
`--no-copy-back`, `--max-input <GB>`, `--id`, `--name`, `--project`,
`-d/--detach`.

```bash
kith run --peer HARI-PC -i train.py -i dataset.zip -o 'out/*' \
    --gpu 4 --pkg 'torch>=2.3+cu121' -- python -u train.py --epochs 5
```

Ctrl-C while following stops following only; the task keeps running.

### Peers & pairing
| command | |
|---|---|
| `kith pair [--ip ADDR] [--yes]` | pair (run on both machines; compare the code) |
| `kith peers` | peers, status, policy, address, capabilities |
| `kith peers set-policy <peer> ask\|auto` | per-peer approval policy |
| `kith unpair <peer>` | forget a peer |

### Daemon & setup
| command | |
|---|---|
| `kith status` | overview: daemon, identity, peers, active tasks, approvals |
| `kith daemon` | run in the foreground |
| `kith daemon start\|stop\|restart` | background control (uses the service if installed for this home) |
| `kith daemon logs [-f] [-n N]` | daemon log (background/service mode) |
| `kith daemon install\|uninstall\|status` | LaunchAgent / Windows service |
| `kith config list\|get\|set\|unset\|path` | `kith.toml` settings: `name port default_policy log_level bind mdns` |
| `kith id` · `kith probe` · `kith doctor` | identity, capability manifest, diagnostics |
| `kith mcp` | MCP server on stdio for AI agents |
| `kith version` · `kith completion zsh\|bash\|fish\|powershell` | |

## Exit codes

| code | meaning |
|---|---|
| 0 | success / task COMPLETED |
| 1 | error (usage, daemon not running, connection, …) |
| 2 | task FAILED |
| 3 | task CANCELLED |
| 4 | task REJECTED (capability mismatch, policy denied, busy) |
| 5 | `kith wait --timeout` elapsed |

Errors print `CODE: message` and, when known, `Fix: …`.
