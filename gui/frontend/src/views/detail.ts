import { esc, html, raw, type Raw } from "../core/html";
import { S, copy, full, hereWord, isEnded, isSent, peerLabel, peerOf, tasks } from "../core/store";
import { api } from "../core/api";
import type { Task, Transition } from "../core/api";
import { argv as fmtArgv, bytes, clock, date, hms, sha } from "../core/fmt";
import { btn, chip, link, osIcon, type LinkMode } from "../ui/parts";
import { icon } from "../ui/icons";

// ---- the live log console (owned imperatively; never re-rendered) ----

type Kind = "out" | "kith" | "warn" | "err";

class LogConsole {
    offset = 0;
    partial = "";
    count = 0;
    done = false;
    inTb = false;
    following = true;
    userScrolling = false;
    seenAt = 0;
    wrap = false;
    query = "";
    timer = 0;
    root?: HTMLElement;
    body?: HTMLElement;
    texts: string[] = [];
    kinds: Kind[] = [];

    constructor(readonly id: string, readonly rejected: boolean, readonly peer: string) {}

    mount(root: HTMLElement) {
        if (this.root === root) return;
        this.root = root;
        root.innerHTML = `
            <div class="con-bar"><span class="strong">Log</span><span class="con-follow"></span><span class="grow"></span>
              <label class="con-find">${icon("search", 14)}<input type="text" placeholder="Find in log" aria-label="Find in log"><span class="con-n faint tiny"></span></label>
              <button class="ibtn con-wrap" title="Wrap lines" aria-label="Wrap lines">${icon("wrap-text", 16)}</button>
              <button class="ibtn con-copy" title="Copy log" aria-label="Copy log">${icon("copy", 16)}</button></div>
            <div class="con-body" tabindex="0"></div>
            <button class="jump" hidden>${icon("arrow-down", 16)}Jump to live<span class="mono jump-n"></span></button>`;
        this.body = root.querySelector(".con-body") as HTMLElement;
        const input = root.querySelector("input") as HTMLInputElement;
        input.addEventListener("input", () => { this.query = input.value.trim().toLowerCase(); this.applyFilter(); });
        root.querySelector(".con-wrap")!.addEventListener("click", (e) => {
            this.wrap = !this.wrap;
            (e.currentTarget as HTMLElement).classList.toggle("on", this.wrap);
            this.body!.classList.toggle("wrap", this.wrap);
        });
        root.querySelector(".con-copy")!.addEventListener("click", () => copy(this.texts.join("\n"), `${this.texts.length} lines`));
        root.querySelector(".jump")!.addEventListener("click", () => { this.following = true; this.body!.scrollTop = this.body!.scrollHeight; this.chrome(); });
        this.body.addEventListener("wheel", () => { this.userScrolling = true; }, { passive: true });
        this.body.addEventListener("pointerdown", () => { this.userScrolling = true; });
        this.body.addEventListener("keydown", () => { this.userScrolling = true; });
        this.body.addEventListener("scroll", () => {
            const b = this.body!;
            const up = b.scrollHeight - b.scrollTop - b.clientHeight > 12;
            if (up && this.userScrolling && this.following) { this.following = false; this.seenAt = this.count; }
            if (!up && !this.following) this.following = true;
            this.userScrolling = false;
            this.chrome();
        });
        this.redrawAll();
        this.chrome();
        if (!this.timer) this.poll();
    }

    stop() { clearTimeout(this.timer); this.timer = -1; }

    private async poll() {
        if (this.timer === -1) return;
        try {
            const r = await api.logs(this.id, this.offset);
            if (r.data) this.append(r.data);
            this.offset = r.offset;
            if (r.done) {
                if (this.partial) { this.push([this.classify(this.partial)]); this.partial = ""; }
                this.done = true;
                this.chrome();
                return;
            }
        } catch { /* daemon busy; try again */ }
        this.timer = window.setTimeout(() => this.poll(), S.info.lite ? 2000 : 700);
    }

    private append(chunk: string) {
        let text = this.partial + chunk;
        this.partial = "";
        const nl = text.lastIndexOf("\n");
        if (nl < 0) { this.partial = text; return; }
        this.partial = text.slice(nl + 1);
        text = text.slice(0, nl);
        this.push(text.split("\n").map((l) => this.classify(l.replace(/\r$/, ""))));
    }

    private classify(s: string): [string, Kind] {
        if (s.startsWith("Traceback (most recent call last)")) this.inTb = true;
        const lo = s.toLowerCase();
        if (this.inTb) {
            if (!s.startsWith(" ") && !s.startsWith("Traceback")) this.inTb = false;
            return [s, "err"];
        }
        if (s.startsWith("[kith]") || s.startsWith("$ ")) return [s, "kith"];
        if (/(^error|error:)/.test(lo)) return [s, "err"];
        if (lo.includes("warning")) return [s, "warn"];
        return [s, "out"];
    }

    private lineEl(n: number, s: string, k: Kind) {
        const d = document.createElement("div");
        d.className = "ln " + k;
        d.innerHTML = `<span class="ln-n">${n}</span><span class="ln-t">${esc(s) || " "}</span>`;
        if (this.query && !s.toLowerCase().includes(this.query)) d.hidden = true;
        return d;
    }

    private push(lines: [string, Kind][]) {
        if (!lines.length) return;
        this.kinds.push(...lines.map((l) => l[1]));
        if (this.body) {
            const frag = document.createDocumentFragment();
            for (const [s, k] of lines) { this.count++; this.texts.push(s); frag.append(this.lineEl(this.count, s, k)); }
            this.body.append(frag);
            const max = 20000;
            while (this.body.childElementCount > max) this.body.firstElementChild!.remove();
            if (this.following) this.body.scrollTop = this.body.scrollHeight;
        } else {
            for (const [s] of lines) { this.count++; this.texts.push(s); }
        }
        for (let i = lines.length - 1; i >= 0; i--) if (lines[i][1] === "err") { lastErrors[this.id] = lines[i][0]; break; }
        this.chrome();
    }

    private redrawAll() {
        if (!this.body) return;
        this.body.innerHTML = "";
        const frag = document.createDocumentFragment();
        this.texts.forEach((s, i) => frag.append(this.lineEl(i + 1, s, this.kinds[i] ?? "out")));
        this.body.append(frag);
        this.body.scrollTop = this.body.scrollHeight;
    }

    private applyFilter() {
        if (!this.body) return;
        let n = 0;
        this.body.querySelectorAll<HTMLElement>(".ln").forEach((d) => {
            const hit = !this.query || d.textContent!.toLowerCase().includes(this.query);
            d.hidden = !hit;
            if (hit) n++;
        });
        (this.root!.querySelector(".con-n") as HTMLElement).textContent = this.query ? String(n) : "";
    }

    private chrome() {
        if (!this.root) return;
        const f = this.root.querySelector(".con-follow") as HTMLElement;
        const live = !this.done;
        f.className = "con-follow " + (live && this.following ? "acc" : "muted");
        f.innerHTML = live && this.following ? `<span class="dot acc k-pulse"></span>Following` : live ? "Paused" : `${this.count} lines`;
        const jump = this.root.querySelector(".jump") as HTMLElement;
        const show = live && !this.following;
        if (show && jump.hidden) { jump.hidden = false; jump.classList.remove("in"); void jump.offsetWidth; jump.classList.add("in"); }
        if (!show) jump.hidden = true;
        const n = this.count - this.seenAt;
        (jump.querySelector(".jump-n") as HTMLElement).textContent = n > 0 ? `+${n}` : "";
        if (!this.count && this.body) {
            this.body.dataset.empty = this.rejected ? `No log. ${this.peer} turned the task down before it started.` : live ? "Waiting for output…" : "This task printed nothing.";
        } else if (this.body) delete this.body.dataset.empty;
    }
}

const consoles: Record<string, LogConsole> = {};
export const lastErrors: Record<string, string | undefined> = {};

/** After each render: attach the console for the open task. */
export function mountConsole() {
    const el = document.querySelector<HTMLElement>("[data-console]");
    for (const id of Object.keys(consoles)) if (!el || el.dataset.console !== id) { consoles[id].stop(); delete consoles[id]; }
    if (!el) return;
    const id = el.dataset.console!;
    const t = tasks().find((x) => x.id === id);
    const c = consoles[id] ??= new LogConsole(id, t?.state === "REJECTED", t ? peerLabel(t) : "The peer");
    c.mount(el);
}

// ---- the detail page ----

function linkMode(t: Task): LinkMode {
    if (!peerOf(t.peer_fp)?.reachable) return "off";
    if (t.state === "TRANSFERRING") return isSent(t) ? "flow" : "back";
    if (t.state === "RUNNING") return isSent(t) ? "back" : "flow";
    return "idle";
}

function timeline(t: Task): Raw {
    const tl: Transition[] = t.timeline ?? [];
    return html`<div class="card pad tl"><div class="strong mb12">Timeline</div>${tl.map((tr, i) => {
        const last = i === tl.length - 1;
        const cur = last && !isEnded(t.state);
        const resumes = tr.state === "INTERRUPTED" && tl.slice(i + 1).some((x) => x.state === "RUNNING");
        return html`<div class="tl-row" id="tl-${t.id}-${tr.seq}">
            <div class="tl-rail"><span class="tl-dot s-${tr.state.toLowerCase()} ${cur ? "cur k-pulse-soft" : ""}"></span>${last ? "" : html`<span class="tl-line ${resumes ? "resume" : ""}"></span>`}</div>
            <div class="tl-body"><div class="row gap8">${chip(tr.state)}<span class="grow"></span><span class="mono faint tiny">${hms(date(tr.ts))}</span></div>
            ${tr.detail ? html`<div class="sub mt4">${tr.detail}</div>` : ""}
            ${resumes ? html`<div class="resume-tag">${raw(icon("rotate-ccw", 14))}resumes as a new attempt</div>` : ""}</div></div>`;
    })}</div>`;
}

function info(t: Task): Raw {
    const dir = isSent(t) ? t.project_dir : t.ws_dir;
    const rows: [string, string, string?][] = [["argv", fmtArgv(t.argv) || "—"], ["Working directory", dir || "—"]];
    if (t.pid) rows.push(["Process", String(t.pid), "Visible in Task Manager while it runs"]);
    rows.push(["Created · updated", `${hms(date(t.created_at))} · ${hms(date(t.updated_at))}`]);
    if (t.outputs?.length) rows.push(["Outputs", t.outputs.join(", ")]);
    const reqs = t.requirements ?? [];
    return html`<div class="card pad info-card"><div class="strong">Info</div>
        ${rows.map(([k, v, note]) => html`<div><div class="sub">${k}</div><div class="mono small brk">${v}</div>${note ? html`<div class="faint tiny">${note}</div>` : ""}</div>`)}
        ${reqs.length ? html`<div><div class="sub">Requirements · ${isSent(t) ? peerLabel(t) : hereWord()}</div>${reqs.map((q) => html`<div class="req">
            <span class="q-${q.status}">${raw(icon(q.status === "pass" ? "check" : q.status === "fail" ? "x" : "circle-minus", 14))}</span>
            <span class="grow">${q.req}</span><span class="muted">${!isSent(t) && q.status === "unknown" ? "checked before it runs" : q.have}</span></div>`)}</div>` : ""}
    </div>`;
}

function failure(t: Task): Raw {
    const rejected = t.state === "REJECTED";
    const title = rejected ? (isSent(t) ? `${peerLabel(t)} can’t run this task` : `${hereWord()[0].toUpperCase() + hereWord().slice(1)} turned this task down`) : `${t.argv?.[0] ?? t.name} failed`;
    let detail = t.detail ?? "";
    if (t.code && detail.startsWith(t.code + ": ")) detail = detail.slice(t.code.length + 2);
    const last = lastErrors[t.id];
    const details = [`${t.name} (${t.id})`, t.state, t.code, t.detail, last, t.remediation ? "Fix: " + t.remediation : ""].filter(Boolean).join("\n");
    return html`<div class="infobar error fail-bar">
        <span class="ib-ic">${raw(icon("x", 11))}</span>
        <div><div class="row wrap gap8"><span class="strong">${title}</span>${t.code ? html`<span class="mono code-red">${t.code}</span>` : ""}</div>
            ${detail ? html`<div class="mt2">${detail}</div>` : ""}
            ${last && last !== detail ? html`<div class="mono small code-red mt2 ell">${last}</div>` : ""}
            ${t.remediation ? html`<div class="sub mt6">${t.remediation}</div>` : ""}</div>
        <button class="btn std" data-act="copy" data-arg="${details}" data-label="error details">${raw(icon("copy", 16))}Copy details</button>
    </div>`;
}

function artifacts(t: Task): Raw {
    const arts = t.artifacts ?? [];
    if (!arts.length) return raw("");
    return html`<div class="card arts"><div class="ahead"><span class="strong">Artifacts</span><span class="r">Size</span><span>SHA-256</span><span>Where</span><span></span></div>
        ${arts.map((a) => {
            const local = a.location === "local";
            const busy = S.fetching[t.id + a.path];
            const where = busy ? "fetching…" : local ? (a.copy_back ? "copied back" : "fetched") : a.producer_offline ? "peer offline" : isSent(t) ? "on peer" : "here";
            const color = local ? "green" : a.producer_offline ? "orange" : "off";
            const base = isSent(t) ? t.project_dir : t.ws_dir;
            const lp = base ? `${base}/${a.path}` : "";
            return html`<div class="arow" id="art-${t.id}-${a.path}">
                <span class="mono small ell">${a.path}</span><span class="muted r">${bytes(a.size)}</span>
                <button class="sha mono small" data-act="copy" data-arg="${a.sha256}" data-label="SHA-256">${sha(a.sha256)}${raw(icon("copy", 13))}</button>
                <span class="row gap6 muted">${raw(`<span class="dot ${color}"></span>`)}${where}</span>
                <span class="r">${busy ? raw(`<span class="spin" style="width:16px;height:16px"></span>`)
                    : local || !isSent(t) ? (lp ? html`<button class="ibtn" data-act="reveal" data-arg="${lp}" title="Show in folder">${raw(icon("folder-open", 16))}</button>` : "")
                    : !a.producer_offline ? html`<button class="btn std small" data-act="fetch" data-arg="${a.path}" data-task="${t.id}">Fetch</button>` : ""}</span>
            </div>`;
        })}</div>`;
}

export function detailView(id: string): Raw {
    const row = tasks().find((t) => t.id === id);
    const t = row ? full(row) : S.details[id];
    if (!t) return html`<div class="page" id="page-detail-${id}"><div class="center muted">${raw(`<span class="spin" style="width:20px;height:20px"></span>`)} Loading task…</div></div>`;
    const ended = isEnded(t.state);
    const p = peerOf(t.peer_fp);
    const subParts = [isSent(t) ? `Sent to ${peerLabel(t)}` : `From ${peerLabel(t)}`, `project ${t.project || "—"}`];
    if (t.attempt > 1) subParts.push(`attempt ${t.attempt}`);
    if (t.state === "REJECTED") subParts.push("never started");
    else if (t.started_at) {
        const s = date(t.started_at)!, e = ended ? date(t.updated_at)! : new Date();
        subParts.push((ended ? "ran " : "") + clock((+e - +s) / 1000) + (ended ? "" : " elapsed"));
    }
    if (t.pid) subParts.push(`process ${t.pid}`);
    const dir = isSent(t) ? t.project_dir : t.ws_dir;
    const mode = linkMode(t);
    return html`<div class="page" id="page-detail-${id}">
        <div class="dhead">
            ${chip(t.state, true)}
            <span class="sub grow">${subParts.join(" · ")}</span>
            <div class="mini-link">${raw(icon(S.info.platform === "windows" ? "monitor" : "server", 18))}${link(mode)}${raw(icon(osIcon(p?.manifest?.os), 18))}</div>
            ${dir && (!isSent(t) || ended) ? btn(isSent(t) ? "Open project folder" : S.info.platform === "windows" ? "Open workspace in Explorer" : "Open workspace", "reveal", "std", { arg: dir, ic: "folder-open" }) : ""}
            ${!ended ? html`<div class="pop-anchor">${btn("Cancel…", "ask-cancel", "danger", { arg: t.id })}
                ${S.confirmCancel === t.id ? html`<div class="menu confirm" role="dialog">
                    <div class="strong">Stop ${t.name}?</div>
                    <div class="sub mt4 mb12">${isSent(t) ? `${peerLabel(t)} ends the process. Files it already wrote stay there and can still be fetched.` : `Kith ends ${t.pid ? "process " + t.pid : "the process"}. Files it already wrote stay in the workspace and ${peerLabel(t)} can still fetch them.`}</div>
                    <div class="grid2">${btn("Stop task", "cancel-task", "accent", { arg: t.id })}${btn("Keep running", "ask-cancel", "std", { arg: "" })}</div>
                </div>` : ""}</div>` : ""}
        </div>
        <div class="dgrid">
            <div class="col">${timeline(t)}${info(t)}</div>
            <div class="col">
                ${t.state === "FAILED" || t.state === "REJECTED" ? failure(t) : ""}
                <div class="card console ${t.state === "REJECTED" ? "short" : ""}" data-keep="console-${id}" data-console="${id}"></div>
                ${artifacts(t)}
            </div>
        </div>
    </div>`;
}
