import { html, raw, type Raw } from "../core/html";
import { S, active, hereWord, isSent, peerLabel, peers, pending, tasks, thisName, needsApproval } from "../core/store";
import { agoShort, date, gb, hms, osLong, plural } from "../core/fmt";
import type { Task, Manifest, Peer } from "../core/api";
import { btn, chip, elapsed, glyph, infobar, link, osIcon, type LinkMode } from "../ui/parts";
import { icon } from "../ui/icons";

/** InfoBars from doctor results: Windows Firewall or a Public network. */
export function reachabilityBar(): Raw {
    const d = S.doctor ?? [];
    const fw = d.find((c) => c.name === "firewall" && c.status === "fail");
    const pub = d.find((c) => c.name === "network profile" && c.status === "warn");
    if (fw && !S.ibClosed.firewall) {
        const who = peers()[0]?.name ?? "Your other machines";
        return infobar("caution", "Windows Firewall is blocking Kith.", `Allow it on private networks so ${who} can reach ${hereWord()}.`,
            btn(S.busy.firewall ? "Waiting for the Administrator window…" : "Allow on private networks", "firewall", "std", { ic: "shield-user", disabled: !!S.busy.firewall }), "ib-close:firewall");
    }
    if (pub && !S.ibClosed.public) {
        return infobar("caution", "This network is set to Public.", "Kith only listens on Private networks. Change the network profile type to Private in Settings › Network & internet.",
            btn("Run checks", "go", "std", { arg: "#/diagnostics" }), "ib-close:public");
    }
    return raw("");
}

export function linkMode(p?: Peer): LinkMode {
    if (!p || !p.reachable) return "off";
    const mine = active().filter((t) => t.peer_fp === p.fp);
    // Data toward this machine: received files, or logs of what we sent.
    if (mine.some((t) => t.state === "TRANSFERRING" && !isSent(t))) return "back";
    if (mine.some((t) => t.state === "RUNNING" && isSent(t))) return "back";
    if (mine.some((t) => t.state === "TRANSFERRING" && isSent(t))) return "flow";
    if (mine.some((t) => t.state === "RUNNING" && !isSent(t))) return "flow";
    return "idle";
}

function linkLabel(m: LinkMode, p?: Peer): string {
    if (!p) return "";
    if (m === "off") return "offline";
    const mine = active().filter((t) => t.peer_fp === p.fp);
    if (mine.some((t) => t.state === "TRANSFERRING")) return m === "back" ? "receiving files" : "sending files";
    if (mine.some((t) => t.state === "RUNNING")) return "streaming logs";
    return "connected";
}

function capRows(m: Manifest | null): Raw {
    const st = S.snap.status;
    const rows: [string, Raw | string, boolean?][] = [];
    if (m) {
        rows.push(["RAM", gb(m.ram_gb)]);
        rows.push(["Disk", gb(m.free_disk_gb) + " free"]);
        rows.push(["Python", (m.python ?? []).join(", ") || "none found", true]);
        const pk = Object.entries(m.packages ?? {}).sort().map(([k, v]) => `${k} ${v}`).join(" · ");
        rows.push(["Packages", pk || "none of torch, numpy", true]);
        rows.push(["OS", `${osLong(m.os)} · ${m.arch}`]);
    }
    if (st) {
        rows.push(["Port", `${S.config.bind || "0.0.0.0"}:${st.port}`, true]);
        rows.push(["Fingerprint", html`<span class="mono">${st.display}</span> <button class="ibtn tiny" data-act="copy" data-arg="${st.display}" data-label="fingerprint" title="Copy fingerprint">${raw(icon("copy", 14))}</button>`]);
    }
    if (S.info.user) rows.push(["User", S.info.user, true]);
    return html`<div class="kv2">${rows.map(([k, v, mono]) => html`<div class="kv"><span class="k">${k}</span><span class="v ${mono ? "mono ell" : ""}">${v}</span></div>`)}</div>`;
}

function gpuBlock(m: Manifest | null): Raw {
    const g = m?.gpus?.[0];
    if (!g) {
        return html`<div class="offer-gpu"><span class="gpu-ic">${raw(icon("cpu", 24))}</span>
            <div><div class="disp">${m ? "No NVIDIA GPU" : "Reading capabilities…"}</div>
            <div class="sub">${m ? "Tasks that require a GPU are turned down here." : ""}</div></div></div>`;
    }
    const pct = g.vram_gb > 0 ? Math.round((g.free_vram_gb / g.vram_gb) * 100) : 0;
    return html`<div class="offer-gpu"><span class="gpu-ic">${raw(icon("microchip", 24))}</span>
        <div class="grow"><div class="disp">${g.name}</div>
        <div class="meter-row"><span class="meter"><i style="width:${pct}%"></i></span>
        <span><span class="mono strong">${gb(g.free_vram_gb)}</span> <span class="muted">VRAM free of ${g.vram_gb.toFixed(1)} GB</span></span></div></div></div>`;
}

function nowRow(t: Task): Raw {
    const meta = isSent(t) ? `on ${peerLabel(t)}` : `from ${peerLabel(t)}`;
    const extra = t.attempt > 1 ? ` · attempt ${t.attempt}` : "";
    return html`<button class="now-row" id="now-${t.id}" data-act="go" data-arg="#/tasks/${encodeURIComponent(t.id)}">
        ${chip(t.state)}
        <span class="nr-name"><span class="strong">${t.name}</span><span class="sub">${meta}${extra}</span></span>
        <span class="mono muted ell">${S.tails[t.id] ?? t.detail ?? ""}</span>
        ${elapsed(t.started_at || t.created_at)}
    </button>`;
}

function describe(t: Task): string {
    const p = peerLabel(t);
    switch (t.state) {
        case "SUBMITTED": return needsApproval(t) ? `from ${p} is waiting for your approval` : isSent(t) ? `sent to ${p}` : `received from ${p}`;
        case "ACCEPTED": return isSent(t) ? `accepted by ${p}` : `accepted from ${p}`;
        case "TRANSFERRING": return isSent(t) ? `sending files to ${p}` : `receiving files from ${p}`;
        case "RUNNING": return t.attempt > 1 ? `resumed as attempt ${t.attempt}` : isSent(t) ? `running on ${p}` : `running on ${hereWord()}`;
        case "FINALIZING": return "hashing outputs";
        case "INTERRUPTED": return t.detail || `${p} stopped responding`;
        case "REJECTED": return [t.code, t.detail?.replace(/^[A-Z_]+: /, "")].filter(Boolean).join(": ");
        default: return t.detail || (isSent(t) ? `on ${p}` : `from ${p}`);
    }
}

export function activityList(n = 7): Raw {
    const list = [...tasks()].sort((a, b) => +date(b.updated_at)! - +date(a.updated_at)!).slice(0, n);
    if (!list.length) return raw("");
    return html`<section><h3>Activity</h3>${list.map((t) => html`<button class="act-row" id="act-${t.id}" data-act="go" data-arg="#/tasks/${encodeURIComponent(t.id)}">
        <span class="mono faint">${hms(date(t.updated_at))}</span>${chip(t.state)}
        <span class="ell"><span class="strong">${t.name}</span> <span class="muted">${describe(t)}</span></span></button>`)}</section>`;
}

export function overview(): Raw {
    const ps = peers();
    const p = ps.find((x) => x.reachable) ?? ps[0];
    const st = S.snap.status;
    const mode = linkMode(p);
    const local = S.local;
    const me = html`<div class="dev right"><div><div class="dev-name">${thisName()}</div>
        <div class="sub">${S.info.platform === "windows" ? "This PC" : "This machine"}${st ? html` · <span class="mono">${st.port}</span>` : ""}</div></div>
        ${glyph(S.info.lite ? "cpu" : S.info.platform === "windows" ? "monitor" : "server")}</div>`;
    const them = p
        ? html`<div class="dev">${glyph(osIcon(p.manifest?.os), 48, !p.reachable)}<div><div class="dev-name">${p.name} ${raw(`<span class="dot ${p.reachable ? "green" : "off"}"></span>`)}</div>
            <div class="sub">${p.reachable ? `Can reach ${hereWord()} · ${p.last_addr ?? ""}` : `Offline · seen ${agoShort(date(p.last_seen)) || "never"} ago`}</div></div></div>`
        : html`<div class="dev">${glyph("monitor-smartphone", 48, true)}<div><div class="dev-name muted">No peer yet</div><div class="sub">${btn("Pair a machine", "pair", "std", { small: true, ic: "link-2" })}</div></div></div>`;

    return html`<div class="page" id="page-overview">
        ${reachabilityBar()}
        <div class="card link-card">
            ${me}
            <div class="link-mid"><div class="lk-label ${mode === "off" ? "faint" : "acc"}">${linkLabel(mode, p)}</div>${link(mode)}<div class="mono faint tiny">${mode === "off" ? "not connected" : "TLS 1.3 · pinned identity"}</div></div>
            ${them}
        </div>
        <div class="card offer">
            <div class="offer-head"><span class="h16">What ${hereWord()} offers</span>
                <span class="sub">Advertised to peers${S.probedAt ? ` · probed ${hms(new Date(S.probedAt)).slice(0, 5)}` : ""}</span>
                <button class="link-btn" data-act="probe" ${S.busy.probe ? raw("disabled") : ""}>${S.busy.probe ? "Probing…" : "Re-probe"}</button></div>
            ${gpuBlock(local)}
            ${capRows(local)}
        </div>
        ${active().length || pending().length ? html`<section><h3>Now</h3><div class="card list">${active().map(nowRow)}${pending().map((t) =>
            html`<button class="now-row wait" id="nw-${t.id}" data-act="go" data-arg="#/approvals">
                <span class="acc">${raw(icon("hand", 20))}</span><span class="ell"><span class="strong">${t.name}</span> <span class="muted">from ${peerLabel(t)} is waiting for approval</span></span>
                <span class="faint">${raw(icon("chevron-right", 18))}</span></button>`)}</div></section>` : ""}
        ${activityList()}
        ${!ps.length && S.loaded ? html`<div class="card empty">${raw(icon("link-2", 28))}<div class="h16">Pair your other machine</div>
            <div class="muted">Open Kith on both machines and pair once with a 6-character code. After that they find each other on this network.</div>
            ${btn("Pair a machine", "pair", "accent")}</div>` : ""}
    </div>`;
}

export { plural };
