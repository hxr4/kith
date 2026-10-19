import "./style.css";
import { Window } from "@wailsio/runtime";
import { patch, setRolling } from "./core/html";
import {
    S, addFirewall, boot, cancelTask, checkTask, chooseTaskFile, closeDialog, confirmPair, copy, daemon, decide, fail, fetchArtifact,
    flash, go, installService, loadSettings, onRender, openNewTask, openPair, peerOf, probe, refresh, runDoctor, sendTask, setConfig,
    setPolicy, setTheme, startPair, tasks, unpair, update,
} from "./core/store";
import { api } from "./core/api";
import { clock } from "./core/fmt";
import { shell } from "./views/shell";
import { flyoutView } from "./views/flyout";
import { mountConsole } from "./views/detail";

const root = document.getElementById("app")!;
let lastRoute = "";
const isFlyout = () => S.route.name === "flyout";

// ---- render ----

function render() {
    document.documentElement.classList.toggle("lite", S.info.lite);
    document.documentElement.classList.toggle("mica", S.info.mica);
    document.documentElement.dataset.chrome = S.info.chrome;
    if (isFlyout()) {
        document.documentElement.classList.add("flyout");
        patch(root, flyoutView());
        const fly = document.getElementById("fly");
        if (fly) api.fitFlyout(Math.ceil(fly.getBoundingClientRect().height)).catch(() => {});
    } else {
        // "#/new" and "#/pair" come from the tray flyout. They open the dialog
        // only when it isn't already open: a pairing in progress keeps its state.
        if (S.route.name === "new") { history.replaceState(null, "", "#/tasks"); S.route = { name: "tasks", param: "" }; if (S.dlg?.kind !== "newtask") openNewTask(); }
        if (S.route.name === "pair") { history.replaceState(null, "", "#/machines"); S.route = { name: "machines", param: "" }; if (S.dlg?.kind !== "pair") openPair(); }
        patch(root, shell());
        // A new screen starts at the top.
        const key = S.route.name + "/" + S.route.param;
        if (key !== lastRoute) { lastRoute = key; document.getElementById("scroll")?.scrollTo(0, 0); }
        placePill();
        mountConsole();
    }
    tick();
}

function placePill() {
    const pill = document.getElementById("nav-pill");
    const on = document.querySelector<HTMLElement>(".nav-i.on");
    if (!pill || !on) return;
    pill.style.transform = `translateY(${on.offsetTop + 10}px)`;
    pill.style.opacity = "1";
}

// ---- live counters (no re-render) ----

function tick() {
    const now = Date.now();
    document.querySelectorAll<HTMLElement>("[data-since]").forEach((el) => {
        const s = Date.parse(el.dataset.since || "");
        if (isNaN(s)) return;
        const u = Date.parse(el.dataset.until || "");
        setRolling(el, clock(((isNaN(u) ? now : u) - s) / 1000));
    });
    document.querySelectorAll<HTMLElement>("[data-countdown]").forEach((el) => {
        const c = Date.parse(el.dataset.countdown || "");
        const ttl = Number(el.dataset.ttl || 900);
        const left = isNaN(c) ? ttl : Math.max(0, ttl - (now - c) / 1000);
        el.classList.toggle("warn", left <= 60);
        const lab = el.querySelector<HTMLElement>(".cd-left");
        if (lab) setRolling(lab, clock(left) + " left");
        const arc = el.querySelector<SVGCircleElement>(".cd-arc");
        if (arc) arc.style.strokeDashoffset = String((50.27 * (1 - left / ttl)).toFixed(2));
    });
    document.querySelectorAll<HTMLElement>("[data-countto]").forEach((el) => {
        el.textContent = clock(Math.max(0, (Number(el.dataset.countto) - now) / 1000));
    });
}
setInterval(tick, 250);

// ---- actions ----

type Handler = (arg: string, el: HTMLElement, e: Event) => unknown;

const A: Record<string, Handler> = {
    go: (a) => go(a),
    noop: () => {},
    pair: () => openPair(),
    "new-task": () => openNewTask(),
    filter: (a) => update({ filter: a as "all" | "r" | "s" }),
    sg: (a) => { S.sgTab = a; go("#/settings/" + a); update(); },
    copy: (a, el) => copy(a, el.dataset.label ?? "text"),
    reveal: (a) => api.reveal(a).catch(fail),
    "open-path": (a) => api.openPath(a).catch(fail),
    probe: () => probe(),
    refresh: async () => { S.busy.refresh = true; update(); await refresh(); await probe(); delete S.busy.refresh; flash("Peers are up to date"); },
    "ask-unpair": (a) => { const p = peerOf(a); if (p) update({ dlg: { kind: "unpair", peer: p } }); },
    unpair: async (a) => { const p = peerOf(a); update({ dlg: null }); if (p) await unpair(p); },
    approve: (a) => { const t = tasks().find((x) => x.id === a); if (t) decide(t, true); },
    deny: (a) => { const t = tasks().find((x) => x.id === a); if (t) decide(t, false); },
    "toggle-open": (a) => { S.open[a] = !S.open[a]; update(); },
    "ask-cancel": (a) => update({ confirmCancel: a && S.confirmCancel !== a ? a : null }),
    "cancel-task": (a) => { const t = tasks().find((x) => x.id === a); update({ confirmCancel: null, ctx: null }); if (t) cancelTask(t); },
    fetch: (a, el) => { const t = tasks().find((x) => x.id === el.dataset.task); if (t) fetchArtifact(t, a); },
    firewall: () => addFirewall(),
    "svc-ok-close": () => update({ svcOk: false }),
    "ask-service": () => (S.info.platform === "windows" ? update({ dlg: { kind: "service" } }) : installService()),
    "install-service": () => installService(),
    daemon: (a) => daemon(a),
    autostart: async () => { try { await api.setAutostart(!S.autostart); loadSettings(); } catch (e) { fail(e); } },
    "cfg-toggle": () => {},
    "theme-open": () => update({ themeOpen: !S.themeOpen }),
    theme: (a) => setTheme(a as "system"),
    "add-path": async () => { try { flash(await api.addToPath()); loadSettings(); } catch (e) { fail(e); } },
    "choose-file": () => chooseTaskFile(),
    send: () => sendTask(),
    "close-dlg": () => closeDialog(),
    smoke: (_a, el, e) => { if (e.target === el) closeDialog(); },
    pick: (a) => { const d = S.dlg; if (d?.kind === "pair") { d.pick = a; d.ip = ""; update(); } },
    "pair-go": () => {
        const d = S.dlg;
        if (d?.kind !== "pair") return;
        if (d.ip.trim()) { startPair(d.ip.trim(), d.ip.trim()); return; }
        const f = d.found.find((x) => x.fp === d.pick);
        if (f) startPair(f.addr, f.name);
    },
    "pair-wait": () => startPair("", "the other machine"),
    "pair-confirm": (a) => confirmPair(a === "1"),
    "pair-reset": () => { api.cancelPairing().catch(() => {}); openPair(); },
    "go-diag": () => { closeDialog(); go("#/diagnostics"); runDoctor(); },
    "run-checks": () => runDoctor(),
    "win-min": () => Window.Minimise(),
    "win-max": () => Window.ToggleMaximise(),
    "win-close": () => Window.Close(),
    "open-main": (a) => { S.open["fly-menu"] = false; api.showMain(a).catch(fail); },
    quit: () => api.quit(),
};

document.addEventListener("click", (e) => {
    const el = (e.target as HTMLElement).closest<HTMLElement>("[data-act]");
    // Clicks outside a context menu, flyout menu or combo close them.
    if (!(e.target as HTMLElement).closest(".menu")) {
        let changed = false;
        if (S.ctx) { S.ctx = null; changed = true; }
        if (S.themeOpen && el?.dataset.act !== "theme-open") { S.themeOpen = false; changed = true; }
        if (S.confirmCancel && el?.dataset.act !== "ask-cancel") { S.confirmCancel = null; changed = true; }
        if (S.open["fly-menu"] && el?.dataset.act !== "toggle-open") { S.open["fly-menu"] = false; changed = true; }
        if (changed) update();
    }
    if (!el || (el as HTMLButtonElement).disabled) return;
    const act = el.dataset.act!;
    const arg = el.dataset.arg ?? "";
    if (act.startsWith("policy:")) { const p = peerOf(act.slice(7)); if (p && p.policy !== arg) setPolicy(p, arg); return; }
    if (act.startsWith("cfg:")) { setConfig(act.slice(4), arg); return; }
    if (act.startsWith("cfg-toggle:")) { const k = act.slice(11); setConfig(k, S.config[k] === "false" ? "true" : "false"); return; }
    if (act.startsWith("ib-close:")) { S.ibClosed[act.slice(9)] = true; update(); return; }
    const h = A[act];
    if (h) { e.preventDefault(); h(arg, el, e); }
});

document.addEventListener("contextmenu", (e) => {
    const row = (e.target as HTMLElement).closest<HTMLElement>("[data-ctx]");
    if (!row) return;
    e.preventDefault();
    update({ ctx: S.ctx === row.dataset.ctx ? null : row.dataset.ctx! });
});

document.addEventListener("input", (e) => {
    const t = e.target as HTMLInputElement;
    if (t.id === "q") {
        S.q = t.value;
        if (S.route.name !== "tasks" || S.route.param) go("#/tasks");
        update();
    }
    if (t.dataset.actInput === "pair-ip" && S.dlg?.kind === "pair") { S.dlg.ip = t.value; if (t.value) S.dlg.pick = undefined; update(); }
});

document.addEventListener("change", (e) => {
    const t = e.target as HTMLInputElement | HTMLSelectElement;
    if (t.dataset.cfg) setConfig(t.dataset.cfg, t.value.trim());
    if (t.dataset.actChange === "pick-peer" && S.dlg?.kind === "newtask") {
        S.dlg.peer = t.value;
        if (S.dlg.file) checkTask(S.dlg.file); else update();
    }
});

document.addEventListener("keydown", (e) => {
    const t = e.target as HTMLElement;
    if (e.key === "Escape") {
        if (S.dlg) { closeDialog(); return; }
        if (S.ctx || S.confirmCancel || S.themeOpen) { update({ ctx: null, confirmCancel: null, themeOpen: false }); return; }
        if (isFlyout()) api.hideFlyout();
    }
    if (e.key === "Enter" && t.tagName === "INPUT" && (t as HTMLInputElement).dataset.cfg) (t as HTMLInputElement).blur();
    if (e.key === "Enter" && (t.dataset?.act) && t.getAttribute("role") === "button") t.click();
    if (e.key === "Enter" && t.id === "pair-ip") A["pair-go"]("", t, e);
    const mod = e.ctrlKey || e.metaKey;
    if (mod && e.key.toLowerCase() === "f" && !isFlyout()) { e.preventDefault(); document.getElementById("q")?.focus(); }
    if (mod && e.key.toLowerCase() === "n" && !isFlyout()) { e.preventDefault(); openNewTask(); }
    if (mod && e.key === "Enter" && S.dlg?.kind === "newtask") sendTask();
});

window.addEventListener("resize", placePill);

onRender(render);
(async () => {
    // Development only: `npm run dev` + ?mock runs the UI on sample data in a browser.
    if (import.meta.env.DEV && new URLSearchParams(location.search).has("mock")) (await import("./dev/mock")).install();
    await boot();
    update();
})();
