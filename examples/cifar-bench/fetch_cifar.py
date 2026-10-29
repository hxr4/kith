"""Download CIFAR-10 (python version, 163 MB) for the Kith benchmark.

Run once on the requester:  python3 fetch_cifar.py
Stdlib only. Verifies the published MD5 and keeps the archive as-is; the
training script reads it directly, so the executor needs no torchvision.
The official server is often slow, so a byte-identical Hugging Face mirror
is tried first; the MD5 check makes the source irrelevant.
"""
import hashlib
import os
import sys
import urllib.request

URLS = [
    "https://huggingface.co/datasets/MIT-OL-AI-D/cifar-10-python/resolve/main/cifar-10-python.tar.gz",
    "https://www.cs.toronto.edu/~kriz/cifar-10-python.tar.gz",
]
MD5 = "c58f30108f718f92721af3b95e74349a"

here = os.path.dirname(os.path.abspath(__file__))
dst = os.path.join(here, "cifar-10-python.tar.gz")


def md5(path):
    h = hashlib.md5()
    with open(path, "rb") as f:
        for b in iter(lambda: f.read(1 << 20), b""):
            h.update(b)
    return h.hexdigest()


if os.path.exists(dst) and md5(dst) == MD5:
    print("already have", dst)
    sys.exit(0)

tmp = dst + ".part"
for url in URLS:
    print("downloading", url)
    try:
        with urllib.request.urlopen(url, timeout=30) as r, open(tmp, "wb") as f:
            total = int(r.headers.get("Content-Length", 0))
            done = 0
            while chunk := r.read(1 << 20):
                f.write(chunk)
                done += len(chunk)
                if total:
                    print(f"\r{done / 1e6:.0f}/{total / 1e6:.0f} MB", end="", flush=True)
        print()
    except OSError as e:
        print("\nfailed:", e)
        continue
    if md5(tmp) == MD5:
        os.replace(tmp, dst)
        print("wrote", dst)
        sys.exit(0)
    print("checksum mismatch; trying the next source")
if os.path.exists(tmp):
    os.remove(tmp)
sys.exit("could not download CIFAR-10")
