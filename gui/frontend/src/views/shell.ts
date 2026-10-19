import { html, raw, type Raw } from "../core/html";
import { S, pending, peers, tasks } from "../core/store";
import { btn, infobar, kmark } from "../ui/parts";
import { icon } from "../ui/icons";
import { overview } from "./overview";
import { tasksView } from "./tasks";
import { detailView } from "./detail";
import { approvalsView } from "./approvals";
import { machinesView, diagnosticsView } from "./machines";
import { settingsView } from "./settings";
import { dialogs } from "./dialogs";

const NAV: [string, string, string][] = [
    ["overview", "Overview", "house"], ["tasks", "Tasks", "list"], ["approvals", "Approvals", "hand"],
    ["machines", "Machines", "monitor-smartphone"], ["diagnostics", "Diagnostics", "activity"],
];

function group(): string {
    const n = S.route.name;
    return n === "settings" ? "settings" : NAV.some(([id]) => id === n) ? n : "overview";
}

function titlebar(): Raw {
    const search = html`<label class="search">${raw(icon("search", 16))}<input id="q" type="search" placeholder="Search tasks" value="${S.q}" aria-label="Search tasks" autocomplete="off"></label>`;
    if (S.info.chrome === "windows") {
        return html`<header class="titlebar">
            <div class="tb-brand">${kmark()}<span>Kith</span></div>
            ${search}
            <div class="caption">
                <button class="cap min" data-act="win-min" aria-label="Minimize">${raw(icon("minus", 16))}</button>
                <button class="cap max" data-act="win-max" aria-label="Maximize">${raw(icon("square", 13))}</button>
                <button class="cap close" data-act="win-close" aria-label="Close">${raw(icon("x", 17))}</button>
            </div></header>`;
    }
    // Linux header bar (native decorations stay; this is the app's toolbar).
    return html`<header class="headerbar ${S.info.chrome === "mac" ? "mac" : ""}">
        <div class="hb-left">${search}</div>
        <div class="hb-title"><div class="strong">Kith</div><div class="sub">${title()}</div></div>
        <div class="hb-right"></div></header>`;
}

function title(): string {
    const r = S.route;
    if (r.name === "tasks" && r.param) return tasks().find((t) => t.id === r.param)?.name ?? "Task";
    return ({ overview: "Overview", tasks: "Tasks", approvals: "Approvals", machines: "Machines", diagnostics: "Diagnostics", settings: "Settings" } as Record<string, string>)[group()];
}

function nav(): Raw {
    const g = group();
    const item = ([id, label, ic]: [string, string, string]) => html`<button class="nav-i ${g === id ? "on" : ""}" id="nav-${id}" data-act="go" data-arg="#/${id}" aria-current="${g === id ? "page" : "false"}">
        ${raw(icon(ic, 20))}<span class="grow">${label}</span>
        ${id === "approvals" && pending().length ? html`<span class="badge">${pending().length}</span>` : ""}</button>`;
    return html`<nav class="nav" aria-label="Sections"><span class="nav-pill" id="nav-pill"></span>
        ${NAV.map(item)}<span class="grow"></span>${item(["settings", "Settings", "settings"])}</nav>`;
}

function pageHead(): Raw {
    const r = S.route;
    const detail = r.name === "tasks" && r.param;
    const action = r.name === "tasks" && !detail ? btn("New task", "new-task", "accent", { ic: "plus" })
        : r.name === "machines" ? btn("Pair a machine", "pair", "accent", { ic: "link-2" })
            : r.name === "diagnostics" ? btn("Run checks", "run-checks", "accent", { disabled: S.doctorRunning }) : "";
    return html`<div class="phead">
        ${detail ? html`<button class="crumb" data-act="go" data-arg="#/tasks">Tasks</button>${raw(icon("chevron-right", 20, "faint"))}` : ""}
        <h1 class="ptitle">${title()}</h1><span class="grow"></span>${action}</div>`;
}

function page(): Raw {
    const r = S.route;
    switch (group()) {
        case "tasks": return r.param ? detailView(r.param) : tasksView();
        case "approvals": return approvalsView();
        case "machines": return machinesView();
        case "diagnostics": return diagnosticsView();
        case "settings": return settingsView();
        default: return overview();
    }
}

function daemonBar(): Raw {
    if (!S.loaded || S.snap.running) return raw("");
    const who = peers()[0]?.name ?? "Your other machines";
    return html`<div class="mb16">${infobar("caution", "Kith isn’t running.", `${who} can’t reach this machine, and nothing below updates.`,
        btn(S.busy.daemon ? "Starting…" : "Start Kith", "daemon", "std", { arg: "start", disabled: !!S.busy.daemon }))}</div>`;
}

/** Pi lite mode: one column with a top tab bar. */
function liteTabs(): Raw {
    const g = group();
    const tabs: [string, string, string][] = [...NAV.slice(0, 4), ["settings", "Settings", "settings"]];
    return html`<header class="litebar"><span class="strong lite-brand">${kmark()}Kith</span>
        ${tabs.map(([id, label, ic]) => html`<button class="lite-tab ${g === id ? "on" : ""}" data-act="go" data-arg="#/${id}">${raw(icon(ic, 20))}${label}
            ${id === "approvals" && pending().length ? html`<span class="badge">${pending().length}</span>` : ""}</button>`)}</header>`;
}

export function shell(): Raw {
    const lite = S.info.lite;
    return html`<div class="win ${lite ? "lite" : ""}" id="win">
        ${lite ? liteTabs() : titlebar()}
        <div class="body">
            ${lite ? "" : nav()}
            <main class="content">
                ${lite ? "" : pageHead()}
                <div class="scroll" id="scroll">${daemonBar()}${page()}</div>
            </main>
        </div>
        <div id="layer">${dialogs()}</div>
        ${S.toast ? html`<div class="toast ${S.toast.error ? "err" : ""}" id="toast-${S.toast.id}" role="status">${raw(icon(S.toast.error ? "triangle-alert" : "circle-check", 18))}<span>${S.toast.text}</span></div>` : ""}
    </div>`;
}
