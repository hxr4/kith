import "./style.css";
import {Events} from "@wailsio/runtime";
import {api, errText, type Check, type Peer, type Snapshot, type Task, type PairEvent} from "./api";
import {$, ago, busy, bytes, clock, confirmBox, esc, stateBadge, stateClass, toast} from "./ui";

// ---------------------------------------------------------------- state --

let snap: Snapshot = {running: false, pending: 0, active: 0, lite: false, platform: "", gui_version: ""};
let cleanup: (() => void) | null = null; // per-view timers
let pair: PairEvent | null = null;
let pairBusy = false;
let tasksFilter = "all";

const tasks = (): Task[] => snap.tasks ?? [];
const peers = (): Peer[] => snap.peers ?? [];
const pendingTasks = () => tasks().filter((t) => t.role === "executor" && t.state === "SUBMITTED" && t.pending_approval);
const isTerminal = (s: string) => ["COMPLETED", "FAILED", "CANCELLED", "REJECTED"].includes(s);

// --------------------------------------------------------------- routing --

type View = (el: HTMLElement, param: string) => void;

function route(): {name: string; param: string} {
    const h = location.hash.replace(/^#\/?/, "");
    const [name, ...rest] = h.split("/");
    return {name: name || "overview", param: decodeURIComponent(rest.join("/"))};
}

const views: Record<string, View> = {overview, tasks: tasksView, approvals, pair: pairView, settings};

function render(full = true) {
    const {name, param} = route();
    document.querySelectorAll<HTMLAnchorElement>(".nav a").forEach((a) => {
        const r = a.dataset.route!;
        a.classList.toggle("active", r === name || (r === "tasks" && name === "tasks"));
    });
    renderChrome();
    const el = $("#view")!;
    if (name === "tasks" && param) {
        if (full) { cleanup?.(); cleanup = null; taskDetail(el, param); }
        else refreshTaskHead(param);
        return;
    }
    // Skip polling re-renders when nothing this view shows has changed
    // (avoids flicker and lost focus/hover state).
    const key = name + "|" + viewKey(name);
    if (!full && key === lastKey) return;
    lastKey = key;
    if (full) { cleanup?.(); cleanup = null; }
    (views[name] ?? overview)(el, param);
}

let lastKey = "";

function viewKey(name: string): string {
    const t = (x: Task) => `${x.id}:${x.state}:${x.pending_approval}:${x.updated_at}`;
    switch (name) {
        case "approvals": return pendingTasks().map((x) => x.id).join(",") + snap.running;
        case "pair": return JSON.stringify(pair) + snap.running;
        default: return [snap.running, snap.service, tasks().map(t).join(","),
            peers().map((p) => `${p.fp}:${p.reachable}:${p.policy}:${p.caps}`).join(","), tasksFilter].join("|");
    }
}

window.addEventListener("hashchange", () => { lastKey = ""; render(true); });

function renderChrome() {
    document.documentElement.classList.toggle("lite", snap.lite);
    const sub = $("#brand-sub")!;
    sub.textContent = snap.running ? (snap.status?.name ?? "running") : "daemon offline";
    sub.className = "brand-sub " + (snap.running ? "on" : "off");
    const p = $("#nav-pending")!;
    p.textContent = snap.pending ? String(snap.pending) : "";
    p.hidden = !snap.pending;
    const a = $("#nav-active")!;
    a.textContent = snap.active ? String(snap.active) : "";
    a.hidden = !snap.active;
    const online = peers().filter((x) => x.reachable).length;
    $("#sidebar-foot")!.innerHTML = snap.running
        ? `<span class="dot ${online ? "on" : "off"}"></span>${online}/${peers().length} peers online`
        : `<span class="dot off"></span>offline`;
}

// ------------------------------------------------------------- overview --

function offlineBanner(): string {
    if (snap.running) return "";
    const svc = snap.service ?? "";
    const installed = svc.startsWith("installed");
    return `<div class="banner bad">
        <div><strong>The Kith daemon is not running.</strong>
        <div class="muted">${esc(snap.error ?? "")}</div></div>
        <div class="row">
          <button class="btn primary" data-daemon="start">Start daemon</button>
          ${installed ? "" : `<button class="btn" data-daemon="install">Install as a service</button>`}
        </div></div>`;
}

function wireDaemonButtons(el: HTMLElement) {
    el.querySelectorAll<HTMLButtonElement>("[data-daemon]").forEach((b) => b.onclick = () => busy(b, async () => {
        const action = b.dataset.daemon!;
        if (action === "stop" && !(await confirmBox("Stop the daemon?",
            "Running tasks on this machine will be interrupted and handled by their interrupt policy.", "Stop", true))) return;
        try { const out = await api.daemon(action); toast(out || `daemon ${action}: done`, "ok"); }
        catch (e) { toast(errText(e), "bad"); }
        snap = await api.snapshot(); render(false);
    }));
}

function gpuText(p: Peer): string {
    const g = p.manifest?.gpus ?? [];
    return g.length ? g.map((x) => `${x.name} · ${x.vram_gb} GB`).join(", ") : "no NVIDIA GPU";
}

function peerCard(p: Peer): string {
    const m = p.manifest;
    const torch = m?.packages?.["torch"];
    return `<div class="card peer">
      <div class="row between"><h3>${esc(p.name)}</h3>
        <span class="pill ${p.reachable ? "ok" : "idle"}"><span class="dot ${p.reachable ? "on" : "off"}"></span>${p.reachable ? "online" : "offline"}</span></div>
      <div class="mono muted">${esc(p.display)}</div>
      <dl class="facts">
        <dt>System</dt><dd>${esc(m ? `${m.os} · ${m.arch}` : "-")}</dd>
        <dt>GPU</dt><dd>${esc(m ? gpuText(p) : "-")}</dd>
        <dt>Python</dt><dd>${esc(m?.python?.join(", ") || "-")}${torch ? ` · torch ${esc(torch)}` : ""}</dd>
        <dt>Disk free</dt><dd>${m ? esc(m.free_disk_gb) + " GB" : "-"}</dd>
        <dt>Address</dt><dd class="mono">${esc(p.last_addr || "-")}</dd>
        <dt>Their tasks here</dt><dd>${p.policy === "auto" ? "run automatically" : "need your approval"}</dd>
      </dl></div>`;
}

function taskRow(t: Task): string {
    const dir = t.role === "executor" ? `<span class="dir in" title="runs on this machine">in</span>` : `<span class="dir out" title="sent to a peer">out</span>`;
    const pend = t.role === "executor" && t.state === "SUBMITTED" && t.pending_approval;
    return `<a class="trow" href="#/tasks/${encodeURIComponent(t.id)}">
      ${dir}<span class="tname">${esc(t.name)}<span class="muted mono"> ${esc(t.id.slice(0, 14))}…</span></span>
      <span class="muted">${esc(t.peer_name ?? "")}</span>${stateBadge(t.state, pend)}
      <span class="muted small">${esc(ago(t.updated_at))}</span></a>`;
}

function overview(el: HTMLElement) {
    const st = snap.status;
    const active = tasks().filter((t) => !isTerminal(t.state));
    const recent = tasks().filter((t) => isTerminal(t.state)).slice(0, 5);
    const pend = pendingTasks();
    el.innerHTML = `
      <h1>Overview</h1>
      ${offlineBanner()}
      ${pend.length ? `<a class="banner warn" href="#/approvals"><strong>${pend.length} task${pend.length > 1 ? "s" : ""} waiting for your approval</strong><span>Review →</span></a>` : ""}
      ${st ? `<div class="card this">
        <div class="row between"><div><div class="label">This machine</div><h2>${esc(st.name)}</h2></div>
        <div class="mono muted">${esc(st.display)}</div></div>
        <div class="muted small">port ${esc(st.port)} · service: ${esc(snap.service ?? "")} · kith ${esc(st.version)}</div></div>` : ""}
      <h2 class="section">Peers</h2>
      <div class="grid">${peers().map(peerCard).join("") || `<div class="card empty">No paired machines yet. <a href="#/pair">Pair one →</a></div>`}</div>
      <h2 class="section">Active tasks</h2>
      <div class="list">${active.map(taskRow).join("") || `<div class="empty">Nothing running.</div>`}</div>
      ${recent.length ? `<h2 class="section">Recent</h2><div class="list">${recent.map(taskRow).join("")}</div>` : ""}`;
    wireDaemonButtons(el);
}

// ---------------------------------------------------------------- tasks --

function tasksView(el: HTMLElement) {
    const filters: [string, string, (t: Task) => boolean][] = [
        ["all", "All", () => true],
        ["active", "Active", (t) => !isTerminal(t.state)],
        ["out", "Sent", (t) => t.role === "requester"],
        ["in", "Received", (t) => t.role === "executor"],
        ["problems", "Failed", (t) => ["FAILED", "REJECTED", "CANCELLED"].includes(t.state)],
    ];
    const f = filters.find((x) => x[0] === tasksFilter) ?? filters[0];
    const list = tasks().filter(f[2]);
    el.innerHTML = `
      <div class="row between"><h1>Tasks</h1>
        <button class="btn" id="submit-file">Submit a task file…</button></div>
      ${offlineBanner()}
      <div class="tabs">${filters.map(([k, l]) => `<button class="tab ${k === f[0] ? "on" : ""}" data-f="${k}">${l}</button>`).join("")}</div>
      <div class="list">${list.map(taskRow).join("") || `<div class="empty">No tasks here yet. Agents submit them via MCP; you can use <span class="mono">kith run</span> or “Submit a task file”.</div>`}</div>`;
    el.querySelectorAll<HTMLButtonElement>(".tab").forEach((b) => b.onclick = () => { tasksFilter = b.dataset.f!; lastKey = ""; tasksView(el); });
    $("#submit-file", el)!.onclick = () => submitFlow();
    wireDaemonButtons(el);
}

async function submitFlow() {
    let path = "";
    try { path = await api.chooseTaskFile(); } catch (e) { toast(errText(e), "bad"); return; }
    if (!path) return;
    const ps = peers();
    if (!ps.length) { toast("Pair a machine first.", "bad"); return; }
    const m = $("#modal")!;
    m.innerHTML = `<div class="modal"><h3>Submit task</h3><p class="mono small">${esc(path)}</p>
      <label>Run on <select id="sub-peer">${ps.map((p) => `<option value="${esc(p.name)}">${esc(p.name)}${p.reachable ? "" : " (offline)"}</option>`).join("")}</select></label>
      <div class="row end"><button class="btn" data-a="no">Cancel</button><button class="btn primary" data-a="yes">Submit</button></div></div>`;
    m.hidden = false;
    m.onclick = async (e) => {
        const a = (e.target as HTMLElement).dataset.a;
        if (a === "no" || e.target === m) { m.hidden = true; return; }
        if (a !== "yes") return;
        const peer = ($("#sub-peer", m) as HTMLSelectElement).value;
        m.hidden = true;
        try {
            const r = await api.submitFile(path, peer);
            if (r.state === "REJECTED") toast(`Rejected: ${r.detail ?? ""}${r.remediation ? "\nFix: " + r.remediation : ""}`, "bad");
            else { toast(`Submitted ${r.task_id}`, "ok"); location.hash = `#/tasks/${r.task_id}`; }
        } catch (err) { toast(errText(err), "bad"); }
    };
}

// ----------------------------------------------------------- task detail --

function detailHead(t: Task): string {
    const pend = t.role === "executor" && t.state === "SUBMITTED" && t.pending_approval;
    const where = t.role === "executor" ? `from ${esc(t.peer_name)} · runs here` : `on ${esc(t.peer_name)}`;
    return `
      <div class="row between"><div><a href="#/tasks" class="muted small">← Tasks</a>
        <h1>${esc(t.name)} ${stateBadge(t.state, pend)}</h1>
        <div class="muted mono small">${esc(t.id)} · ${where} · attempt ${esc(t.attempt)}${t.pid ? " · pid " + esc(t.pid) : ""}</div></div>
        <div class="row">
          ${pend ? `<button class="btn primary" data-act="approve">Approve</button><button class="btn danger" data-act="deny">Deny</button>` : ""}
          ${!isTerminal(t.state) && !pend ? `<button class="btn danger" data-act="cancel">Cancel task</button>` : ""}
        </div></div>
      ${t.detail ? `<div class="banner ${stateClass(t.state) === "bad" ? "bad" : "info"}"><div>${esc(t.detail)}${t.remediation ? `<div class="fix">Fix: ${esc(t.remediation)}</div>` : ""}</div></div>` : ""}
      <div class="cols">
        <div class="card"><div class="label">Command</div><pre class="argv">${esc((t.argv ?? []).map((a) => /\s/.test(a) ? JSON.stringify(a) : a).join(" "))}</pre>
          ${t.project_dir ? `<div class="label">Project folder</div><div class="mono small">${esc(t.project_dir)}</div>` : ""}
          <div class="label">Timeline</div>
          <ol class="timeline">${(t.timeline ?? []).map((x) => `<li><span class="tl-dot ${stateClass(x.state)}"></span>
            <span class="mono small muted">${esc(clock(x.ts))}</span> <strong>${esc(x.state)}</strong> <span class="muted">${esc(x.detail ?? "")}</span></li>`).join("")}</ol>
        </div>
        <div class="card"><div class="label">Artifacts</div>
          ${(t.artifacts ?? []).length ? `<table class="arts"><tbody>${(t.artifacts ?? []).map((a) => `<tr>
            <td class="mono">${esc(a.path)}</td><td class="muted">${esc(bytes(a.size))}</td>
            <td><span class="pill ${a.location === "local" ? "ok" : "idle"}">${esc(a.status)}</span></td>
            <td class="end">${t.role === "requester" ? (a.location === "local"
                ? `<button class="btn small" data-reveal="${esc(a.path)}">Show</button>`
                : `<button class="btn small" data-fetch="${esc(a.path)}" ${a.producer_offline ? "disabled" : ""}>Fetch</button>`) : ""}</td>
            </tr><tr><td colspan="4" class="mono tiny muted">sha256 ${esc(a.sha256)}</td></tr>`).join("")}</tbody></table>`
            : `<div class="muted">${isTerminal(t.state) ? "No artifacts." : "Artifacts appear when the task finishes."}</div>`}
        </div>
      </div>`;
}

let headTask: Task | null = null;

async function refreshTaskHead(id: string) {
    try { headTask = await api.task(id); } catch (e) { $("#detail-head")!.innerHTML = `<div class="banner bad">${esc(errText(e))}</div>`; return; }
    const h = $("#detail-head");
    if (!h) return;
    h.innerHTML = detailHead(headTask);
    wireDetail(h, headTask);
}

function wireDetail(h: HTMLElement, t: Task) {
    h.querySelectorAll<HTMLButtonElement>("[data-act]").forEach((b) => b.onclick = () => busy(b, async () => {
        const act = b.dataset.act!;
        if (act === "cancel" && !(await confirmBox("Cancel this task?", "The process tree is stopped; this cannot be undone.", "Cancel task", true))) return;
        if (act === "deny" && !(await confirmBox("Deny this task?", "It will be rejected and not run.", "Deny", true))) return;
        try {
            if (act === "approve") await api.approve(t.id);
            if (act === "deny") await api.deny(t.id);
            if (act === "cancel") await api.cancel(t.id);
            toast(`${act} sent`, "ok");
        } catch (e) { toast(errText(e), "bad"); }
        refreshTaskHead(t.id);
    }));
    h.querySelectorAll<HTMLButtonElement>("[data-fetch]").forEach((b) => b.onclick = () => busy(b, async () => {
        try { const r = await api.fetch(t.id, b.dataset.fetch!); toast(`Fetched ${r.path} (sha256 verified)`, "ok"); }
        catch (e) { toast(errText(e), "bad"); }
        refreshTaskHead(t.id);
    }));
    h.querySelectorAll<HTMLButtonElement>("[data-reveal]").forEach((b) => b.onclick = async () => {
        const p = (t.project_dir ?? "") + "/" + b.dataset.reveal!;
        try { await api.reveal(p); } catch (e) { toast(errText(e), "bad"); }
    });
}

function taskDetail(el: HTMLElement, id: string) {
    el.innerHTML = `<div id="detail-head"><div class="muted">Loading…</div></div>
      <div class="card logs-card"><div class="row between"><div class="label">Output</div>
        <label class="small muted"><input type="checkbox" id="follow" checked/> follow</label></div>
        <pre class="logs" id="logs"></pre></div>`;
    refreshTaskHead(id);
    const logs = $("#logs")!;
    let offset = 0, done = false, stopped = false;
    const every = snap.lite ? 3000 : 1000;
    const tick = async () => {
        if (stopped) return;
        try {
            const r = await api.logs(id, offset);
            if (r.data) {
                const follow = ($("#follow") as HTMLInputElement | null)?.checked ?? true;
                logs.append(document.createTextNode(r.data)); // text node: never parsed as HTML
                offset = r.offset;
                if (follow) logs.scrollTop = logs.scrollHeight;
            }
            if (r.done && !done) { done = true; refreshTaskHead(id); }
        } catch { /* daemon restarting: keep polling */ }
        if (!stopped && !done) timer = window.setTimeout(tick, every);
    };
    let timer = window.setTimeout(tick, 0);
    cleanup = () => { stopped = true; clearTimeout(timer); };
}

// ------------------------------------------------------------ approvals --

function approvals(el: HTMLElement) {
    const list = pendingTasks();
    el.innerHTML = `<h1>Approvals</h1>
      <p class="muted">Peers with the <strong>ask</strong> policy need your OK before their tasks run on this machine.
      Approved tasks run as your user, without a sandbox. Unanswered requests expire after 15 minutes.</p>
      <div id="appr">${list.length ? "" : `<div class="card empty">Nothing is waiting for approval.</div>`}</div>`;
    const host = $("#appr", el)!;
    list.forEach(async (t) => {
        const card = document.createElement("div");
        card.className = "card approval";
        card.innerHTML = `<div class="muted">Loading ${esc(t.id)}…</div>`;
        host.appendChild(card);
        let full: Task = t;
        try { full = await api.task(t.id); } catch { /* show what we have */ }
        card.innerHTML = `<div class="row between"><div><h3>${esc(full.name)}</h3>
            <div class="muted small">from <strong>${esc(full.peer_name)}</strong> · ${esc(ago(full.created_at))} · <span class="mono">${esc(full.id)}</span></div></div>
            <div class="row"><button class="btn danger" data-a="deny">Deny</button><button class="btn primary" data-a="approve">Approve</button></div></div>
            <div class="label">It will run</div><pre class="argv">${esc((full.argv ?? []).join(" "))}</pre>`;
        card.querySelectorAll<HTMLButtonElement>("[data-a]").forEach((b) => b.onclick = () => busy(b, async () => {
            try {
                if (b.dataset.a === "approve") { await api.approve(t.id); toast("Approved: it will run now", "ok"); }
                else { await api.deny(t.id); toast("Denied", "info"); }
                card.remove();
            } catch (e) { toast(errText(e), "bad"); }
        }));
    });
}

// ----------------------------------------------------------------- pair --

function pairView(el: HTMLElement) {
    const p = pair;
    let body = "";
    if (!p || p.kind === "done" || p.kind === "error") {
        body = `
          ${p?.kind === "done" ? `<div class="banner ok"><div><strong>Paired with ${esc(p.data?.name)}</strong> (${esc(p.data?.display)}). It is now listed under Peers.</div></div>` : ""}
          ${p?.kind === "error" ? `<div class="banner bad"><div>${esc(p.error)}</div></div>` : ""}
          <div class="card">
            <ol class="steps"><li>Open Kith (or run <span class="mono">kith pair</span>) on the <strong>other</strong> machine and start pairing there too.</li>
            <li>Leave the address empty to find it automatically, or enter its IP if discovery fails.</li>
            <li>Both screens show a code. Confirm only if they are identical.</li></ol>
            <div class="row"><input id="pair-ip" placeholder="other machine's IP (optional)" spellcheck="false"/>
            <button class="btn primary" id="pair-go">Start pairing</button></div>
            <p class="muted small">Pairing lets that machine run commands on this one as your user (subject to its policy: ask by default).</p>
          </div>`;
    } else if (p.kind === "waiting") {
        body = `<div class="card center"><div class="spinner"></div><p>${esc(p.data?.message ?? "Waiting for the other machine…")}</p>
          <button class="btn" id="pair-cancel">Cancel</button></div>`;
    } else if (p.kind === "code") {
        const code = String(p.data?.code ?? "");
        body = `<div class="card center">
          <div class="muted">Pairing with <strong>${esc(p.data?.peer?.name)}</strong> <span class="mono">${esc(p.data?.peer?.display)}</span> (${esc(p.data?.addr)})</div>
          <div class="code">${esc(code.slice(0, 3))}<span></span>${esc(code.slice(3))}</div>
          <p>Does the other machine show <strong>exactly</strong> this code?</p>
          <div class="row center"><button class="btn danger" id="pair-no">No, different</button>
          <button class="btn primary" id="pair-yes">Yes, they match</button></div></div>`;
    }
    el.innerHTML = `<h1>Pair a machine</h1>${offlineBanner()}${body}`;
    wireDaemonButtons(el);
    const go = $<HTMLButtonElement>("#pair-go", el);
    if (go) go.onclick = () => busy(go, async () => {
        if (pairBusy) return;
        pairBusy = true;
        pair = {kind: "waiting", data: {message: "Starting…"}};
        try { await api.startPairing(($("#pair-ip", el) as HTMLInputElement).value); }
        catch (e) { pair = {kind: "error", error: errText(e)}; pairBusy = false; }
        render(false);
    });
    const cancel = $<HTMLButtonElement>("#pair-cancel", el);
    if (cancel) cancel.onclick = () => api.cancelPairing();
    const yes = $<HTMLButtonElement>("#pair-yes", el), no = $<HTMLButtonElement>("#pair-no", el);
    if (yes) yes.onclick = () => busy(yes, () => api.confirmPairing(true).then(() => { pair = {kind: "waiting", data: {message: "Verifying…"}}; render(false); }));
    if (no) no.onclick = () => busy(no, () => api.confirmPairing(false));
}

// ------------------------------------------------------------- settings --

const CONFIG_HELP: Record<string, string> = {
    name: "Name shown to your other machines",
    default_policy: "Tasks from newly paired machines: ask (approve each) or auto",
    log_level: "Daemon log detail",
    port: "TCP port (both machines must allow it through the firewall)",
    mdns: "Find machines automatically on the LAN",
    bind: "Listen address",
};

function settings(el: HTMLElement) {
    el.innerHTML = `<h1>Settings</h1>
      <div class="card"><div class="row between"><div><div class="label">Daemon</div>
        <div>${snap.running ? `<span class="dot on"></span> running · pid ${esc(snap.status?.pid)}` : `<span class="dot off"></span> not running`}</div>
        <div class="muted small">service: ${esc(snap.service ?? "")}</div></div>
        <div class="row">
          ${snap.running ? `<button class="btn" data-daemon="restart">Restart</button><button class="btn danger" data-daemon="stop">Stop</button>` : `<button class="btn primary" data-daemon="start">Start</button>`}
          ${(snap.service ?? "").startsWith("installed") ? "" : `<button class="btn" data-daemon="install">Install as a service</button>`}
        </div></div></div>
      <div class="card"><div class="row between"><div><div class="label">App</div>
        <div>Open Kith in the ${snap.platform.startsWith("darwin") ? "menu bar" : "tray"} at login</div>
        <div class="muted small">Approval requests then reach you as notifications even when the window is closed.</div></div>
        <label class="switch"><input type="checkbox" id="autostart"/><span></span></label></div></div>
      <div class="card"><div class="label">Paired machines</div><div id="peer-settings"></div></div>
      <div class="card"><div class="label">Configuration</div><div id="cfg" class="muted">Loading…</div></div>
      <div class="card"><div class="row between"><div class="label">Diagnostics</div><button class="btn" id="doctor">Run checks</button></div><div id="doc"></div></div>
      <div class="card"><div class="label">About</div><div class="muted small">Kith ${esc(snap.gui_version)} · ${esc(snap.platform)}${snap.lite ? " · lite mode" : ""}</div></div>`;
    wireDaemonButtons(el);
    const auto = $<HTMLInputElement>("#autostart", el)!;
    api.autostart().then((on) => { auto.checked = on; });
    auto.onchange = async () => {
        try { await api.setAutostart(auto.checked); toast(auto.checked ? "Kith will open at login" : "Kith will not open at login", "ok"); }
        catch (e) { auto.checked = !auto.checked; toast(errText(e), "bad"); }
    };
    $("#peer-settings", el)!.innerHTML = peers().map((p) => `<div class="row between prow">
        <div><strong>${esc(p.name)}</strong> <span class="mono muted small">${esc(p.display)}</span></div>
        <div class="row"><label class="small">Their tasks <select data-policy="${esc(p.name)}">
          <option value="ask" ${p.policy === "ask" ? "selected" : ""}>need my approval</option>
          <option value="auto" ${p.policy === "auto" ? "selected" : ""}>run automatically</option></select></label>
          <button class="btn small danger" data-unpair="${esc(p.name)}">Unpair</button></div></div>`).join("") || `<div class="muted">None yet.</div>`;
    el.querySelectorAll<HTMLSelectElement>("[data-policy]").forEach((s) => s.onchange = async () => {
        try { await api.setPolicy(s.dataset.policy!, s.value); toast(`Policy for ${s.dataset.policy}: ${s.value}`, "ok"); }
        catch (e) { toast(errText(e), "bad"); }
    });
    el.querySelectorAll<HTMLButtonElement>("[data-unpair]").forEach((b) => b.onclick = async () => {
        const name = b.dataset.unpair!;
        if (!(await confirmBox(`Unpair ${name}?`, "It will no longer be able to connect; you would need to pair again.", "Unpair", true))) return;
        try { await api.unpair(name); toast(`Unpaired ${name}`, "ok"); } catch (e) { toast(errText(e), "bad"); }
    });
    api.configList().then((cfg) => {
        $("#cfg", el)!.innerHTML = Object.keys(CONFIG_HELP).map((k) => `<div class="row between crow">
            <div><span class="mono">${esc(k)}</span><div class="muted small">${esc(CONFIG_HELP[k])}</div></div>
            <div class="row">${k === "default_policy" || k === "log_level" || k === "mdns"
                ? `<select data-cfg="${k}">${(k === "default_policy" ? ["ask", "auto"] : k === "mdns" ? ["true", "false"] : ["debug", "info", "warn", "error"])
                    .map((v) => `<option ${String(cfg[k]) === v ? "selected" : ""}>${v}</option>`).join("")}</select>`
                : `<input data-cfg="${k}" value="${esc(cfg[k])}" spellcheck="false"/>`}
              <button class="btn small" data-save="${k}">Save</button></div></div>`).join("")
            + `<p class="muted small">Changes apply after the daemon restarts.</p>`;
        el.querySelectorAll<HTMLButtonElement>("[data-save]").forEach((b) => b.onclick = () => busy(b, async () => {
            const k = b.dataset.save!;
            const v = (el.querySelector(`[data-cfg="${k}"]`) as HTMLInputElement).value;
            try { await api.configSet(k, v); toast(`${k} saved; restart the daemon to apply`, "ok"); } catch (e) { toast(errText(e), "bad"); }
        }));
    }).catch((e) => { $("#cfg", el)!.textContent = errText(e); });
    const doc = $<HTMLButtonElement>("#doctor", el)!;
    doc.onclick = () => busy(doc, async () => {
        try {
            const checks: Check[] = await api.doctor();
            $("#doc", el)!.innerHTML = `<ul class="checks">${checks.map((c) => `<li class="${c.status}">
              <span class="ck">${c.status === "ok" ? "✓" : c.status === "fail" ? "✕" : c.status === "warn" ? "!" : "i"}</span>
              <div><strong>${esc(c.name)}</strong> <span class="muted">${esc(c.detail)}</span>${c.fix ? `<div class="fix">Fix: ${esc(c.fix)}</div>` : ""}</div></li>`).join("")}</ul>`;
        } catch (e) { toast(errText(e), "bad"); }
    });
}

// ----------------------------------------------------------------- boot --

Events.On("kith:state", (ev: any) => {
    snap = ev.data as Snapshot;
    const {name, param} = route();
    // Live views re-render; forms (settings, pair code) only refresh chrome.
    if (name === "settings" || (name === "pair" && pair?.kind === "code")) renderChrome();
    else if (name === "tasks" && param) { renderChrome(); refreshTaskHead(param); }
    else render(false);
});

Events.On("kith:pair", (ev: any) => {
    pair = ev.data as PairEvent;
    if (pair.kind === "done" || pair.kind === "error") pairBusy = false;
    if (pair.kind === "done") toast(`Paired with ${pair.data?.name}`, "ok");
    if (route().name === "pair") render(false);
});

api.snapshot().then((s) => { snap = s; render(true); }).catch((e) => { snap.error = errText(e); render(true); });
