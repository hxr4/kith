// Small DOM helpers. Every dynamic string goes through esc(): task names,
// details and peer names come from the other machine and are untrusted.

export function esc(v: unknown): string {
    return String(v ?? "").replace(/[&<>"']/g, (c) =>
        ({"&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"}[c]!));
}

export const $ = <T extends HTMLElement = HTMLElement>(sel: string, root: ParentNode = document) =>
    root.querySelector(sel) as T | null;

export function stateClass(s: string): string {
    switch (s) {
        case "COMPLETED": return "ok";
        case "FAILED": case "REJECTED": return "bad";
        case "CANCELLED": case "INTERRUPTED": case "CANCELLING": return "warn";
        case "RUNNING": case "TRANSFERRING": case "FINALIZING": return "live";
        default: return "idle";
    }
}

export function stateBadge(s: string, pending = false): string {
    const label = pending ? "NEEDS APPROVAL" : s;
    return `<span class="state ${pending ? "warn" : stateClass(s)}">${esc(label)}</span>`;
}

export function ago(ts?: string): string {
    if (!ts) return "";
    const d = (Date.now() - new Date(ts).getTime()) / 1000;
    if (isNaN(d)) return "";
    if (d < 45) return "just now";
    if (d < 3600) return `${Math.round(d / 60)} min ago`;
    if (d < 86400) return `${Math.round(d / 3600)} h ago`;
    return new Date(ts).toLocaleDateString();
}

export function clock(ts: string): string {
    const t = new Date(ts);
    return isNaN(t.getTime()) ? ts : t.toLocaleTimeString();
}

export function bytes(n: number): string {
    if (n < 1024) return `${n} B`;
    const u = ["KB", "MB", "GB", "TB"];
    let v = n / 1024, i = 0;
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return `${v.toFixed(v < 10 ? 1 : 0)} ${u[i]}`;
}

export function toast(msg: string, kind: "ok" | "bad" | "info" = "info") {
    const host = $("#toasts")!;
    const el = document.createElement("div");
    el.className = `toast ${kind}`;
    el.textContent = msg;
    host.appendChild(el);
    setTimeout(() => el.remove(), kind === "bad" ? 9000 : 4500);
}

// confirmBox resolves true if the user confirms (in-page; native confirm()
// is not available in every webview).
export function confirmBox(title: string, body: string, ok = "Confirm", danger = false): Promise<boolean> {
    const m = $("#modal")!;
    m.innerHTML = `<div class="modal" role="dialog" aria-modal="true">
        <h3>${esc(title)}</h3><p>${esc(body)}</p>
        <div class="row end"><button class="btn" data-a="no">Cancel</button>
        <button class="btn ${danger ? "danger" : "primary"}" data-a="yes">${esc(ok)}</button></div></div>`;
    m.hidden = false;
    return new Promise((resolve) => {
        const done = (v: boolean) => { m.hidden = true; m.innerHTML = ""; resolve(v); };
        m.onclick = (e) => {
            const a = (e.target as HTMLElement).dataset.a;
            if (a === "yes") done(true);
            else if (a === "no" || e.target === m) done(false);
        };
    });
}

// busy disables a button while an action runs.
export async function busy<T>(btn: HTMLButtonElement | null, fn: () => Promise<T>): Promise<T | undefined> {
    if (btn) { btn.disabled = true; btn.classList.add("busy"); }
    try { return await fn(); } finally { if (btn) { btn.disabled = false; btn.classList.remove("busy"); } }
}
