---
name: kith
description: Run commands, training jobs and other GPU or heavy work on the user's other Kith-paired computer (for example a Windows PC with an NVIDIA GPU), and follow its live progress (epochs, loss, accuracy, F1) without asking the user to watch it. Use when the user says they have a Kith-connected or paired computer, mentions their other machine or its GPU, or when a job needs CUDA, more VRAM or more compute than this machine has.
---

# Kith: use the user's other computer

Kith pairs two of the user's own computers on the local network (mutually
authenticated, end-to-end encrypted). From this machine you can send a
task to the other one: Kith copies the input files over, runs an argv
command in a fresh workspace there, streams the output back, and copies
the outputs back here. Typical use: this is a laptop without a CUDA GPU,
and the paired machine has one.

Work on your own. Check, send and follow the task without asking the user
at each step, and report progress as it happens.

## Tools (MCP server `kith`)

| Tool | Use it to |
|---|---|
| `list_peers` | See the paired machine: name, online, OS, Python, packages (torch, numpy...), GPU and VRAM, free disk |
| `check_task` | Preflight a task before sending; each requirement gets pass, fail or unknown |
| `submit_task` | Send a task (`peer` is optional when one machine is paired) |
| `wait_task` | Block until the next epoch (`until=epoch`), any change (`until=change`) or the end (`until=finish`); default 50 s |
| `list_tasks` | Find tasks, newest first, in both directions (`active: true` for unfinished ones); use it when the user mentions "the run" |
| `task_progress` | Get state, the latest and best metrics, the current step, recent output lines and artifacts in one call |
| `fetch_artifact` | Bring an output file here (if it stayed on the peer) |
| `get_task`, `stream_logs`, `cancel_task` | Get the full record, read the raw output, or stop the task |

If the MCP tools aren't available, use the `kith` CLI. Every command takes
`--json`. Use `kith peers`, `kith check -f task.toml`, `kith submit -f task.toml`,
`kith logs <id> -f`, `kith wait <id>`, `kith task <id>`, `kith fetch <id> <path>`,
and `kith run -i <input> -o 'out/*' -- <cmd...>` for one-off commands.

## Workflow

1. **Look.** Call `list_peers`. Note the GPU, VRAM, Python version and
   packages, then size the job to fit them (see Constraints below).
2. **Write the task.** Prefer a `task.toml` next to the code (format below).
   Alternatively, pass `spec` as an object with the same fields.
3. **Check.** Call `check_task`. Fix any `fail` before sending: change a
   version range, drop a package the peer lacks, or reduce `vram_gb`.
4. **Send.** Call `submit_task` with `spec_file` (or `spec` plus `project_dir`).
5. **Follow.** Call `wait_task` with `until="epoch"` in a loop until
   `complete` is true. After each epoch, tell the user one short line, for
   example `epoch 7/20: val_acc 0.884, macro-F1 0.881 (best 0.884), 24 s/epoch, about 5 min left`.
   Don't paste raw logs. Use `tail` only to explain a problem.
6. **Finish.** When the task completes, outputs with `copy_back` are
   already in `project_dir`. Otherwise call `fetch_artifact`. Read the
   results (for example `out/benchmark.json`) and summarise them.
7. **On failure,** read `code`, `detail`, `remediation` and the `tail`
   lines. Fix the cause and resubmit. Don't resubmit unchanged.
8. **Don't cancel a healthy run silently.** If you must cancel, for example
   because the run will hit `timeout_minutes` (a new task starts from
   scratch, since the timeout can't be raised on a running task), first
   tell the user in one line why, then what you resubmit. If it's only
   slow, report the throughput and keep going. Size `timeout_minutes`
   generously at the start (2 to 3 times your estimate).

### Approval

If `submit_task` returns `pending_approval: true`, the other machine asks
before it runs tasks from this one. Tell the user once: "Approve the task
on <peer> (notification or Kith app)". To stop being asked, they can set
this machine's policy from Ask to Auto on the peer (Kith app › Machines).
Keep calling `wait_task`; it returns as soon as the task starts.

## Task format (`task.toml`)

```toml
[task]
project = "my-project"                  # groups tasks; any name
name = "train"
command = ["python", "-u", "train.py", "--epochs", "20", "--out", "out/"]

[task.inputs]
files = ["train.py", "data/*.npz"]      # relative to project_dir; globs ok

[task.env]
python = ">=3.10"
packages = { torch = ">=2.3", numpy = ">=1.24" }
gpu = { vram_gb = 4, required = true }  # omit for CPU work

[task.outputs]
include = ["out/model.pt", "out/metrics.json"]
copy_back = true                        # copy outputs back to project_dir

[task.policy]
on_interrupt = "resume-if-checkpoint"   # or "restart", "fail"
checkpoint = { pattern = "out/checkpoint*.pt" }
timeout_minutes = 120
```

Notes:
- The command is an argv list, not a shell line. On a Windows peer the
  interpreter is usually `python`, not `python3`.
- Always pass `-u` to Python so output streams live.
- Prefer failing fast inside the task (for example `--device cuda`, which
  raises at once without a GPU) over `env.probe`. A probe runs before the
  task is accepted, and a first `import torch` on Windows can take a
  minute or more. If a task is rejected because a probe timed out, drop
  the probe and resubmit.
- Only the files listed under `inputs` are sent. Keep datasets as files in
  the project, not absolute paths.
- The peer has no internet guarantee. Ship data as inputs rather than
  downloading it there.

## Reporting metrics (so you can follow the run)

Have the training script print one JSON line per epoch, prefixed with `KITH_METRIC `:

```python
import json
def metric(**kw): print("KITH_METRIC " + json.dumps(kw), flush=True)

metric(kind="epoch", epoch=ep, epochs=total, train_loss=tl, val_acc=acc, macro_f1=f1, epoch_s=dt)
metric(kind="step", epoch=ep, step=i, steps=n, loss=l)    # optional, mid-epoch
metric(kind="done", final_val_acc=acc, final_macro_f1=f1)  # optional summary
```

`task_progress` and `wait_task` then return `latest`, `best` (higher is
better for accuracy and F1, lower for loss), `recent`, `current_step` and
`done`. Scripts that only print lines like `epoch 3/20 loss=0.41 f1: 0.88`
are parsed too (`metrics_source: "parsed from output"`), but the
`KITH_METRIC` format is exact.

## Constraints: fit the job to the GPU

Read the VRAM from `list_peers` and leave headroom.
- **Under 8 GB of VRAM** (for example an RTX 3050 with 6 GB):
  - use mixed precision (`torch.autocast("cuda", dtype=torch.float16)` with `GradScaler`)
  - use moderate batches (128 to 256 for small CNNs), and halve the batch on `torch.OutOfMemoryError`
  - avoid `torch.compile` on Windows (no Triton)
  - keep `num_workers=0` on Windows, or put small datasets on the GPU
- Checkpoint every epoch and resume from it, so the
  `resume-if-checkpoint` policy survives a reboot or a dropped network.
- Write outputs under one directory (for example `out/`) and list them
  in `outputs.include`.

## Example: GPU benchmark

`examples/cifar-bench` in the Kith repository trains ResNet-18 on CIFAR-10
(pure PyTorch, sized for 6 GB). It prints `KITH_METRIC` lines for every
epoch, with accuracy, macro-F1, throughput and peak VRAM. To run it:

1. On this machine, run `python3 fetch_cifar.py` once.
2. Call `check_task` with `spec_file: "<repo>/examples/cifar-bench/task.toml"`.
3. Call `submit_task` with the same `spec_file`.
4. Call `wait_task` with `until="epoch"` until it completes.
5. Summarise `out/benchmark.json`.

## Errors

| `code` | Meaning | What to do |
|---|---|---|
| `NO_MATCH` | The peer can't meet a requirement | Read `remediation`. Relax the requirement or change the task |
| `PEER_UNREACHABLE` | The peer is offline or unreachable | Ask the user to open Kith on it. Accepted tasks resume when it's back |
| `POLICY_DENIED` | Someone pressed Deny on the peer | Ask the user before resending |
| `BUSY` | The peer's queue is full | Wait, then resubmit |
| `TIMEOUT` | `timeout_minutes` passed | Raise the timeout or shrink the job |
| `CANCELLED_BY_PEER` | Stopped on the other machine | Ask the user before resending |
