"""CUDA MNIST training for the Kith demo.

Reads MNIST from dataset.zip (no downloads on the executor), trains a small
CNN, prints one line per epoch, checkpoints every epoch (resume-safe) and
writes out/model.pt + out/metrics.json.
"""
import argparse, gzip, io, json, os, time, zipfile

import numpy as np
import torch
import torch.nn as nn
import torch.nn.functional as F

p = argparse.ArgumentParser()
p.add_argument("--epochs", type=int, default=5)
p.add_argument("--out", default="out/")
p.add_argument("--device", default="cuda")
a = p.parse_args()
os.makedirs(a.out, exist_ok=True)

if a.device == "cuda" and not torch.cuda.is_available():
    raise SystemExit("CUDA not available on this machine")
dev = torch.device(a.device if a.device != "cuda" or torch.cuda.is_available() else "cpu")
print("device:", torch.cuda.get_device_name(0) if dev.type == "cuda" else dev, flush=True)

def idx(z, name):
    raw = gzip.decompress(z.read(name))
    if name.endswith("idx3-ubyte.gz"):
        return np.frombuffer(raw, np.uint8, offset=16).reshape(-1, 1, 28, 28)
    return np.frombuffer(raw, np.uint8, offset=8)

with zipfile.ZipFile("dataset.zip") as z:
    xtr, ytr = idx(z, "train-images-idx3-ubyte.gz"), idx(z, "train-labels-idx1-ubyte.gz")
    xte, yte = idx(z, "t10k-images-idx3-ubyte.gz"), idx(z, "t10k-labels-idx1-ubyte.gz")
xtr = torch.tensor(xtr, dtype=torch.float32, device=dev) / 255.0
ytr = torch.tensor(ytr, dtype=torch.long, device=dev)
xte = torch.tensor(xte, dtype=torch.float32, device=dev) / 255.0
yte = torch.tensor(yte, dtype=torch.long, device=dev)

class Net(nn.Module):
    def __init__(self):
        super().__init__()
        self.c1, self.c2 = nn.Conv2d(1, 32, 3, 1), nn.Conv2d(32, 64, 3, 1)
        self.f1, self.f2 = nn.Linear(9216, 128), nn.Linear(128, 10)
    def forward(self, x):
        x = F.max_pool2d(F.relu(self.c2(F.relu(self.c1(x)))), 2)
        return self.f2(F.relu(self.f1(torch.flatten(x, 1))))

torch.manual_seed(0)
net = Net().to(dev)
opt = torch.optim.Adam(net.parameters(), lr=1e-3)
start = 0
ckpt = os.path.join(a.out, "checkpoint.pt")
if os.path.exists(ckpt):  # resume-if-checkpoint: the script detects its own checkpoint
    s = torch.load(ckpt, map_location=dev)
    net.load_state_dict(s["model"]); opt.load_state_dict(s["opt"]); start = s["epoch"]
    print("resumed from checkpoint at epoch", start, flush=True)

acc = 0.0
for ep in range(start, a.epochs):
    t0 = time.time(); net.train()
    perm = torch.randperm(len(xtr), device=dev)
    for i in range(0, len(xtr), 256):
        b = perm[i:i + 256]
        opt.zero_grad(); loss = F.cross_entropy(net(xtr[b]), ytr[b]); loss.backward(); opt.step()
    net.eval()
    with torch.no_grad():
        acc = (net(xte).argmax(1) == yte).float().mean().item()
    print(f"epoch {ep + 1}/{a.epochs} loss={loss.item():.4f} test_acc={acc:.4f} ({time.time() - t0:.1f}s)", flush=True)
    torch.save({"model": net.state_dict(), "opt": opt.state_dict(), "epoch": ep + 1}, ckpt)

torch.save(net.state_dict(), os.path.join(a.out, "model.pt"))
json.dump({"epochs": a.epochs, "test_acc": acc}, open(os.path.join(a.out, "metrics.json"), "w"))
print("saved", os.path.join(a.out, "model.pt"), flush=True)
