import { html, raw, type Raw } from "../core/html";
import { S, hereWord, peers } from "../core/store";
import { agoShort, ago, date, gb, osLong, plural } from "../core/fmt";
import type { Peer } from "../core/api";
import { btn, glyph, infobar, osIcon, seg } from "../ui/parts";
import { icon } from "../ui/icons";

function caps(p: Peer): Raw {
    const m = p.manifest;
    const rows: [string, string, boolean?][] = [];
    if (m) {
        rows.push(["OS", `${osLong(m.os)} · ${m.arch}`]);
        rows.push(["RAM", gb(m.ram_gb)]);
        for (const g of m.gpus ?? []) rows.push(["GPU", `${g.name} · ${gb(g.free_vram_gb)} free of ${gb(g.vram_gb)}`]);
        if (!(m.gpus ?? []).length) rows.push(["GPU", "no NVIDIA GPU"]);
        rows.push(["Disk", gb(m.free_disk_gb) + " free"]);
        rows.push(["Python", (m.python ?? []).join(", ") || "none found", true]);
        rows.push(["Packages", Object.entries(m.packages ?? {}).sort().map(([k, v]) => `${k} ${v}`).join(" · ") || "none of torch, numpy", true]);
    }
    return html`<div class="kv2 tight">${rows.map(([k, v, mono]) => html`<div class="kv"><span class="k">${k}</span><span class="v ${mono ? "mono ell" : ""}">${v}</span></div>`)}
        <div class="kv"><span class="k">Fingerprint</span><span class="v mono">${p.display} <button class="ibtn tiny" data-act="copy" data-arg="${p.display}" data-label="fingerprint" title="Copy">${raw(icon("copy", 14))}</button></span></div></div>`;
}

function card(p: Peer): Raw {
    const auto = p.policy === "auto";
    if (!p.reachable && !p.manifest) {
        return html`<div class="card" id="peer-${p.fp}"><div class="peer-head">${glyph(osIcon(), 44, true)}
            <div class="grow"><div class="h16 strong muted row gap8">${p.name}${raw(`<span class="dot off"></span>`)}<span class="small normal">Offline</span></div>
            <div class="sub">Last seen ${ago(date(p.last_seen)) || "never"}${p.last_addr ? html` at <span class="mono">${p.last_addr}</span>` : ""} · <span class="mono">${p.display}</span></div></div>
            ${btn("Unpair…", "ask-unpair", "danger", { arg: p.fp })}</div></div>`;
    }
    return html`<div class="card" id="peer-${p.fp}">
        <div class="peer-head">${glyph(osIcon(p.manifest?.os), 44, !p.reachable)}
            <div class="grow"><div class="h16 strong row gap8">${p.name}${raw(`<span class="dot ${p.reachable ? "green" : "off"}"></span>`)}<span class="small normal ${p.reachable ? "green" : "muted"}">${p.reachable ? "Reachable" : "Offline"}</span></div>
            <div class="sub">Last address <span class="mono">${p.last_addr ?? "—"}</span> · seen ${p.reachable ? "just now" : (agoShort(date(p.last_seen)) || "never") + " ago"}</div></div>
            ${btn(S.busy.refresh ? "Refreshing…" : "Refresh", "refresh", "std", { ic: "refresh-cw", disabled: !!S.busy.refresh })}
            ${btn("Unpair…", "ask-unpair", "danger", { arg: p.fp })}</div>
        ${caps(p)}
        <div class="peer-policy"><div class="grow"><div>When ${p.name} sends a task to ${hereWord()}</div>
            <div class="sub">${auto ? "Runs without asking." : "You approve each task before any file moves."}</div></div>
            ${seg([["ask", "Ask"], ["auto", "Auto"]], p.policy ?? "ask", "policy:" + p.fp)}</div>
        ${auto ? html`<div class="pad-x pb16 fade-in">${infobar("caution", `Auto runs every task from ${p.name} as ${S.info.user || "you"}, without asking.`, "Use it only for a machine you fully control.")}</div>` : ""}
    </div>`;
}

export function machinesView(): Raw {
    const ps = peers();
    return html`<div class="page narrow" id="page-machines">
        <div class="sub lead">${ps.length ? `${plural(ps.length, "paired machine")}. ${ps.length === 1 ? "It was" : "Each was"} verified with a code shown on both screens.` : "No paired machines yet."}</div>
        ${ps.map(card)}
        ${!ps.length && S.loaded ? html`<div class="card empty">${raw(icon("link-2", 28))}<div class="h16">Pair your other machine</div>
            <div class="muted">Pair once with a 6-character code shown on both screens.</div>${btn("Pair a machine", "pair", "accent")}</div>` : ""}
    </div>`;
}

/** Doctor check names, as people write them. */
export function checkTitle(n: string): string {
    const known: Record<string, string> = {
        vpn: "VPN", mdns: "mDNS", "lan address": "LAN address", "local network": "Local Network permission",
        firewall: "Firewall rule TCP 7420", "firewall mdns": "Firewall rule mDNS UDP 5353", "network profile": "Network profile",
        python: "Python", "nvidia-smi": "nvidia-smi", listener: "Listener", daemon: "Daemon", peers: "Peers",
    };
    if (known[n.toLowerCase()]) return known[n.toLowerCase()];
    if (n.startsWith("peer ")) return "Peer: " + n.slice(5);
    return n[0].toUpperCase() + n.slice(1);
}

export function diagnosticsView(): Raw {
    const checks = S.doctor ?? [];
    const IC: Record<string, [string, string]> = { ok: ["circle-check", "green"], warn: ["triangle-alert", "orange"], fail: ["circle-x", "red"], info: ["info", "muted"] };
    const n = (s: string) => checks.filter((c) => c.status === s).length;
    const head = S.doctorRunning ? `Running ${Math.min(S.doctorShown + 1, Math.max(checks.length, 1))} of ${Math.max(checks.length, 1)} checks…`
        : checks.length ? [`${n("ok")} ok`, n("warn") && `${plural(n("warn"), "warning")}`, n("fail") && `${n("fail")} failing`, n("info") && plural(n("info"), "note")].filter(Boolean).join(", ") + ` · checked ${ago(new Date(S.doctorAt))}`
            : "Checks the daemon, network, firewall, discovery and each peer.";
    return html`<div class="page narrow" id="page-diagnostics">
        <div class="sub lead">${head}</div>
        ${!checks.length ? html`<div class="card pad row gap8 muted">${raw(`<span class="spin" style="width:16px;height:16px"></span>`)}Running checks…</div>` : ""}
        <div class="diag">${checks.map((c, i) => {
            const shown = i < S.doctorShown;
            const [ic, color] = shown ? IC[c.status] ?? IC.info : ["", "muted"];
            const open = shown && !!c.fix && !!S.open["diag:" + c.name];
            return html`<div class="card dcheck" id="dc-${i}">
                <button class="dc-row" data-act="toggle-open" data-arg="diag:${c.name}" ${c.fix ? "" : raw("disabled")}>
                    <span class="${color}">${shown ? raw(icon(ic, 18, "pop")) : raw(`<span class="spin" style="width:16px;height:16px"></span>`)}</span>
                    <span class="strong">${checkTitle(c.name)}</span>
                    <span class="sub brk">${shown ? c.detail : "Checking…"}</span>
                    <span class="chev ${open ? "open" : ""}">${shown && c.fix ? raw(icon("chevron-down", 18)) : ""}</span>
                </button>
                ${open ? html`<div class="dc-fix fade-in"><div class="strong">How to fix</div>${c.fix}</div>` : ""}
            </div>`;
        })}</div>
    </div>`;
}
