import { html, raw, type Raw } from "../core/html";
import { S, hereWord, peers, thisName } from "../core/store";
import { btn, infobar, kmark, pivots, seg, toggle } from "../ui/parts";
import { icon } from "../ui/icons";

function row(ic: string, title: Raw | string, sub: Raw | string, trailing: Raw | string, id: string): Raw {
    return html`<div class="srow" id="${id}"><span class="srow-ic">${raw(icon(ic, 20))}</span>
        <div class="grow"><div>${title}</div>${sub ? html`<div class="sub">${sub}</div>` : ""}</div>${trailing}</div>`;
}

const isWin = () => S.info.platform === "windows";

function general(): Raw {
    const themes: [string, string][] = [["system", "Use system setting"], ["light", "Light"], ["dark", "Dark"]];
    const tl = themes.find(([v]) => v === S.theme)?.[1] ?? themes[0][1];
    return html`
        ${row("id-card", "Device name", `How peers see ${hereWord()}`, html`<input class="tf" id="cfg-name" data-cfg="name" value="${S.config.name ?? thisName()}" aria-label="Device name">`, "s-name")}
        ${row("log-in", isWin() ? "Start Kith when I sign in" : "Open Kith at login",
            isWin() ? "Opens in the tray only. Tasks stop when you sign out unless the background service is installed." : "Opens quietly in the tray.",
            html`<span class="muted">${S.autostart ? "On" : "Off"}</span>${toggle(S.autostart, "autostart", "Start at sign-in")}`, "s-login")}
        ${row("hand", "Default policy for new peers", S.config.default_policy === "auto" ? "New peers run tasks without asking." : "You approve each task a new peer sends.",
            seg([["ask", "Ask"], ["auto", "Auto"]], S.config.default_policy ?? "ask", "cfg:default_policy"), "s-policy")}
        ${row("palette", "Theme", "", html`<div class="combo-wrap"><button class="combo" data-act="theme-open">${tl}${raw(icon("chevron-down", 16))}</button>
            ${S.themeOpen ? html`<div class="menu combo-menu">${themes.map(([v, l]) => html`<button class="mi ${v === S.theme ? "sel" : ""}" data-act="theme" data-arg="${v}"><span class="sel-bar"></span>${l}</button>`)}</div>` : ""}</div>`, "s-theme")}`;
}

function service(): Raw {
    const st = S.service || "unknown";
    const installed = /^installed/i.test(st);
    const running = S.snap.running;
    const who = peers()[0]?.name ?? "your other machines";
    const svcName = isWin() ? "a Windows service" : S.info.platform === "linux" ? "a systemd user service" : "a LaunchAgent";
    return html`
        ${S.svcOk ? html`<div class="mb8 fade-in">${infobar("success", "Kith service installed and running.", `Tasks keep running after you sign out, as ${S.info.user || "you"}.`, undefined, "svc-ok-close")}</div>` : ""}
        ${row("server", html`<span class="row gap8">${raw(`<span class="dot ${running ? "green" : "off"}"></span>`)}${running ? "Running" : "Stopped"}</span>`,
            html`<span class="mono">${running && S.snap.status ? `kithd ${S.snap.status.version} · pid ${S.snap.status.pid} · ${S.config.bind || "0.0.0.0"}:${S.snap.status.port}` : "peers can’t reach " + hereWord()} · service: ${st}</span>`,
            html`${btn("Start", "daemon", "std", { arg: "start", disabled: running || !!S.busy.daemon })}${btn("Stop", "daemon", "std", { arg: "stop", disabled: !running || !!S.busy.daemon })}${btn("Restart", "daemon", "std", { arg: "restart", disabled: !running || !!S.busy.daemon })}`, "s-daemon")}
        ${row("shield-user", `Install as ${svcName}`, isWin() ? `Runs Kith when nobody is signed in, so tasks from ${who} can run overnight. Needs an Administrator once.` : "Starts Kith at boot, even before you sign in.",
            S.svcWaiting ? html`<span class="row gap8 muted small">${raw(`<span class="spin" style="width:16px;height:16px"></span>`)}${isWin() ? "Waiting for the Administrator window…" : "Installing…"}</span>`
                : installed ? html`<span class="row gap6 green small">${raw(icon("circle-check", 18))}Installed</span>`
                    : btn("Install service…", "ask-service", "accent"), "s-install")}
        ${row("trash-2", "Uninstall service", `Kith goes back to running only while you’re signed in.`, btn("Uninstall", "daemon", "std", { arg: "uninstall", disabled: !installed || !!S.busy.daemon }), "s-uninstall")}`;
}

function network(): Raw {
    const fw = (S.doctor ?? []).find((c) => c.name === "firewall");
    const fwOk = !fw || fw.status === "ok";
    return html`
        ${row("network", "TCP port", "", html`<input class="tf mono r" style="width:100px" data-cfg="port" value="${S.config.port ?? "7420"}" aria-label="TCP port">`, "s-port")}
        ${row("router", "Bind address", "0.0.0.0 listens on all adapters", html`<input class="tf mono" style="width:180px" data-cfg="bind" value="${S.config.bind ?? "0.0.0.0"}" aria-label="Bind address">`, "s-bind")}
        ${row("radar", "Discover peers with mDNS", html`Advertises <span class="mono">_kith._tcp</span> on UDP 5353`,
            html`<span class="muted">${S.config.mdns !== "false" ? "On" : "Off"}</span>${toggle(S.config.mdns !== "false", "cfg-toggle:mdns", "mDNS discovery")}`, "s-mdns")}
        ${isWin() ? row("flame", html`<span class="row gap8">${raw(`<span class="dot ${fwOk ? "green" : "orange"}"></span>`)}${fwOk ? "Firewall rules present" : "Firewall rules missing"}</span>`,
            fwOk ? "Inbound TCP 7420 and mDNS UDP 5353 are allowed on Private networks" : "Peers can’t reach TCP 7420 or discover this PC on UDP 5353",
            fwOk ? "" : btn(S.busy.firewall ? "Waiting…" : "Add firewall rules", "firewall", "accent", { ic: "shield-user", disabled: !!S.busy.firewall }), "s-fw") : ""}
        ${S.configDirty ? html`<div class="mt8">${infobar("info", "Restart the daemon to apply.", "Running tasks become INTERRUPTED and resume by their policy.", btn("Restart now", "daemon", "std", { arg: "restart" }))}</div>` : ""}`;
}

function logs(): Raw {
    return html`
        ${row("sliders-horizontal", "Log level", "debug includes every frame header; use it only while troubleshooting",
            html`<select class="combo sel" data-cfg="log_level" aria-label="Log level">${["debug", "info", "warn", "error"].map((l) => html`<option value="${l}" ${(S.config.log_level ?? "info") === l ? raw("selected") : ""}>${l}</option>`)}</select>`, "s-level")}
        ${row("file-text", "Daemon log", html`<span class="mono">${S.info.log_path}</span>`, btn("Open daemon log", "open-path", "std", { arg: S.info.log_path }), "s-log")}
        ${S.configDirty ? html`<div class="mt8">${infobar("info", "Restart the daemon to apply.", "", btn("Restart now", "daemon", "std", { arg: "restart" }))}</div>` : ""}`;
}

function cli(): Raw {
    const c = S.cli;
    const snippet = `{\n  "mcpServers": {\n    "kith": {\n      "command": "kith",\n      "args": ["mcp"]\n    }\n  }\n}`;
    return html`
        ${row(c?.on_path ? "circle-check" : "circle-alert", c?.on_path ? "kith is on your PATH" : "kith isn’t on your PATH",
            html`<span class="mono">${c?.path ?? ""}</span>${c?.on_path && isWin() ? " · open a new terminal to use it" : ""}`,
            c && !c.on_path && isWin() ? btn("Add to PATH", "add-path", "accent") : "", "s-cli")}
        <div class="srow col" id="s-mcp"><div class="row gap12 w100"><div class="grow"><div>Use Kith from an AI agent</div>
            <div class="sub">Add this to the agent’s MCP config. Tasks it sends still follow each peer’s Ask or Auto policy.</div></div>
            ${btn("Copy", "copy", "std", { arg: snippet, ic: "copy" })}</div>
            <pre class="snippet mono">${snippet}</pre></div>`;
}

function about(): Raw {
    const st = S.snap.status;
    return html`
        <div class="srow" id="s-ver"><span class="app-ic">${kmark("white")}</span><div class="grow"><div class="strong">Kith</div>
            <div class="sub">Version ${S.info.version || "dev"}${st && st.version !== S.info.version ? ` · kithd ${st.version}` : ""}</div></div></div>
        ${row("fingerprint", `${hereWord()[0].toUpperCase() + hereWord().slice(1)}’s fingerprint`, "",
            st ? html`<span class="mono">${st.display}</span><button class="ibtn" data-act="copy" data-arg="${st.display}" data-label="fingerprint" title="Copy">${raw(icon("copy", 16))}</button>` : html`<span class="muted">start the daemon to show it</span>`, "s-fp")}
        ${row("folder", "Data folder", html`<span class="mono">${S.info.home}</span>`, btn(isWin() ? "Open in Explorer" : "Open folder", "reveal", "std", { arg: S.info.home }), "s-home")}
        ${row("info", "", html`Kith does not sandbox tasks. A task you approve runs as ${S.info.user || "you"} with your files, your Python and your GPU, exactly as if you had typed the command yourself.`, "", "s-note")}`;
}

export function settingsView(): Raw {
    const tabs: [string, string][] = [["general", "General"], ["service", "Background service"], ["network", "Network"], ["logs", "Logs"], ["cli", "Command line"], ["about", "About"]];
    const body = { general, service, network, logs, cli, about }[S.sgTab as "general"] ?? general;
    return html`<div class="page narrow" id="page-settings">
        ${pivots(tabs, S.sgTab, "sg")}
        <div class="sgroup" id="sg-${S.sgTab}">${body()}</div>
    </div>`;
}
