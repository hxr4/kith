"""Reverse-direction demo: runs on the Mac, submitted from Windows.

Stdlib only. Hashes the transferred input, streams progress, and returns a
report plus a derived binary artifact.
"""
import hashlib, json, os, platform, sys, time

os.makedirs("out", exist_ok=True)
print("running on", platform.system(), platform.machine(), "python", platform.python_version(), flush=True)
data = open("data.bin", "rb").read()
h = hashlib.sha256()
for i in range(0, len(data), 1 << 20):
    h.update(data[i:i + (1 << 20)])
    print("hashed %d / %d MiB" % (min(len(data), i + (1 << 20)) >> 20, len(data) >> 20), flush=True)
    time.sleep(0.2)
digest = h.hexdigest()
open("out/digest.bin", "wb").write(bytes.fromhex(digest) * 1024)
json.dump({"host": platform.node(), "os": platform.platform(), "input_bytes": len(data), "input_sha256": digest,
           "task_id": os.environ.get("KITH_TASK_ID")}, open("out/report.json", "w"), indent=1)
print("input sha256", digest, flush=True)
