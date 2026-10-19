import { Events } from "@wailsio/runtime";
import {
    api, errText,
    type AppInfo, type Check, type CheckResult, type CLIInfo, type Found, type Manifest, type Peer, type Snapshot, type Task,
} from "./api";

export type Route = { name: string; param: string };

export type PairStep = "choose" | "waiting" | "compare" | "confirming" | "paired" | "error";
export type Dialog =
    | { kind: "newtask"; file?: string; check?: CheckResult; err?: string; errFix?: string; checking?: boolean; revealed: number; sending?: boolean; peer?: string }
    | { kind: "unpair"; peer: Peer }
    | { kind: "service" }
    | { kind: "pair"; step: PairStep; found: Found[]; pick?: string; ip: string; target: string; name: string; code?: string; peer?: Peer; err?: string; errCode?: string; peerConfirmed?: boolean; startedAt: number };

const lsGet = (k: string, d: string) => { try { return localStorage.getItem(k) ?? d; } catch { return d; } };
const lsSet = (k: string, v: string) => { try { localStorage.setItem(k, v); } catch { /* private mode */ } };

export const S = {
    info: { platform: "", chrome: "windows", lite: false, mica: false, home: "", user: "", version: "", log_path: "" } as AppInfo,
    snap: { running: false, pending: 0, active: 0, lite: false, platform: "", gui_version: "" } as Snapshot,
    loaded: false,
    details: {} as Record<string, Task>,
    tails: {} as Record<string, string>,
    doctor: null as Check[] | null,
    doctorShown: 0,
    doctorRunning: false,
    doctorAt: 0,
    config: {} as Record<string, string>,
    cli: null as CLIInfo | null,
    service: "",
    autostart: false,
    route: { name: "overview", param: "" } as Route,
    filter: "all" as "all" | "r" | "s",
    q: "",
    ctx: null as string | null, // task id with an open context menu
    dlg: null as Dialog | null,
    toast: null as { text: string; error: boolean; id: number } | null,
    decided: {} as Record<string, string>,
    leaving: {} as Record<string, "approve" | "deny">,
    ibClosed: {} as Record<string, boolean>,
    busy: {} as Record<string, boolean>,
    theme: lsGet("kith.theme", "system") as "system" | "light" | "dark",
    themeOpen: false,
    sgTab: "general",
    confirmCancel: null as string | null,
    svcWaiting: false,
    svcOk: false,
    open: {} as Record<string, boolean>, // diagnostics expanders
    fetching: {} as Record<string, boolean>,
    configDirty: false,
    local: null as Manifest | null,
    probedAt: 0,
};

// ---- render scheduling ----

let renderFn = () => {};
let scheduled = false;
export function onRender(f: () => void) { renderFn = f; }
export function update(p?: Partial<typeof S>) {
    if (p) Object.assign(S, p);
    if (scheduled) return;
    scheduled = true;
    requestAnimationFrame(() => { scheduled = false; renderFn(); });
}

// ---- derived ----

export const ENDED = ["COMPLETED", "FAILED", "CANCELLED", "REJECTED"];
export const LIVE = ["TRANSFERRING", "RUNNING", "CANCELLING"];
export const isEnded = (s: string) => ENDED.includes(s);
export const tasks = (): Task[] => S.snap.tasks ?? [];
export const peers = (): Peer[] => S.snap.peers ?? [];
export const needsApproval = (t: Task) => t.role === "executor" && t.state === "SUBMITTED" && t.pending_approval;
export const pending = () => tasks().filter(needsApproval);
export const active = () => tasks().filter((t) => !isEnded(t.state) && !needsApproval(t));
export const finished = () => tasks().filter((t) => isEnded(t.state));
export const isSent = (t: Task) => t.role === "requester";
export const peerOf = (fp: string) => peers().find((p) => p.fp === fp);
export const peerLabel = (t: Task) => t.peer_name || peerOf(t.peer_fp)?.name || t.peer_fp.slice(0, 8);
export const reachable = () => peers().filter((p) => p.reachable);
export const thisName = () => S.snap.status?.name || S.config.name || (S.info.platform === "windows" ? "This PC" : "This machine");
export const hereWord = () => (S.info.platform === "windows" ? "this PC" : S.info.lite ? "this Pi" : "this machine");
export const full = (t: Task) => ({ ...(S.details[t.id] ?? {}), ...t, argv: S.details[t.id]?.argv, inputs: S.details[t.id]?.inputs, requirements: S.details[t.id]?.requirements, outputs: S.details[t.id]?.outputs, timeline: S.details[t.id]?.timeline ?? t.timeline, artifacts: S.details[t.id]?.artifacts ?? t.artifacts, pid: S.details[t.id]?.pid } as Task);

// ---- routing ----

function parse(): Route {
    const h = location.hash.replace(/^#\/?/, "");
    const [name, ...rest] = h.split("/");
    return { name: name || "overview", param: decodeURIComponent(rest.join("/")) };
}

export function go(hash: string) {
    if (location.hash === hash) return;
    location.hash = hash;
}

window.addEventListener("hashchange", () => {
    const r = parse();
    S.route = r;
    S.ctx = null;
    S.confirmCancel = null;
    S.themeOpen = false;
    if (r.name === "settings" && r.param) S.sgTab = r.param;
    update();
    onRoute();
});

// ---- feedback ----

let toastTimer = 0;
export function flash(text: string, error = false) {
    clearTimeout(toastTimer);
    update({ toast: { text, error, id: Date.now() } });
    toastTimer = window.setTimeout(() => update({ toast: null }), error ? 5000 : 1600);
}
export function fail(e: unknown) {
    const t = errText(e).replace(/\nFix: /, ". ");
    flash(t, true);
}

export async function copy(text: string, label: string) {
    try { await navigator.clipboard.writeText(text); flash(`Copied ${label}`); } catch (e) { fail(e); }
}

export function setTheme(t: "system" | "light" | "dark") {
    lsSet("kith.theme", t);
    update({ theme: t, themeOpen: false });
    applyTheme();
}

export function applyTheme() {
    const r = document.documentElement;
    if (S.theme === "system") delete r.dataset.theme;
    else r.dataset.theme = S.theme;
}

// ---- data ----

export async function refresh() {
    try { applySnap(await api.snapshot()); } catch (e) { S.snap = { ...S.snap, running: false, error: errText(e) }; update(); }
}

function applySnap(s: Snapshot) {
    S.snap = s;
    S.loaded = true;
    if (S.svcWaiting && /running/i.test(s.service ?? "")) { S.svcWaiting = false; S.svcOk = true; }
    if (s.service) S.service = s.service;
    update();
}

export async function loadDetail(id: string) {
    try { S.details[id] = await api.task(id); update(); } catch { /* gone */ }
}

const tailOffsets: Record<string, number> = {};
export async function refreshTails(limit = 3) {
    for (const t of active().slice(0, limit)) {
        const from = tailOffsets[t.id] ?? Math.max(0, (t.log_size || 0) - 4096);
        try {
            const r = await api.logs(t.id, from);
            tailOffsets[t.id] = r.offset;
            const line = (r.data ?? "").split(/\r?\n/).map((l) => l.trim()).filter(Boolean).pop();
            if (line && S.tails[t.id] !== line) { S.tails[t.id] = line; update(); }
        } catch { /* daemon busy */ }
    }
}

export async function probe() {
    S.busy.probe = true; update();
    try { S.local = await api.probe(); S.probedAt = Date.now(); } catch { /* daemon down */ }
    delete S.busy.probe; update();
}

export async function loadSettings() {
    const [cfg, cli, svc, auto] = await Promise.allSettled([api.configList(), api.cliStatus(), api.serviceStatus(), api.autostart()]);
    if (cfg.status === "fulfilled") S.config = cfg.value ?? {};
    if (cli.status === "fulfilled") S.cli = cli.value;
    if (svc.status === "fulfilled") S.service = svc.value;
    if (auto.status === "fulfilled") S.autostart = auto.value;
    update();
}

export async function runDoctor(stagger = true) {
    if (S.doctorRunning) return;
    update({ doctorRunning: true, doctorShown: 0 });
    let checks: Check[];
    try { checks = await api.doctor(); } catch (e) { checks = [{ name: "doctor", status: "fail", detail: errText(e) }]; }
    S.doctor = checks;
    for (let i = 1; i <= checks.length; i++) {
        if (stagger) await sleep(200);
        update({ doctorShown: i });
    }
    update({ doctorRunning: false, doctorAt: Date.now(), doctorShown: checks.length });
}

export const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// ---- actions ----

async function act(key: string, op: () => Promise<unknown>, done?: string): Promise<boolean> {
    S.busy[key] = true; update();
    try {
        await op();
        if (done) flash(done);
        await refresh();
        return true;
    } catch (e) { fail(e); return false; } finally { delete S.busy[key]; update(); }
}

export async function decide(t: Task, approve: boolean) {
    S.leaving[t.id] = approve ? "approve" : "deny";
    update();
    await sleep(approve ? 330 : 170);
    const ok = await act("decide:" + t.id, () => (approve ? api.approve(t.id) : api.deny(t.id)));
    delete S.leaving[t.id];
    if (ok) S.decided[t.id] = approve ? "Approved" : "Denied";
    update();
}

export const cancelTask = (t: Task) => act("cancel:" + t.id, () => api.cancel(t.id));
export const setPolicy = (p: Peer, policy: string) => act("policy:" + p.fp, () => api.setPolicy(p.fp, policy));
export const unpair = (p: Peer) => act("unpair:" + p.fp, () => api.unpair(p.fp), `Unpaired ${p.name}`);

export async function fetchArtifact(t: Task, path: string) {
    S.fetching[t.id + path] = true; update();
    try {
        const r = await api.fetch(t.id, path);
        flash(`Fetched ${r.path.split(/[\\/]/).pop()} (SHA-256 verified)`);
        await loadDetail(t.id);
    } catch (e) { fail(e); } finally { delete S.fetching[t.id + path]; update(); }
}

export async function daemon(action: string) {
    const ok = await act("daemon", () => api.daemon(action));
    if (ok && (action === "restart" || action === "start")) S.configDirty = false;
    loadSettings();
}

export async function setConfig(k: string, v: string) {
    if (S.config[k] === v) return;
    try {
        await api.configSet(k, v);
        S.config[k] = v;
        if (S.snap.running) S.configDirty = true;
        update();
    } catch (e) { fail(e); loadSettings(); }
}

export async function installService() {
    update({ dlg: null, svcWaiting: true });
    try {
        const msg = await api.daemon("install");
        if (S.info.platform !== "windows") { flash(msg || "Service installed"); S.svcWaiting = false; S.svcOk = true; }
        // On Windows an Administrator window takes over; the poller notices the service.
        const until = Date.now() + 5 * 60_000;
        while (S.svcWaiting && Date.now() < until) {
            await sleep(2000);
            S.service = await api.serviceStatus().catch(() => S.service);
            if (/running/i.test(S.service)) { S.svcWaiting = false; S.svcOk = true; }
            update();
        }
        S.svcWaiting = false;
    } catch (e) { S.svcWaiting = false; fail(e); }
    loadSettings();
}

export async function addFirewall() {
    S.busy.firewall = true; update();
    try { flash(await api.addFirewallRules()); runDoctor(false); } catch (e) { fail(e); } finally { delete S.busy.firewall; update(); }
}

// ---- new task ----

export function openNewTask(file?: string) {
    const peer = reachable()[0]?.fp ?? peers()[0]?.fp;
    S.dlg = { kind: "newtask", revealed: 0, peer };
    update();
    if (file) checkTask(file);
}

export async function chooseTaskFile() {
    try { const f = await api.chooseTaskFile(); if (f) checkTask(f); } catch (e) { fail(e); }
}

export async function checkTask(file: string) {
    const d = S.dlg;
    if (!d || d.kind !== "newtask") return;
    Object.assign(d, { file, checking: true, err: undefined, errFix: undefined, revealed: 0, check: undefined });
    update();
    try {
        const c = await api.check(file, d.peer ?? "");
        if (S.dlg !== d) return;
        d.check = c; d.checking = false; update();
        for (let i = 1; i <= c.requirements.length; i++) {
            await sleep(260);
            if (S.dlg !== d || d.check !== c) return;
            d.revealed = i; update();
        }
    } catch (e) {
        const t = errText(e);
        const [msg, fix] = t.split("\nFix: ");
        Object.assign(d, { checking: false, err: msg, errFix: fix });
        update();
    }
}

export async function sendTask() {
    const d = S.dlg;
    if (!d || d.kind !== "newtask" || !d.file || !d.peer) return;
    d.sending = true; update();
    try {
        const r = await api.submitFile(d.file, d.peer);
        if (r.state === "REJECTED") {
            Object.assign(d, { sending: false, check: undefined, err: `${peerOf(d.peer)?.name ?? "The peer"} turned it down: ${r.detail ?? ""}`, errFix: r.remediation });
            update();
            return;
        }
        update({ dlg: null });
        flash(`Sent to ${peerOf(d.peer)?.name ?? "peer"}`);
        await refresh();
        go("#/tasks/" + encodeURIComponent(r.task_id));
    } catch (e) { d.sending = false; fail(e); update(); }
}

// ---- pairing ----

export function openPair() {
    S.dlg = { kind: "pair", step: "choose", found: [], ip: "", target: "", name: "", startedAt: Date.now() };
    update();
    scan();
}

async function scan() {
    for (let i = 0; i < 30; i++) {
        const d = S.dlg;
        if (!d || d.kind !== "pair" || d.step !== "choose") return;
        try {
            const f = await api.discover();
            const known = new Set(d.found.map((x) => x.fp));
            d.found = [...d.found, ...f.filter((x) => !known.has(x.fp))];
            if (!d.pick) d.pick = d.found.find((x) => !x.paired)?.fp;
            update();
        } catch { /* mDNS blocked: manual IP still works */ }
        await sleep(2500);
    }
}

export async function startPair(ip: string, name: string) {
    const d = S.dlg;
    if (!d || d.kind !== "pair") return;
    Object.assign(d, { step: "waiting", target: ip, name, err: undefined, errCode: undefined, startedAt: Date.now() });
    update();
    try { await api.startPairing(ip); } catch (e) { pairError(errText(e)); }
}

export function pairError(msg: string, code?: string) {
    const d = S.dlg;
    if (!d || d.kind !== "pair") return;
    Object.assign(d, { step: "error", err: msg, errCode: code || (msg.match(/^([A-Z_]+):/) || [])[1] });
    update();
}

export async function confirmPair(ok: boolean) {
    const d = S.dlg;
    if (!d || d.kind !== "pair") return;
    const code = d.code ?? "";
    if (!ok) { await api.confirmPairing(false, code).catch(() => {}); Object.assign(d, { step: "error", errCode: "MISMATCH", err: "codes did not match" }); update(); return; }
    d.step = "confirming"; update();
    try { await api.confirmPairing(true, code); } catch (e) { pairError(errText(e)); }
}

export function closeDialog() {
    if (S.dlg?.kind === "pair" && S.dlg.step !== "paired") api.cancelPairing().catch(() => {});
    update({ dlg: null });
}

// ---- events from Go ----

export function listen() {
    Events.On("kith:state", (ev: any) => applySnap(ev.data as Snapshot));
    Events.On("kith:pair", (ev: any) => handlePair(ev.data));
    Events.On("kith:drop", async (ev: any) => handleDrop((ev.data as string[]) ?? []));
}

export function handlePair(e: { kind: string; data?: any; error?: string; code?: string }) {
    const d = S.dlg;
    if (!d || d.kind !== "pair") return;
    // A new code (two crossed connections settling on one) restarts the comparison.
    if (e.kind === "code") { d.code = e.data?.code ?? ""; if (e.data?.peer?.name) d.name = e.data.peer.name; d.step = "compare"; d.peerConfirmed = false; }
    if (e.kind === "peer-confirmed") d.peerConfirmed = true;
    if (e.kind === "done") { d.peer = e.data as Peer; d.name = d.peer?.name ?? d.name; d.step = "paired"; refresh(); }
    if (e.kind === "error") { pairError(e.error ?? "pairing failed", e.code); return; }
    update();
}

export async function handleDrop(files: string[]) {
    if (!files.length) return;
    try {
        const f = await api.resolveTaskFile(files[0]);
        if (S.dlg?.kind === "newtask") checkTask(f); else openNewTask(f);
    } catch (e) { fail(e); }
}

// ---- per-route side effects ----

let routeTimer = 0;
export function onRoute() {
    clearInterval(routeTimer);
    const r = S.route;
    const tick = () => {
        if (r.name === "overview" || r.name === "flyout") refreshTails(4);
        if (r.name === "flyout") pending().slice(0, 2).forEach((t) => { if (!S.details[t.id]) loadDetail(t.id); });
        if (r.name === "tasks" && r.param) loadDetail(r.param);
    };
    tick();
    routeTimer = window.setInterval(tick, S.info.lite ? 5000 : 1200);
    if (r.name === "approvals") pending().forEach((t) => loadDetail(t.id));
    if (r.name === "diagnostics" && !S.doctor) runDoctor();
    if (r.name === "settings") loadSettings();
}

export async function boot() {
    S.route = parse();
    if (S.route.name === "settings" && S.route.param) S.sgTab = S.route.param;
    applyTheme();
    try { S.info = await api.info(); } catch { /* dev preview */ }
    listen();
    await refresh();
    loadSettings();
    onRoute();
    if (S.route.name !== "flyout") probe();
    // Windows: check reachability once so Overview can warn about the
    // firewall or a Public network.
    if (S.info.platform === "windows") runDoctor(false);
}
