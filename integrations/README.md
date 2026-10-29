# Use Kith from AI agents

Tell any agent "I have a Kith-connected computer". It can then run jobs on
your other machine (for example the Windows PC with the GPU) and follow
them live, epoch by epoch, without you watching.

Every agent gets the same two pieces:

- **The MCP server, `kith mcp`.** Its tools:
  - `list_peers`, `check_task`, `submit_task` and `list_tasks`
  - `wait_task` and `task_progress`, which give live metrics such as
    accuracy, F1 and loss per epoch
  - `fetch_artifact`, `get_task`, `stream_logs` and `cancel_task`

  The server also sends usage instructions, so an agent that only has the
  server still knows the workflow.
- **The Kith skill (`SKILL.md`).** It covers when to use Kith, the task
  format, how to print metrics, fitting jobs to the GPU, and errors.
  Claude Code, Codex, Antigravity, Cursor and Gemini CLI all read this
  Agent Skills format.

## One command

```bash
kith agents install            # every agent found on this machine
kith agents install codex      # or name them: claude codex antigravity cursor gemini
kith agents                    # what's connected
kith agents uninstall          # undo
```

Then restart the agent. Run this on each machine whose agents should use
Kith. It works the same on macOS, Windows and Linux.

| Agent | MCP server goes in | Skill goes in |
|---|---|---|
| Claude Code | `claude mcp add --scope user kith` | `~/.claude/skills/kith/` |
| Codex (CLI and app) | `~/.codex/config.toml`, one marked block (`tool_timeout_sec = 660` so `wait_task` can block) | `~/.codex/skills/kith/` |
| Antigravity | `~/.gemini/config/mcp_config.json` | `~/.gemini/config/skills/kith/` |
| Cursor | `~/.cursor/mcp.json` | `~/.cursor/skills/kith/` |
| Gemini CLI | `~/.gemini/settings.json` | `~/.gemini/skills/kith/` |

How the installer edits configs:
- JSON configs are merged, not replaced, and a `.kith-backup` copy is kept.
- It writes the absolute path of `kith`, so GUI apps that don't see your
  shell's PATH still find it.
- It never touches a `kith` entry you wrote yourself.

## Other agents, or by hand

Any MCP client works. Print the entry with `kith agents print`:

```json
{ "mcpServers": { "kith": { "command": "/path/to/kith", "args": ["mcp"] } } }
```

For agents that read Agent Skills, run `kith agents skill > <skills dir>/kith/SKILL.md`.
For agents that only take instructions, paste [skill/kith/SKILL.md](skill/kith/SKILL.md)
into their rules file (`AGENTS.md`, `GEMINI.md`, `.cursorrules`...).

## Plugin bundles

Use these if you prefer a plugin manager. They start `kith` from PATH.

- **Claude Code:** run `/plugin marketplace add <repo>/integrations/claude-code`,
  then `/plugin install kith@kith`.
- **Antigravity:** copy `antigravity/kith/` to `~/.gemini/config/plugins/kith/`,
  or to `.agents/plugins/kith/` in a project.
- **Codex, Cursor and others:** use `kith agents install`, or [mcp.json](mcp.json)
  plus [skill/](skill/).

These are generated from `internal/agents` by `make integrations`. Don't
edit them by hand.

## Running without being asked

By default the other machine asks before it runs a task from this one, so
a person has to press Approve. To let agents work unattended, open Kith on
the other machine, go to Machines, select this machine, and switch its
policy from Ask to Auto (or run `kith peers set-policy <this machine> auto`
there). Your agent may still ask you to allow its MCP tools the first time.
That's the agent's own setting.

## Try it

Fetch the benchmark data once:

```bash
cd examples/cifar-bench && python3 fetch_cifar.py
```

Then ask your agent:

> I have a Kith-connected computer. Run the CIFAR benchmark in
> examples/cifar-bench on it and tell me the accuracy and F1 after each
> epoch.

The agent checks the task against the peer's GPU, sends it, and reports
lines like `epoch 7/20: val_acc 0.884, macro-F1 0.881, 24 s/epoch, about 5 min left`.
At the end it summarises `out/benchmark.json`.
