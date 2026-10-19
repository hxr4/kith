import { html, raw, type Raw } from "../core/html";
import { S, hereWord, peerOf, peers, thisName, type Dialog } from "../core/store";
import { argv, bytes, clock, pairCode, plural } from "../core/fmt";
import { btn, glyph, infobar, osIcon } from "../ui/parts";
import { icon } from "../ui/icons";

function foot(primary: Raw, secondary: Raw): Raw {
    return html`<div class="dlg-foot">${primary}${secondary}</div>`;
}

function newTask(d: Extract<Dialog, { kind: "newtask" }>): Raw {
    const c = d.check;
    const peer = peerOf(d.peer ?? "");
    const n = c?.requirements.length ?? 0;
    const allShown = d.revealed >= n;
    const fails = c?.requirements.filter((q) => q.status === "fail").length ?? 0;
    const can = !!c && c.ok && allShown && !d.sending && !!peer;
    const head = d.checking ? "Reading…" : !c ? "" : !n ? (c.problem ? "Can’t check" : "Nothing to check")
        : !allShown ? `Checking ${Math.min(d.revealed + 1, n)} of ${n}…` : fails ? `${fails} of ${n} don’t match` : `All ${n} requirements pass`;
    return html`<div class="dlg wide" role="dialog" aria-label="New task">
        <div class="dlg-body">
            <div class="dlg-title">New task</div>
            <div class="sub mt2">Kith checks the task against the peer before any file moves.</div>
            <div class="drop" data-file-drop-target>
                ${raw(icon(d.file ? "file-text" : "plus", 24))}
                <div class="grow">${d.file ? html`<div class="mono strong">${d.file.split(/[\\/]/).pop()}</div>
                    <div class="sub ell">${d.file.replace(/[\\/][^\\/]*$/, "")}${c ? ` · parsed, ${plural(c.inputs.length, "input")}` : ""}</div>`
                    : html`<div class="strong">Drop a kith.toml, or a folder with one</div><div class="sub">The task file <span class="mono">kith run</span> and <span class="mono">kith submit</span> use</div>`}</div>
                ${d.file ? html`<span class="sub">Drop another to replace</span>` : ""}
                ${btn("Choose…", "choose-file", "std", { small: true })}
            </div>
            ${d.err ? html`<div class="mt12">${infobar("error", d.err, d.errFix ?? "")}</div>` : ""}
            ${d.checking && !c ? html`<div class="row gap8 muted mt12">${raw(`<span class="spin" style="width:16px;height:16px"></span>`)}Reading the task…</div>` : ""}
            ${c ? html`<div class="fields">
                <span class="k">Command</span><span class="mono small brk">${argv(c.command)}</span>
                <span class="k">Inputs</span><span><span class="mono small">${c.inputs.slice(0, 4).map((f) => f.path).join(", ")}${c.inputs.length > 4 ? ", …" : ""}</span> <span class="muted">· ${plural(c.inputs.length, "file")}, ${bytes(c.input_bytes)}</span></span>
                ${n ? html`<span class="k">Requires</span><span class="tokens">${c.requirements.map((q) => html`<span class="token mono">${q.req}</span>`)}</span>` : ""}
                ${c.outputs?.length ? html`<span class="k">Outputs</span><span class="mono small">${c.outputs.join(", ")}</span>` : ""}
                <span class="k">Timeout</span><span>${c.timeout_minutes ? c.timeout_minutes + " min" : "none"}</span>
                <span class="k">On interrupt</span><span><span class="token mono">${c.on_interrupt || "fail"}</span></span>
                <span class="k">Send to</span><span><select class="combo sel peer-sel" data-act-change="pick-peer" aria-label="Peer">${peers().map((p) =>
                    html`<option value="${p.fp}" ${p.fp === d.peer ? raw("selected") : ""}>${p.name}${p.reachable ? " · reachable" : " · offline"}</option>`)}</select></span>
            </div>
            <div class="card preflight"><div class="pf-head"><span class="strong small grow">Preflight on ${c.peer_name ?? peer?.name ?? "peer"}</span><span class="sub">${head}</span></div>
                ${c.requirements.map((q, i) => {
                    const done = i < d.revealed;
                    return html`<div class="pf-row" id="pf-${i}">
                        <span class="q-${done ? q.status : "wait"}">${done ? raw(icon(q.status === "pass" ? "circle-check" : q.status === "fail" ? "circle-x" : "circle-minus", 16, "pop")) : raw(`<span class="spin" style="width:14px;height:14px"></span>`)}</span>
                        <span class="mono small">${q.req}</span>${raw(icon("arrow-right", 14, "faint"))}
                        <span class="mono small ${done ? "" : "faint"}">${done ? q.have : "checking…"}</span></div>`;
                })}
                ${c.problem ? html`<div class="pf-row warn small">${raw(icon("triangle-alert", 16))}<span class="grow">${c.problem}</span></div>` : ""}
                ${c.probe?.length && allShown ? html`<div class="pf-row sub">${raw(icon("terminal", 14))}<span>Probe runs on ${c.peer_name ?? "the peer"} when sent:</span><span class="mono ell">${argv(c.probe)}</span></div>` : ""}
            </div>` : ""}
        </div>
        ${foot(btn(d.sending ? "Sending…" : `Send to ${peer?.name ?? "peer"}`, "send", can ? "accent" : "accent", { disabled: !can, wide: true }), btn("Cancel", "close-dlg", "std", { wide: true }))}
    </div>`;
}

function unpair(p: { name: string; fp: string }): Raw {
    return html`<div class="dlg" role="dialog" aria-label="Unpair">
        <div class="dlg-body"><div class="dlg-title">Unpair ${p.name}?</div>
            <div class="mt10">It won’t be able to send tasks here, and ${hereWord()} forgets its identity. Pairing again needs a new code on both screens.</div>
            <div class="sub mt8">Finished tasks and their artifacts stay on ${hereWord()}.</div></div>
        ${foot(btn("Unpair", "unpair", "accent", { arg: p.fp, wide: true }), btn("Cancel", "close-dlg", "std", { wide: true }))}
    </div>`;
}

function service(): Raw {
    const who = peers()[0]?.name ?? "your other machines";
    return html`<div class="dlg" role="dialog" aria-label="Install the Kith service">
        <div class="dlg-body"><div class="dlg-title">Install the Kith service?</div>
            <div class="mt10">Kith keeps running when you sign out, so tasks from ${who} can run overnight.</div>
            <div class="bullets mt12">
                <span class="acc">${raw(icon("shield-user", 18))}</span><span>An Administrator window will open. Type your Windows password there; Kith never sees it.</span>
                <span class="acc">${raw(icon("user", 18))}</span><span>The service runs as you, ${S.info.user || "your user"}, so tasks can use your Python and GPU.</span>
                <span class="acc">${raw(icon("undo-2", 18))}</span><span>You can uninstall it here at any time.</span>
            </div></div>
        ${foot(btn("Continue", "install-service", "accent", { ic: "shield-user", wide: true }), btn("Not now", "close-dlg", "std", { wide: true }))}
    </div>`;
}

/** The error screen for a failed pairing, chosen by the daemon's error code. */
function pairErr(d: Extract<Dialog, { kind: "pair" }>): [string, string, string, string] {
    const who = d.name && d.name !== "the other machine" ? d.name : "The other machine";
    const m = (d.err ?? "").toLowerCase();
    const here = thisName().toLowerCase();
    switch (d.errCode) {
        case "MISMATCH":
            return ["error", "The codes didn’t match", "Start over on both machines and compare the new code character by character.", "Start over"];
        case "RATE_LIMITED":
            return ["caution", "Too many attempts", `${who} paused pairing after repeated failed tries. Wait up to an hour, then try again.`, "OK"];
        case "CANCELLED_BY_PEER":
            return ["caution", `Pairing was cancelled on ${who}`, `Start again on both machines when you’re ready.`, "Start over"];
        case "BUSY":
            return ["caution", `${who} is busy pairing`, `${who} is pairing with another machine. Wait for it to finish, then try again.`, "Try again"];
        case "TIMEOUT":
            if (m.includes("within 5 minutes"))
                return ["caution", "No machine connected", "Open Kith on the other machine and choose Pair a machine, then start again here.", "Start over"];
            if (here && m.includes(`answer on ${here}`))
                return ["caution", "The code expired", "Codes are valid for two minutes. Start again and confirm on both machines.", "Start over"];
            return ["caution", `${who} didn’t answer in time`, `The code expired before anyone confirmed it on ${who}. Start again on both machines.`, "Start over"];
        case "AUTH_FAILED":
            if (m.includes("rejected by the user"))
                return ["error", `${who} said the codes didn’t match`, "Start over on both machines and compare the new code character by character.", "Start over"];
            if (m.includes("pairing mode"))
                return ["caution", `${who} isn’t in pairing mode`, `Kith answered but isn’t waiting to pair. On ${who}, open Kith and choose Pair a machine, then try again.`, "Try again"];
            break;
        case "PEER_UNREACHABLE":
            if (m.includes("disconnected"))
                return ["error", `${who} disconnected`, `The connection dropped during pairing. Check that Kith is still running on ${who}, then start over.`, "Start over"];
            if (m.includes("daemon is not running"))
                break;
            return ["error", `Can’t reach ${d.target ? d.target.split(":")[0] : who}`,
                `Check that Kith is open on ${who === "The other machine" ? "it" : who} and that both machines are on the same network. If it runs Windows, its firewall must allow Kith on private networks.`, "Run diagnostics"];
    }
    return ["error", "Pairing didn’t finish", d.err ?? "", "Try again"];
}

function pair(d: Extract<Dialog, { kind: "pair" }>): Raw {
    const order = ["choose", "waiting", "compare", "paired"];
    const idx = d.step === "error" ? 2 : d.step === "confirming" ? 2 : order.indexOf(d.step);
    const steps = ["Choose", "Connect", "Compare", "Paired"].map((l, i) =>
        html`<span class="step ${i === idx ? "cur" : i < idx ? "done" : ""} ${d.step === "error" && i === idx ? "err" : ""}"><span class="step-dot"></span>${l}</span>`);
    const showLink = ["waiting", "compare", "confirming", "paired"].includes(d.step);
    const pick = d.found.find((f) => f.fp === d.pick);
    let body: Raw, primary: Raw, secondary: Raw;
    switch (d.step) {
        case "choose":
            body = html`<div class="sub mb8">Machines running Kith on this network</div>
                <div class="found">${d.found.map((f) => html`<button class="found-row ${f.fp === d.pick ? "sel" : ""} ${f.paired ? "dim" : ""}" id="f-${f.fp}" data-act="pick" data-arg="${f.fp}" ${f.paired ? raw("disabled") : ""}>
                    <span class="sel-bar"></span>${raw(icon("monitor-smartphone", 22))}
                    <span class="grow"><span class="block">${f.name}</span><span class="sub"><span class="mono">${f.addr.split(":")[0]}</span> · <span class="mono">${f.fp}</span></span></span>
                    <span class="sub">${f.paired ? "already paired" : ""}</span></button>`)}
                    <div class="found-more">${raw(`<span class="spin" style="width:14px;height:14px"></span>`)}${d.found.length ? "Looking for more…" : "Looking for machines… open Kith on the other one."}</div></div>
                <div class="row gap8 mt12"><span class="sub">Or enter an IP address</span>
                    <input class="tf mono" id="pair-ip" data-act-input="pair-ip" placeholder="10.0.0.12" value="${d.ip}" aria-label="IP address" style="width:180px">
                    <button class="link-btn" data-act="pair-wait">Wait for the other machine</button></div>`;
            primary = btn(d.ip ? `Pair with ${d.ip}` : pick ? `Pair with ${pick.name}` : "Pair", "pair-go", "accent", { disabled: !d.ip && !pick, wide: true });
            secondary = btn("Cancel", "close-dlg", "std", { wide: true });
            break;
        case "waiting": {
            const left = Math.max(0, 300 - (Date.now() - d.startedAt) / 1000);
            body = html`<div class="center-text"><div class="h18">${d.target ? `Open Kith on ${d.name} and choose Pair.` : "On your other machine, open Kith and choose Pair."}</div>
                <div class="row gap8 justify sub mt8">${raw(`<span class="spin" style="width:14px;height:14px"></span>`)}${d.target ? html`Waiting at <span class="mono">${d.target.split(":")[0]}</span>` : `Waiting as ${thisName()}`} · <span class="mono" data-keep="pair-left" data-countto="${d.startedAt + 300000}">${clock(left)}</span> left</div></div>`;
            primary = btn("Waiting…", "noop", "accent", { disabled: true, wide: true });
            secondary = btn("Cancel", "pair-reset", "std", { wide: true });
            break;
        }
        case "compare":
        case "confirming":
            body = html`<div class="center-text"><div class="code" aria-label="Pairing code ${pairCode(d.code ?? "")}">${pairCode(d.code ?? "").split("").map((ch, i) =>
                html`<span style="--i:${i}">${ch === " " ? raw("&ensp;") : ch}</span>`)}</div>
                <div class="h16 strong">Does ${d.name} show the same code?</div>
                <div class="sub mt4">Compare every character. If they differ, something else on this network is answering for it.</div>
                ${d.step === "confirming" ? html`<div class="row gap8 justify sub mt8">${raw(`<span class="spin" style="width:14px;height:14px"></span>`)}Waiting for ${d.name} to confirm…</div>`
                    : d.peerConfirmed ? html`<div class="row gap8 justify sub mt8"><span class="acc">${raw(icon("check", 16))}</span>${d.name} confirmed. Check the code, then confirm here.</div>` : ""}</div>`;
            primary = btn("They match", "pair-confirm", "accent", { arg: "1", disabled: d.step === "confirming", wide: true });
            secondary = btn("They don’t match", "pair-confirm", "std", { arg: "0", disabled: d.step === "confirming", wide: true });
            break;
        case "paired":
            body = html`<div class="center-text paired-text"><div class="h18">${d.name} is paired</div>
                <div class="sub mt4">${d.peer?.policy === "auto" ? "Tasks it sends run without asking." : "Tasks it sends will ask before they run."} You can change that in Machines.</div>
                <div class="fp-grid mt12"><span class="sub">${S.info.platform === "windows" ? "This PC" : "This machine"}</span><span class="mono small">${S.snap.status?.display ?? ""}</span>
                <span class="sub">${d.name}</span><span class="mono small">${d.peer?.display ?? ""}</span></div></div>`;
            primary = btn("Done", "close-dlg", "accent", { wide: true });
            secondary = btn("Pair another", "pair-reset", "std", { wide: true });
            break;
        default: {
            const [sev, title, msg, cta] = pairErr(d);
            body = html`${infobar(sev as "error", title, msg)}<div class="sub mt12">Nothing was paired and no keys were saved.</div>`;
            primary = btn(cta, cta === "Run diagnostics" ? "go-diag" : "pair-reset", "accent", { wide: true });
            secondary = btn("Close", "close-dlg", "std", { wide: true });
        }
    }
    return html`<div class="dlg pair" role="dialog" aria-label="Pair a machine">
        <div class="dlg-body"><div class="row gap12"><span class="dlg-title grow">Pair a machine</span>${steps}</div>
            ${showLink ? html`<div class="pair-link ${d.step}">${glyph(S.info.platform === "windows" ? "monitor" : "server", 60)}
                <div class="pl-line"><span class="pl-bar"></span>${d.step === "paired" ? raw(`<span class="pl-ring"></span><span class="pl-check">${icon("check", 14)}</span>`) : ""}</div>
                ${glyph(osIcon(d.peer?.manifest?.os), 60, d.step === "waiting")}</div>` : ""}
            <div class="pair-body">${body}</div></div>
        ${foot(primary, secondary)}
    </div>`;
}

export function dialogs(): Raw {
    const d = S.dlg;
    if (!d) return raw("");
    const inner = d.kind === "newtask" ? newTask(d) : d.kind === "unpair" ? unpair(d.peer) : d.kind === "service" ? service() : pair(d);
    return html`<div class="smoke" id="dlg-${d.kind}" data-act="smoke">${inner}</div>`;
}
