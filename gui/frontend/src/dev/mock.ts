// Development-only sample backend: `npm run dev`, then open /?mock (add
// &platform=linux, &chrome=linux, &lite, &off, &nopeers to see other states).
// Imported dynamically behind import.meta.env.DEV, so it never ships.
import { api, type Check, type Peer, type Snapshot, type Task } from "../core/api";
import { handlePair, refresh } from "../core/store";

const q = new URLSearchParams(location.search);
const now = Date.now();
const iso = (msAgo: number) => new Date(now - msAgo).toISOString();
const MAC = "Hari's MacBook Pro";

const mac: Peer = {
    fp: "86ae9c40b85a8d0b", display: "KITH-86AE-9C40-B85A-8D0B", name: MAC, policy: "ask", last_addr: "10.157.240.88:7420",
    last_seen: iso(0), reachable: true,
    manifest: { os: "darwin-27.0", arch: "arm64", python: ["3.12.4"], packages: { torch: "2.5.1" }, gpus: [], free_disk_gb: 184, ram_gb: 16 },
};
const peers: Peer[] = q.has("nopeers") ? [] : [mac];

const T = (id: string, state: string, name: string, project: string, role: "executor" | "requester", minsAgo: number, extra: Partial<Task> = {}): Task => ({
    id, state, name, project, role, peer_fp: mac.fp, peer_name: MAC, attempt: 1, pending_approval: false,
    created_at: iso(minsAgo * 60000 + 5000), updated_at: iso(minsAgo * 60000), started_at: iso(minsAgo * 60000), log_size: 0,
    ws_dir: `C:\\Users\\hari\\.kith\\work\\${id}`, ...extra,
});

let tasks: Task[] = [
    T("tsk_render", "SUBMITTED", "render-batch", "blender-shots", "executor", 0.2, { pending_approval: true }),
    T("tsk_mnist", "RUNNING", "mnist-train", "mnist", "executor", 6, { attempt: 2, pid: 20516 }),
    T("tsk_thumbs", "COMPLETED", "thumbs-resize", "photo-archive", "requester", 22, { project_dir: "C:\\Users\\hari\\photos", detail: "3 artifacts copied back" }),
    T("tsk_eval", "FAILED", "eval-suite", "qwen-ft", "executor", 52, { code: "EXIT_NONZERO", detail: "process exited with status 1", remediation: "Lower --batch-size to 8 or load the model in 8-bit, then send it again." }),
    T("tsk_llm", "REJECTED", "llm-finetune", "qwen-ft", "executor", 61, { code: "NO_MATCH", detail: "NO_MATCH: GPU with >=12GB VRAM required (found NVIDIA GeForce RTX 3050 6GB Laptop GPU 6.0GB).", remediation: "submit to a peer with a suitable NVIDIA GPU" }),
];

const details: Record<string, Partial<Task>> = {
    tsk_render: {
        argv: ["blender", "-b", "scenes/shot_04.blend", "-P", "render.py", "-o", "//out/frame_####", "-s", "1", "-e", "240", "-a", "--", "--cycles-device", "CUDA"],
        inputs: [{ path: "scenes/shot_04.blend", size: 48_340_000 }, { path: "hdri/studio_2k.exr", size: 1_990_000 }, { path: "render.py", size: 3100 }],
        requirements: [{ req: "os = windows", have: "windows-11", status: "pass" }, { req: "blender >=4.2", have: "checked when sent", status: "unknown" }, { req: "disk >= 1.0 GB", have: "212.0 GB free", status: "pass" }],
    },
    tsk_mnist: {
        argv: ["python", "-u", "train.py", "--epochs", "3"], outputs: ["out/model.pt", "out/metrics.json"],
        requirements: [{ req: "os = windows", have: "windows-11", status: "pass" }, { req: "python >=3.10", have: "3.12", status: "pass" }, { req: "torch >=2.0", have: "2.5.1+cu121", status: "pass" }, { req: "gpu.vram >= 4 GB", have: "NVIDIA GeForce RTX 3050 6GB Laptop GPU 6.0 GB", status: "pass" }],
        timeline: [
            { seq: 1, state: "SUBMITTED", ts: iso(9 * 60000), detail: "3 files, 11.2 MB" }, { seq: 2, state: "ACCEPTED", ts: iso(9 * 60000 - 1000), detail: "approved" },
            { seq: 3, state: "TRANSFERRING", ts: iso(9 * 60000 - 2000), detail: "11.2 MB received" }, { seq: 4, state: "RUNNING", ts: iso(9 * 60000 - 3000), detail: "pid 18244" },
            { seq: 5, state: "INTERRUPTED", ts: iso(7 * 60000), detail: `${MAC} stopped responding. Policy resume-if-checkpoint keeps the task.` },
            { seq: 6, state: "RUNNING", ts: iso(6 * 60000), detail: "pid 20516 · from checkpoints/epoch1.pt" },
        ],
    },
    tsk_thumbs: {
        argv: ["python", "resize.py", "--max", "2048", "photos/"], outputs: ["out/thumbs.zip", "out/manifest.csv"],
        timeline: [{ seq: 1, state: "SUBMITTED", ts: iso(25 * 60000) }, { seq: 2, state: "ACCEPTED", ts: iso(25 * 60000) }, { seq: 3, state: "RUNNING", ts: iso(24 * 60000) }, { seq: 4, state: "FINALIZING", ts: iso(22 * 60000) }, { seq: 5, state: "COMPLETED", ts: iso(22 * 60000), detail: "exit 0" }],
        artifacts: [
            { id: "a1", task_id: "tsk_thumbs", path: "out/thumbs.zip", sha256: "b41e09aa77d3a", size: 1_700_000, location: "local", producer_fp: mac.fp, fetched: false, copy_back: true, producer_offline: false, status: "local" },
            { id: "a2", task_id: "tsk_thumbs", path: "out/manifest.csv", sha256: "4f02d8bb77c1", size: 412, location: "remote", producer_fp: mac.fp, fetched: false, copy_back: false, producer_offline: false, status: "remote" },
        ],
    },
    tsk_eval: {
        argv: ["python", "eval.py", "--model", "qwen2.5-1.5b", "--suite", "mmlu", "--batch-size", "32"],
        timeline: [{ seq: 1, state: "SUBMITTED", ts: iso(53 * 60000) }, { seq: 2, state: "ACCEPTED", ts: iso(53 * 60000) }, { seq: 3, state: "RUNNING", ts: iso(53 * 60000) }, { seq: 4, state: "FAILED", ts: iso(52 * 60000), detail: "process exited with status 1" }],
    },
    tsk_llm: {
        argv: ["python", "finetune.py", "--model", "qwen2.5-7b"],
        requirements: [{ req: "gpu.vram >= 12 GB", have: "NVIDIA GeForce RTX 3050 6GB Laptop GPU 6.0 GB", status: "fail" }, { req: "disk >= 1.0 GB", have: "212.0 GB free", status: "pass" }],
        timeline: [{ seq: 1, state: "SUBMITTED", ts: iso(61 * 60000) }, { seq: 2, state: "REJECTED", ts: iso(61 * 60000), detail: "NO_MATCH: GPU with >=12GB VRAM required" }],
    },
};

const logs: Record<string, string[]> = {
    tsk_mnist: ["[kith] attempt 2 · policy resume-if-checkpoint · pid 20516", "$ python -u train.py --epochs 3", "Using device: cuda (NVIDIA GeForce RTX 3050 6GB Laptop GPU)",
        "dataloader.py:617: UserWarning: This DataLoader will create 4 worker processes in total."],
    tsk_eval: ["[kith] attempt 1 · pid 9120", "evaluating mmlu: 14042 questions, batch 32", "Traceback (most recent call last):", '  File "eval.py", line 88, in <module>',
        "    logits = model(**batch).logits", "torch.OutOfMemoryError: CUDA out of memory. Tried to allocate 1.20 GiB.", "[kith] exit 1 after 0:52"],
    tsk_thumbs: ["found 1284 images in photos/", "batch 1/6  214 images  0:21", "wrote out/thumbs.zip and out/manifest.csv", "[kith] completed in 2:14"],
};
let step = 400;
setInterval(() => { step += 10; logs.tsk_mnist.push(`epoch 2 step ${step} loss ${(0.11 * Math.exp(-step / 1500)).toFixed(4)}`); }, 700);

const running = !q.has("off");
const snap = (): Snapshot => ({
    running, pending: tasks.filter((t) => t.pending_approval).length, active: 1, lite: q.has("lite"), platform: "windows", gui_version: "1.0.0",
    service: q.has("noservice") ? "not installed" : "installed, running",
    status: running ? { fp: "88ee257ad23b41fd", display: "KITH-88EE-257A-D23B-41FD", name: "HARI-PC", port: 7420, home: "C:\\Users\\hari\\.kith", version: "1.0.0", pid: 4120, peers: peers.length, online: peers.length, mdns: true } : undefined,
    peers: running ? peers : [], tasks: running ? tasks.map((t) => ({ ...t, log_size: (logs[t.id] ?? []).join("\n").length })) : [],
});

const doctor: Check[] = [
    { name: "daemon", status: "ok", detail: "running, pid 4120, KITH-88EE-257A-D23B-41FD (HARI-PC), TCP 7420" },
    { name: "lan address", status: "ok", detail: "Wi-Fi 10.157.240.135/20" },
    { name: "vpn", status: "ok", detail: "no VPN interfaces with IPv4 addresses" },
    { name: "listener", status: "ok", detail: "TCP 10.157.240.135:7420 accepts connections" },
    { name: "firewall", status: q.has("fw") ? "fail" : "ok", detail: q.has("fw") ? "no inbound firewall rule for TCP 7420" : "inbound rule for TCP 7420 (Private profile) present", fix: "run \"kith daemon firewall\" from an elevated terminal" },
    { name: "firewall mdns", status: "ok", detail: "inbound mDNS (UDP 5353) rule for kith.exe present" },
    { name: "network profile", status: q.has("public") ? "warn" : "ok", detail: q.has("public") ? "network HomeNet is Public: the firewall rule only applies on Private networks" : "Private (HomeNet=Private)", fix: "Settings > Network & internet > (your network) > Network profile type: Private." },
    { name: "python", status: "ok", detail: "Python 3.12" },
    { name: "nvidia-smi", status: "ok", detail: "NVIDIA GeForce RTX 3050 6GB Laptop GPU 6.0 GB" },
    { name: "peer " + MAC, status: "ok", detail: "connected via 10.157.240.88:7420" },
    { name: "mdns", status: "ok", detail: "query OK, 1 other kith daemon(s) visible" },
];

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
let config: Record<string, string> = { name: "HARI-PC", port: "7420", default_policy: "ask", log_level: "info", bind: "0.0.0.0", mdns: "true" };
let autostart = true;

export function install() {
    const m: Record<string, (...a: any[]) => Promise<any>> = {
        info: async () => ({ platform: q.get("platform") ?? "windows", chrome: (q.get("chrome") ?? "windows") as "windows", lite: q.has("lite"), mica: false, home: "C:\\Users\\hari\\.kith", user: "hari", version: "1.0.0", log_path: "C:\\Users\\hari\\.kith\\daemon.log" }),
        snapshot: async () => snap(),
        task: async (id) => ({ ...tasks.find((t) => t.id === id)!, ...details[id] } as Task),
        logs: async (id, off) => {
            const text = (logs[id] ?? []).map((l) => l + "\n").join("");
            const t = tasks.find((x) => x.id === id);
            return { task_id: id, offset: text.length, state: t?.state ?? "", done: !!t && ["COMPLETED", "FAILED", "CANCELLED", "REJECTED"].includes(t.state), data: text.slice(off) };
        },
        approve: async (id) => { const t = tasks.find((x) => x.id === id)!; Object.assign(t, { pending_approval: false, state: "RUNNING", started_at: new Date().toISOString(), updated_at: new Date().toISOString() }); logs[id] = ["Fra:1 rendered"]; return t; },
        deny: async (id) => { const t = tasks.find((x) => x.id === id)!; Object.assign(t, { pending_approval: false, state: "REJECTED", detail: "POLICY_DENIED: denied by the executor's owner" }); return t; },
        cancel: async (id) => { const t = tasks.find((x) => x.id === id)!; t.state = "CANCELLING"; setTimeout(() => { t.state = "CANCELLED"; t.detail = "cancelled by the executor"; refresh(); }, 1200); return t; },
        fetch: async (id, path) => { await sleep(1500); const a = details[id]?.artifacts?.find((x) => x.path === path); if (a) a.location = "local"; return { task_id: id, path, local_path: path, sha256: "", size: 0 }; },
        setPolicy: async (fp, p) => { const pr = peers.find((x) => x.fp === fp); if (pr) pr.policy = p; },
        unpair: async (fp) => { const i = peers.findIndex((x) => x.fp === fp); if (i >= 0) peers.splice(i, 1); },
        probe: async () => ({ os: "windows-11", arch: "amd64", python: ["3.12"], packages: { torch: "2.5.1+cu121", numpy: "2.1.3" }, gpus: [{ name: "NVIDIA GeForce RTX 3050 6GB Laptop GPU", vram_gb: 6, free_vram_gb: 5.1 }], free_disk_gb: 212, ram_gb: 31.8 }),
        doctor: async () => { await sleep(600); return doctor; },
        configList: async () => ({ ...config }),
        configSet: async (k, v) => { config = { ...config, [k]: v }; },
        daemon: async () => { await sleep(800); return "ok"; },
        serviceStatus: async () => (q.has("noservice") ? "not installed" : "installed, running"),
        startPairing: async () => { setTimeout(() => handlePair({ kind: "code", data: { code: "347X0F", peer: { name: MAC } } }), 2600); },
        confirmPairing: async (ok: boolean) => { if (ok) setTimeout(() => handlePair({ kind: "done", data: { ...mac, policy: "ask" } }), 900); },
        cancelPairing: async () => {},
        discover: async () => { await sleep(900); return [{ name: MAC, fp: mac.display, addr: "10.157.240.88:7420", mode: "available", paired: false }, { name: "kith-pi", fp: "KITH-1A2B-3C4D-5E6F-7A8B", addr: "10.157.240.42:7420", mode: "available", paired: true }]; },
        chooseTaskFile: async () => "C:\\Users\\hari\\code\\mnist\\kith.toml",
        resolveTaskFile: async (p) => p,
        check: async (file) => { await sleep(500); return { file, project_dir: "C:\\Users\\hari\\code\\mnist", name: "mnist-train", project: "mnist", command: ["python", "-u", "train.py", "--epochs", "3"], inputs: [{ path: "train.py", size: 3219 }, { path: "data/mnist.npz", size: 11_595_252 }], input_bytes: 11_598_471, outputs: ["out/model.pt"], timeout_minutes: 30, on_interrupt: "resume-if-checkpoint", peer: mac.fp, peer_name: MAC, reachable: true, requirements: [{ req: "os = darwin", have: "darwin-27.0", status: "pass" }, { req: "python >=3.10", have: "3.12.4", status: "pass" }, { req: "torch >=2.0", have: "2.5.1", status: "pass" }, { req: "disk >= 1.0 GB", have: "184.0 GB free", status: "pass" }], ok: true }; },
        submitFile: async () => { const t = T("tsk_new" + Date.now(), "SUBMITTED", "mnist-train", "mnist", "requester", 0, { project_dir: "C:\\Users\\hari\\code\\mnist" }); tasks = [t, ...tasks]; return { task_id: t.id, state: "SUBMITTED" }; },
        reveal: async () => {},
        openPath: async () => {},
        autostart: async () => autostart,
        setAutostart: async (on: boolean) => { autostart = on; },
        cliStatus: async () => ({ on_path: false, path: "C:\\Users\\hari\\AppData\\Local\\Programs\\Kith\\kith.exe" }),
        addToPath: async () => "Added to your user PATH. Open a new terminal to use kith.",
        addFirewallRules: async () => { await sleep(1200); return "Firewall rules added for Private networks"; },
        showMain: async (r: string) => { location.hash = r; },
        hideFlyout: async () => {}, fitFlyout: async () => {}, quit: async () => {},
    };
    Object.assign(api as any, m);
    setInterval(refresh, 1500);
}
