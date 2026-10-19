import { html, raw, type Raw } from "../core/html";
import { S, active, finished, hereWord, isSent, peerLabel, peers, pending, reachable, thisName } from "../core/store";
import { agoShort, date, gb, osName } from "../core/fmt";
import type { Task, Peer } from "../core/api";
import { btn, chip, elapsed, infobar, osIcon } from "../ui/parts";
import { icon } from "../ui/icons";
import { approvalCompact } from "./approvals";

function capLine(p: Peer): string {
    const m = p.manifest;
    if (p.reachable && m) {
        const g = m.gpus?.[0];
        return [osName(m.os), m.arch, g ? `${g.name.replace("NVIDIA GeForce ", "")}` : null, gb(m.ram_gb)].filter(Boolean).join(" · ");
    }
    const seen = agoShort(date(p.last_seen));
    return (m ? osName(m.os) + " · " : "") + (seen ? `last seen ${seen} ago` : "not seen yet");
}

function working(t: Task): Raw {
    const meta = isSent(t) ? `on ${peerLabel(t)}` : `from ${peerLabel(t)}${t.attempt > 1 ? ` · attempt ${t.attempt}` : ""}`;
    return html`<button class="fw-row" id="fw-${t.id}" data-act="open-main" data-arg="#/tasks/${encodeURIComponent(t.id)}">
        <div class="row gap8">${chip(t.state)}<span class="strong grow ell">${t.name}</span>${elapsed(t.started_at || t.created_at)}</div>
        <div class="sub mt4">${meta}</div>
        <div class="mono tiny2 ell mt2">${S.tails[t.id] ?? t.detail ?? ""}</div></button>`;
}

function recentIcon(state: string): Raw {
    const [ic, c] = state === "COMPLETED" ? ["circle-check", "green"] : state === "FAILED" ? ["circle-x", "red"] : state === "REJECTED" ? ["ban", "red"] : ["circle-minus", "muted"];
    return raw(`<span class="${c}">${icon(ic, 16)}</span>`);
}

export function flyoutView(): Raw {
    const off = S.loaded && !S.snap.running;
    const ps = peers();
    const noPeers = !off && S.loaded && !ps.length;
    const win = S.info.platform === "windows";
    const noService = !off && win && /^not installed/i.test(S.service);
    const here = active().filter((t) => !isSent(t));
    const elsewhere = active().filter(isSent);
    const busy = S.busy.daemon;
    const daemonLine = !S.loaded ? "Connecting…" : busy ? "Working…" : off ? "Kith isn’t running"
        : noPeers ? "Running · no peers yet" : `Running · reachable from ${reachable().length} of ${ps.length} peer${ps.length === 1 ? "" : "s"}`;
    const menuOpen = S.open["fly-menu"];
    return html`<div class="fly" id="fly">
        <div class="fly-head">
            <div class="grow"><div class="strong">${thisName()}</div>
                <div class="row gap6 sub">${raw(`<span class="dot ${busy ? "orange" : off || !S.loaded ? "off" : "green"}"></span>`)}${daemonLine}</div></div>
            <button class="ibtn" data-act="toggle-open" data-arg="fly-menu" aria-label="More">${raw(icon("ellipsis", 20))}</button>
            ${menuOpen ? html`<div class="menu fly-menu">
                <button class="mi" data-act="open-main" data-arg="#/overview">${raw(icon("external-link", 18))}Open Kith</button>
                <button class="mi" data-act="open-main" data-arg="#/settings">${raw(icon("settings", 18))}Settings</button>
                ${S.snap.running ? html`<button class="mi" data-act="daemon" data-arg="restart">${raw(icon("rotate-ccw", 18))}Restart daemon</button>` : ""}
                <button class="mi" data-act="quit">${raw(icon("log-out", 18))}Quit</button></div>` : ""}
        </div>
        ${off ? html`<div class="fly-empty">${raw(icon("power", 30, "faint"))}
            <div class="sub">The Kith daemon is stopped. ${ps[0]?.name ?? "Your other machines"} can’t reach ${hereWord()} and nothing will run here.</div>
            ${btn(busy ? "Starting…" : "Start Kith", "daemon", "accent", { arg: "start", wide: true, disabled: !!busy })}</div>` : ""}
        ${noPeers ? html`<div class="fly-empty"><div class="row gap6 faint">${raw(icon(win ? "monitor" : "server", 24))}<span class="dash"></span><span class="ghost-box"></span></div>
            <div class="sub">No machines paired. Pair once with a 6-character code; after that they find each other on this network.</div>
            ${btn("Pair a machine", "open-main", "accent", { arg: "#/pair", wide: true })}</div>` : ""}
        ${noService ? html`<div class="fly-pad">${infobar("caution", "Kith stops when you sign out.", "Install the background service so tasks keep running.",
            btn("Install service…", "open-main", "std", { arg: "#/settings/service", small: true }))}</div>` : ""}
        ${here.length ? html`<div class="fly-sec"><div class="fly-h">${win ? "This PC is working" : `${hereWord()[0].toUpperCase() + hereWord().slice(1)} is working`}</div>${here.slice(0, 3).map(working)}</div>` : ""}
        ${pending().length ? html`<div class="fly-sec pad"><div class="fly-h">Needs approval</div>${pending().slice(0, 2).map((t) => approvalCompact(t))}
            ${pending().length > 2 ? html`<button class="link-btn" data-act="open-main" data-arg="#/approvals">${pending().length - 2} more waiting…</button>` : ""}</div>` : ""}
        ${elsewhere.length ? html`<div class="fly-sec"><div class="fly-h">Running elsewhere</div>${elsewhere.slice(0, 2).map(working)}</div>` : ""}
        ${!off && ps.length ? html`<div class="fly-sec"><div class="fly-h">Peers</div>${ps.map((p) => html`<button class="fp-row" id="fp-${p.fp}" data-act="open-main" data-arg="#/machines">
            ${raw(icon(osIcon(p.manifest?.os), 20, p.reachable ? "" : "faint"))}
            <span class="grow"><span class="block small">${p.name}</span><span class="mono tiny2 muted ell block">${capLine(p)}</span></span>
            <span class="row gap6 small ${p.reachable ? "green" : "muted"}">${raw(`<span class="dot ${p.reachable ? "green" : "off"}"></span>`)}${p.reachable ? "reachable" : "offline"}</span></button>`)}</div>` : ""}
        ${!off && finished().length ? html`<div class="fly-sec"><div class="fly-h">Recent</div>${finished().slice(0, 3).map((t) => html`<button class="rc-row" id="rc-${t.id}" data-act="open-main" data-arg="#/tasks/${encodeURIComponent(t.id)}">
            ${recentIcon(t.state)}<span class="ell small grow">${t.name} <span class="muted">${t.state === "REJECTED" ? t.code ?? "" : t.detail ?? ""}</span></span>
            <span class="faint tiny">${agoShort(date(t.updated_at))}</span></button>`)}</div>` : ""}
        <div class="fly-foot">${btn("New task…", "open-main", "std", { arg: "#/new", ic: "plus", wide: true })}${btn("Open Kith", "open-main", "std", { arg: "#/overview", ic: "external-link", wide: true })}</div>
    </div>`;
}
