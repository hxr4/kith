import { html, raw, type Raw } from "../core/html";
import { S, full, hereWord, isSent, pending, peerLabel, peerOf, tasks } from "../core/store";
import { ago, argv, bytes, date, hms, plural } from "../core/fmt";
import type { Task } from "../core/api";
import { btn, glyph, infobar, osIcon, ring } from "../ui/parts";
import { icon } from "../ui/icons";

export function reqList(t: Task, compact = false): Raw {
    const reqs = (t.requirements ?? []).filter((q) => !compact || !q.req.startsWith("disk"));
    if (!reqs.length) return html`<span class="muted small">No requirements</span>`;
    return html`${reqs.map((q) => html`<div class="req ${compact ? "inline" : ""}">
        <span class="q-${q.status}">${raw(icon(q.status === "pass" ? "check" : q.status === "fail" ? "x" : "circle-minus", 14))}</span>
        <span class="${compact ? "" : "grow"}">${q.req}</span>${compact ? "" : html`<span class="muted">${q.status === "unknown" ? "checked before it runs" : q.have}</span>`}</div>`)}`;
}

/** Full approval card (Approvals screen). */
function card(row: Task): Raw {
    const t = full(row);
    const p = peerOf(t.peer_fp);
    const leaving = S.leaving[t.id];
    const files = t.inputs ?? [];
    const total = files.reduce((a, f) => a + f.size, 0);
    const open = S.open["argv:" + t.id];
    return html`<div class="card approval ${leaving ? "leave-" + leaving : ""}" id="ap-${t.id}">
        <div class="ap-head">${glyph(osIcon(p?.manifest?.os), 40)}
            <div class="grow"><div class="h16"><span class="strong">${peerLabel(t)}</span> wants to run <span class="strong">${t.name}</span></div>
            <div class="sub">Project ${t.project || "—"} · requested ${hms(date(t.created_at))} · <span class="mono">${p?.display ?? ""}</span> verified</div></div>
            ${ring(t.created_at)}</div>
        <div class="argv-box"><span class="mono small ${open ? "brk" : "ell"}">${argv(t.argv) || "loading…"}</span>
            <button class="link-btn" data-act="toggle-open" data-arg="argv:${t.id}">${open ? "Show less" : "Show all"}</button></div>
        <div class="ap-cols">
            <div><div class="sub strong mb6">${plural(files.length, "file")} · ${bytes(total)}</div>
                ${files.slice(0, 8).map((f) => html`<div class="row between mono small"><span class="ell">${f.path}</span><span class="muted">${bytes(f.size)}</span></div>`)}
                ${files.length > 8 ? html`<div class="muted small">and ${files.length - 8} more</div>` : ""}</div>
            <div><div class="sub strong mb6">Asked for · ${hereWord()} has</div>${reqList(t)}</div>
        </div>
        <div class="ap-foot"><span class="sub grow ell">Workspace <span class="mono">${t.ws_dir ?? ""}</span></span>
            ${btn("Deny", "deny", "std", { arg: t.id, disabled: !!leaving })}${btn("Approve", "approve", "accent", { arg: t.id, disabled: !!leaving })}</div>
    </div>`;
}

/** Compact card (flyout, Pi). */
export function approvalCompact(row: Task, lite = false): Raw {
    const t = full(row);
    const leaving = S.leaving[t.id];
    const files = t.inputs ?? [];
    const total = files.reduce((a, f) => a + f.size, 0);
    const open = S.open["argv:" + t.id];
    return html`<div class="card ap-mini ${leaving ? "leave-" + leaving : ""}" id="apm-${t.id}">
        <div class="row start gap10"><div class="grow small"><span class="strong">${peerLabel(t)}</span> wants to run <span class="strong">${t.name}</span></div>${ring(t.created_at, 900, lite ? 24 : 20)}</div>
        <div class="row gap8 base mt6"><span class="mono tiny2 grow ${open ? "brk" : "ell"}">${argv(t.argv) || "loading…"}</span>
            <button class="link-btn tiny" data-act="toggle-open" data-arg="argv:${t.id}">${open ? "less" : "more"}</button></div>
        ${files.length ? html`<div class="sub mt6">${plural(files.length, "file")} · ${bytes(total)}</div>` : ""}
        <div class="reqs-inline mt4">${reqList(t, true)}</div>
        <div class="sub mt6">Runs as ${S.info.user || "you"} on ${hereWord()}, without a sandbox.</div>
        <div class="grid2 mt10">${btn("Approve", "approve", "accent", { arg: t.id, disabled: !!leaving })}${btn("Deny", "deny", "std", { arg: t.id, disabled: !!leaving })}</div>
    </div>`;
}

function outcome(t: Task): [string, string, string] {
    if (t.state === "REJECTED") {
        const d = (t.detail ?? "").toLowerCase();
        if (d.includes("not approved")) return ["Expired", "clock", "muted"];
        if (d.includes("denied")) return ["Denied", "ban", "muted"];
        return ["Turned down", "ban", "red"];
    }
    return [S.decided[t.id] ?? "Approved", "check", "green"];
}

export function approvalsView(): Raw {
    const list = pending();
    const history = tasks().filter((t) => !isSent(t) && !list.includes(t)).slice(0, 20);
    const lastApproved = history.find((t) => S.decided[t.id] === "Approved");
    return html`<div class="page narrow" id="page-approvals">
        ${infobar("info", "", html`Approving lets the peer run its command on ${hereWord()} as <span class="mono">${S.info.user || "you"}</span>, with your files and network. Kith checks requirements, not intent: there is no sandbox.`)}
        ${list.map(card)}
        ${!list.length ? html`<div class="card pad center muted fade-in">${lastApproved && !["COMPLETED", "FAILED", "CANCELLED", "REJECTED"].includes(lastApproved.state)
            ? `${lastApproved.name} is running on ${hereWord()}. Nothing else is waiting.` : "Nothing is waiting for approval."}</div>` : ""}
        ${history.length ? html`<section><h3>History</h3><div class="card list">${history.map((t) => {
            const [label, ic, color] = outcome(t);
            return html`<button class="hrow" id="h-${t.id}" data-act="go" data-arg="#/tasks/${encodeURIComponent(t.id)}">
                <span class="strong ell">${t.name}</span><span class="row gap4 ${color}">${raw(icon(ic, 16))}${label}</span>
                <span class="mono small muted ell">${argv(S.details[t.id]?.argv) || t.project}</span><span class="muted small r">${ago(date(t.created_at))}</span></button>`;
        })}</div></section>` : ""}
    </div>`;
}
