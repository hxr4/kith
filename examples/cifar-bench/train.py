"""Kith GPU benchmark: ResNet-18 on CIFAR-10, pure PyTorch (no torchvision).

Sized for a 6 GB laptop GPU such as the RTX 3050:
  - the dataset lives on the GPU as uint8 (~180 MB) and augmentation (random
    crop + flip) runs there, so there are no DataLoader workers to spawn;
  - mixed precision (fp16 + GradScaler) and channels_last;
  - batch 256 by default (~2.5 GB peak); a startup probe halves the batch
    until a training step fits, so smaller GPUs still run;
  - --max-vram-gb caps this process's share of GPU memory.

It checkpoints every epoch (out/checkpoint.pt) and resumes from it, so Kith's
resume-if-checkpoint policy survives a dropped connection or a reboot.

Progress is printed for people and, one JSON object per line, for agents:
    KITH_METRIC {"kind":"epoch","epoch":3,"epochs":20,"val_acc":0.81,...}
`kith mcp` (task_progress / wait_task) parses these lines.

Outputs: out/model.pt, out/metrics.json (per-epoch history),
out/benchmark.json (summary), out/confusion.csv.
"""
import argparse
import contextlib
import json
import math
import os
import pickle
import platform
import tarfile
import time

import numpy as np
import torch
import torch.nn as nn
import torch.nn.functional as F

CLASSES = ["airplane", "automobile", "bird", "cat", "deer", "dog", "frog", "horse", "ship", "truck"]
MEAN = (0.4914, 0.4822, 0.4465)
STD = (0.2470, 0.2435, 0.2616)


def metric(**kw):
    """One machine-readable progress record (see the module docstring)."""
    print("KITH_METRIC " + json.dumps(kw, separators=(",", ":")), flush=True)


def load_cifar(path):
    xs, ys = {"train": [], "test": []}, {"train": [], "test": []}
    with tarfile.open(path, "r:gz") as t:
        for m in t.getmembers():
            name = os.path.basename(m.name)
            split = "train" if name.startswith("data_batch_") else "test" if name == "test_batch" else None
            if split is None:
                continue
            d = pickle.load(t.extractfile(m), encoding="bytes")
            xs[split].append(np.asarray(d[b"data"], dtype=np.uint8).reshape(-1, 3, 32, 32))
            ys[split].append(np.asarray(d[b"labels"], dtype=np.int64))
    return {s: (np.concatenate(xs[s]), np.concatenate(ys[s])) for s in xs}


# ---- model: ResNet-18, CIFAR variant (3x3 stem, no max-pool) ----

class Block(nn.Module):
    def __init__(self, cin, cout, stride):
        super().__init__()
        self.c1 = nn.Conv2d(cin, cout, 3, stride, 1, bias=False)
        self.b1 = nn.BatchNorm2d(cout)
        self.c2 = nn.Conv2d(cout, cout, 3, 1, 1, bias=False)
        self.b2 = nn.BatchNorm2d(cout)
        self.short = nn.Sequential()
        if stride != 1 or cin != cout:
            self.short = nn.Sequential(nn.Conv2d(cin, cout, 1, stride, bias=False), nn.BatchNorm2d(cout))

    def forward(self, x):
        y = F.relu(self.b1(self.c1(x)), inplace=True)
        y = self.b2(self.c2(y))
        return F.relu(y + self.short(x), inplace=True)


class ResNet18(nn.Module):
    def __init__(self, width=64, classes=10):
        super().__init__()
        w = width
        self.stem = nn.Sequential(nn.Conv2d(3, w, 3, 1, 1, bias=False), nn.BatchNorm2d(w), nn.ReLU(inplace=True))
        layers, cin = [], w
        for i, cout in enumerate([w, 2 * w, 4 * w, 8 * w]):
            for j in range(2):
                layers.append(Block(cin, cout, 2 if (j == 0 and i > 0) else 1))
                cin = cout
        self.layers = nn.Sequential(*layers)
        self.fc = nn.Linear(cin, classes)

    def forward(self, x):
        x = self.layers(self.stem(x))
        return self.fc(torch.flatten(F.adaptive_avg_pool2d(x, 1), 1))


# ---- GPU-side batches ----

def autocast(amp):
    """fp16 mixed precision on CUDA; a no-op elsewhere (MPS/CPU run fp32)."""
    return torch.autocast("cuda", dtype=torch.float16) if amp else contextlib.nullcontext()


MEMORY_FORMAT = torch.contiguous_format  # channels_last on CUDA (set in main)


def normalize(xb, mean, std):
    return ((xb.float() / 255.0 - mean) / std).contiguous(memory_format=MEMORY_FORMAT)


def augment(xb, gen):
    """Random 32x32 crop from a 4-px reflect-padded image, then a random flip."""
    b = xb.shape[0]
    xp = F.pad(xb.float(), (4, 4, 4, 4), mode="reflect")
    ox = torch.randint(0, 9, (b,), device=xb.device, generator=gen)
    oy = torch.randint(0, 9, (b,), device=xb.device, generator=gen)
    r = torch.arange(32, device=xb.device)
    iy = (oy[:, None] + r)[:, :, None]  # b,32,1
    ix = (ox[:, None] + r)[:, None, :]  # b,1,32
    bi = torch.arange(b, device=xb.device)[:, None, None]
    out = xp.permute(0, 2, 3, 1)[bi, iy, ix].permute(0, 3, 1, 2)  # b,3,32,32
    flip = torch.rand(b, device=xb.device, generator=gen) < 0.5
    out = torch.where(flip[:, None, None, None], out.flip(3), out)
    return out


@torch.no_grad()
def evaluate(model, x, y, mean, std, amp):
    model.eval()
    conf = torch.zeros(10, 10, dtype=torch.long, device=x.device)
    loss_sum = 0.0
    for i in range(0, x.shape[0], 1000):
        xb, yb = x[i:i + 1000], y[i:i + 1000]
        with autocast(amp):
            logits = model(normalize(xb, mean, std))
        loss_sum += F.cross_entropy(logits.float(), yb, reduction="sum").item()
        pred = logits.argmax(1)
        conf += torch.bincount(yb * 10 + pred, minlength=100).view(10, 10)
    model.train()
    conf = conf.cpu().numpy()
    tp = np.diag(conf).astype(float)
    fp = conf.sum(0) - tp
    fn = conf.sum(1) - tp
    f1 = np.where(2 * tp + fp + fn > 0, 2 * tp / np.maximum(2 * tp + fp + fn, 1), 0.0)
    return {"val_loss": loss_sum / x.shape[0], "val_acc": float(tp.sum() / conf.sum()),
            "macro_f1": float(f1.mean()), "per_class_f1": {c: round(float(v), 4) for c, v in zip(CLASSES, f1)}}, conf


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--data", default="cifar-10-python.tar.gz")
    ap.add_argument("--epochs", type=int, default=20)
    ap.add_argument("--batch", type=int, default=256)
    ap.add_argument("--lr", type=float, default=0.2, help="peak learning rate (OneCycle) at batch 256")
    ap.add_argument("--width", type=int, default=64, help="base channels (64 = standard ResNet-18, 11.2M params)")
    ap.add_argument("--max-vram-gb", type=float, default=0, help="cap this process's GPU memory (0 = no cap)")
    ap.add_argument("--limit", type=int, default=0, help="use only the first N training images (smoke tests)")
    ap.add_argument("--device", default="auto", help="auto | cuda | mps | cpu")
    ap.add_argument("--log-every", type=int, default=50, help="steps between progress records")
    ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--out", default="out")
    a = ap.parse_args()
    os.makedirs(a.out, exist_ok=True)

    if a.device == "auto":
        a.device = "cuda" if torch.cuda.is_available() else "mps" if torch.backends.mps.is_available() else "cpu"
    if a.device == "cuda" and not torch.cuda.is_available():
        raise SystemExit(f"--device cuda, but this torch ({torch.__version__}) sees no CUDA GPU; "
                         "install a CUDA build of torch or check the NVIDIA driver (nvidia-smi)")
    dev = torch.device(a.device)
    torch.manual_seed(a.seed)
    gen = torch.Generator(device=dev).manual_seed(a.seed)
    amp = dev.type == "cuda"
    gpu_name, vram_total = None, None
    if dev.type == "cuda":
        # channels_last speeds up convolutions on tensor cores (other backends,
        # notably MPS, mishandle it in backward).
        global MEMORY_FORMAT
        MEMORY_FORMAT = torch.channels_last
        torch.backends.cudnn.benchmark = True
        props = torch.cuda.get_device_properties(0)
        gpu_name, vram_total = props.name, props.total_memory / 2**30
        if a.max_vram_gb > 0:
            torch.cuda.set_per_process_memory_fraction(min(1.0, a.max_vram_gb / vram_total), 0)
    print(f"device: {gpu_name or dev}  torch {torch.__version__}  python {platform.python_version()}"
          + (f"  VRAM {vram_total:.1f} GB" if vram_total else ""), flush=True)

    t0 = time.time()
    data = load_cifar(a.data)
    xtr, ytr = data["train"]
    if a.limit:
        xtr, ytr = xtr[:a.limit], ytr[:a.limit]
    xte, yte = data["test"]
    xtr, ytr = torch.from_numpy(xtr).to(dev), torch.from_numpy(ytr).to(dev)
    xte, yte = torch.from_numpy(xte).to(dev), torch.from_numpy(yte).to(dev)
    mean = torch.tensor(MEAN, device=dev).view(1, 3, 1, 1)
    std = torch.tensor(STD, device=dev).view(1, 3, 1, 1)
    print(f"data: {len(xtr)} train / {len(xte)} test images loaded in {time.time() - t0:.1f}s", flush=True)

    model = ResNet18(a.width).to(dev).to(memory_format=MEMORY_FORMAT)
    params = sum(p.numel() for p in model.parameters())
    scaler = torch.amp.GradScaler(dev.type, enabled=amp)

    # Find a batch size that fits: try one full training step, halve on OOM.
    batch = a.batch
    if dev.type == "cuda":
        probe_opt = torch.optim.SGD(model.parameters(), lr=0.0)
        while True:
            try:
                xb = augment(xtr[:batch], gen)
                with autocast(amp):
                    loss = F.cross_entropy(model(normalize(xb, mean, std)), ytr[:batch])
                scaler.scale(loss).backward()
                probe_opt.zero_grad(set_to_none=True)
                torch.cuda.synchronize()
                break
            except torch.OutOfMemoryError:
                probe_opt.zero_grad(set_to_none=True)
                torch.cuda.empty_cache()
                if batch <= 32:
                    raise
                batch //= 2
                print(f"out of GPU memory; retrying with batch {batch}", flush=True)
        del probe_opt
        scaler = torch.amp.GradScaler("cuda", enabled=amp)
        torch.cuda.reset_peak_memory_stats()

    steps = math.ceil(len(xtr) / batch)
    peak_lr = a.lr * batch / 256
    opt = torch.optim.SGD(model.parameters(), lr=peak_lr, momentum=0.9, nesterov=True, weight_decay=5e-4)
    sched = torch.optim.lr_scheduler.OneCycleLR(opt, max_lr=peak_lr, epochs=a.epochs, steps_per_epoch=steps,
                                                pct_start=0.25, div_factor=10, final_div_factor=100)
    history, start, best = [], 0, 0.0
    ckpt = os.path.join(a.out, "checkpoint.pt")
    if os.path.exists(ckpt):
        s = torch.load(ckpt, map_location=dev, weights_only=False)
        if s.get("batch") == batch and s.get("epochs") == a.epochs:
            model.load_state_dict(s["model"])
            opt.load_state_dict(s["opt"])
            sched.load_state_dict(s["sched"])
            scaler.load_state_dict(s["scaler"])
            history, start, best = s["history"], s["epoch"], s["best"]
            print(f"resumed from checkpoint after epoch {start}", flush=True)

    print(f"model: ResNet-18 width {a.width}, {params / 1e6:.1f}M params; batch {batch}, {steps} steps/epoch, "
          f"{a.epochs} epochs, peak lr {peak_lr:.3f}, {'fp16 mixed precision' if amp else 'fp32'}", flush=True)
    metric(kind="start", epochs=a.epochs, start_epoch=start, batch=batch, steps=steps, params=params,
           device=gpu_name or str(dev), vram_gb=round(vram_total, 2) if vram_total else None, torch=torch.__version__)

    crit = nn.CrossEntropyLoss(label_smoothing=0.1)
    run_t0 = time.time()
    conf = None
    for ep in range(start, a.epochs):
        model.train()
        et0 = time.time()
        perm = torch.randperm(len(xtr), device=dev, generator=gen)
        seen = 0
        win_t0, win_n = time.time(), 0
        for i in range(steps):
            idx = perm[i * batch:(i + 1) * batch]
            xb, yb = augment(xtr[idx], gen), ytr[idx]
            with autocast(amp):
                logits = model(normalize(xb, mean, std))
                loss = crit(logits, yb)
            opt.zero_grad(set_to_none=True)
            scaler.scale(loss).backward()
            scaler.step(opt)
            scaler.update()
            sched.step()
            n = yb.shape[0]
            seen += n
            win_n += n
            if (i + 1) % a.log_every == 0 or i + 1 == steps:
                # Syncing here (not every step) keeps the GPU queue full.
                rate = win_n / max(time.time() - win_t0, 1e-6)
                metric(kind="step", epoch=ep + 1, epochs=a.epochs, step=i + 1, steps=steps,
                       loss=round(loss.item(), 4), lr=round(sched.get_last_lr()[0], 5), img_s=round(rate))
                win_t0, win_n = time.time(), 0
        # Exact training accuracy on a fixed 5k-image sample, without augmentation.
        with torch.no_grad(), autocast(amp):
            model.eval()
            sub = slice(0, min(5000, len(xtr)))
            train_acc = (model(normalize(xtr[sub], mean, std)).argmax(1) == ytr[sub]).float().mean().item()
            model.train()
        ev, conf = evaluate(model, xte, yte, mean, std, amp)
        if dev.type == "cuda":
            torch.cuda.synchronize()
        dt = time.time() - et0
        best = max(best, ev["val_acc"])
        rec = {"epoch": ep + 1, "epochs": a.epochs, "train_loss": round(loss.item(), 4), "train_acc": round(train_acc, 4),
               "val_loss": round(ev["val_loss"], 4), "val_acc": round(ev["val_acc"], 4), "macro_f1": round(ev["macro_f1"], 4),
               "best_val_acc": round(best, 4), "lr": round(sched.get_last_lr()[0], 5), "epoch_s": round(dt, 1),
               "img_s": round(seen / dt), "eta_s": round(dt * (a.epochs - ep - 1))}
        if dev.type == "cuda":
            rec["peak_vram_gb"] = round(torch.cuda.max_memory_allocated() / 2**30, 2)
        history.append(rec)
        print(f"epoch {ep + 1}/{a.epochs}  loss {rec['train_loss']:.3f}  train_acc {rec['train_acc']:.3f}  "
              f"val_acc {rec['val_acc']:.4f}  macro_f1 {rec['macro_f1']:.4f}  {dt:.1f}s  {rec['img_s']} img/s"
              + (f"  peak {rec['peak_vram_gb']} GB" if "peak_vram_gb" in rec else ""), flush=True)
        metric(kind="epoch", **rec)
        torch.save({"model": model.state_dict(), "opt": opt.state_dict(), "sched": sched.state_dict(),
                    "scaler": scaler.state_dict(), "epoch": ep + 1, "history": history, "best": best,
                    "batch": batch, "epochs": a.epochs}, ckpt)

    total = time.time() - run_t0
    if conf is None:  # resumed after the last epoch
        ev, conf = evaluate(model, xte, yte, mean, std, amp)
    torch.save(model.state_dict(), os.path.join(a.out, "model.pt"))
    json.dump(history, open(os.path.join(a.out, "metrics.json"), "w"), indent=1)
    with open(os.path.join(a.out, "confusion.csv"), "w") as f:
        f.write("true\\pred," + ",".join(CLASSES) + "\n")
        for c, row in zip(CLASSES, conf):
            f.write(c + "," + ",".join(str(int(v)) for v in row) + "\n")
    last = history[-1] if history else {}
    bench = {"model": "ResNet-18 (CIFAR)", "params": params, "dataset": "CIFAR-10", "device": gpu_name or str(dev),
             "torch": torch.__version__, "python": platform.python_version(), "epochs": a.epochs, "batch": batch,
             "precision": "fp16-amp" if amp else "fp32", "final_val_acc": last.get("val_acc"),
             "best_val_acc": round(best, 4), "final_macro_f1": round(ev["macro_f1"], 4), "per_class_f1": ev["per_class_f1"],
             "train_seconds": round(total, 1),
             "mean_epoch_s": round(sum(h["epoch_s"] for h in history) / max(len(history), 1), 1),
             "mean_img_s": round(sum(h["img_s"] for h in history) / max(len(history), 1)),
             "peak_vram_gb": max((h.get("peak_vram_gb", 0) for h in history), default=None)}
    json.dump(bench, open(os.path.join(a.out, "benchmark.json"), "w"), indent=1)
    metric(kind="done", **{k: v for k, v in bench.items() if k != "per_class_f1"})
    print(f"done: val_acc {bench['final_val_acc']}  macro_f1 {bench['final_macro_f1']}  "
          f"{bench['train_seconds']}s  {bench['mean_img_s']} img/s", flush=True)


if __name__ == "__main__":
    main()
