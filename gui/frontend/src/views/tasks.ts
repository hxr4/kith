import { html, raw, type Raw } from "../core/html";
import { S, isSent, needsApproval, peerLabel, tasks } from "../core/store";
import { ago, date } from "../core/fmt";
import type { Task } from "../core/api";
import { chip, pivots } from "../ui/parts";
import { icon } from "../ui/icons";

export function filtered(): Task[] {
    const q = S.q.trim().toLowerCase();
    return tasks().filter((t) =>
        (S.filter === "all" || (S.filter === "s") === isSent(t)) &&
        (!q || `${t.name} ${t.project} ${peerLabel(t)} ${t.state} ${t.id}`.toLowerCase().includes(q)));
}

function row(t: Task, i: number): Raw {
    const open = needsApproval(t) ? "#/approvals" : "#/tasks/" + encodeURIComponent(t.id);
    const menu = S.ctx === t.id;
    const dir = isSent(t) ? t.project_dir : t.ws_dir;
    return html`<div class="trow ${menu ? "sel" : ""}" id="row-${t.id}" style="--i:${Math.min(i, 9)}" role="button" tabindex="0" data-act="go" data-arg="${open}" data-ctx="${t.id}">
        ${chip(t.state)}
        <span class="ell">${t.name}</span>
        <span class="ell muted">${t.project || "—"}</span>
        <span class="ell muted peer">${raw(icon(isSent(t) ? "arrow-up-right" : "arrow-down-left", 14, "faint"))}${peerLabel(t)}</span>
        <span class="mono muted">${t.attempt}</span>
        <span class="muted r">${ago(date(t.updated_at))}</span>
        ${menu ? html`<div class="menu ctx" role="menu">
            ${needsApproval(t) || !["COMPLETED", "FAILED", "CANCELLED", "REJECTED"].includes(t.state)
                ? html`<button class="mi" data-act="cancel-task" data-arg="${t.id}">${raw(icon("circle-x", 16))}Cancel</button>` : ""}
            <button class="mi" data-act="copy" data-arg="${t.id}" data-label="task ID">${raw(icon("copy", 16))}<span class="grow">Copy ID</span><span class="mono faint tiny">${t.id.slice(-8)}</span></button>
            ${dir ? html`<div class="msep"></div><button class="mi" data-act="reveal" data-arg="${dir}">${raw(icon("folder-open", 16))}${isSent(t) ? "Open project folder" : "Open workspace folder"}</button>` : ""}
        </div>` : ""}
    </div>`;
}

export function tasksView(): Raw {
    const all = tasks();
    const rows = filtered();
    const q = S.q.trim();
    return html`<div class="page" id="page-tasks">
        ${pivots([["all", "All", all.length], ["r", "Received", all.filter((t) => !isSent(t)).length], ["s", "Sent", all.filter(isSent).length]], S.filter, "filter")}
        <div class="card table">
            <div class="thead"><span>State</span><span>Name</span><span>Project</span><span>Peer</span><span>Attempt</span><span class="r">Updated</span></div>
            ${rows.map(row)}
            ${!rows.length ? html`<div class="empty-row">${q ? `No tasks match “${q}”.` : html`No tasks yet. Send one with New task, ${raw("<span class=mono>kith run</span>")}, or from an AI agent over MCP.`}</div>` : ""}
        </div>
    </div>`;
}
