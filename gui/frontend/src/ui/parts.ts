// Shared pieces from the design's component sheet.
import { html, raw, type Raw } from "../core/html";
import { icon } from "./icons";

const ENDED_ICON: Record<string, string> = { COMPLETED: "check", FAILED: "x", CANCELLED: "minus", REJECTED: "ban" };
const LIVE = ["TRANSFERRING", "RUNNING", "CANCELLING"];
const RING = ["SUBMITTED", "ACCEPTED"];

/** State chip. In progress: a leading dot (live states pulse). Ended: a glyph, popping once. */
export function chip(state: string, big = false): Raw {
    const ended = ENDED_ICON[state];
    const lead = ended
        ? raw(icon(ended, big ? 12 : 11, "chip-ic"))
        : html`<span class="chip-dot ${RING.includes(state) ? "ring" : ""} ${LIVE.includes(state) ? "k-pulse" : ""}"></span>`;
    return html`<span class="chip s-${state.toLowerCase()} ${big ? "big" : ""} ${ended ? "ended" : ""}">${lead}${state}</span>`;
}

export type LinkMode = "off" | "idle" | "flow" | "back";

/** The link: solid when connected, dashed when offline; pulses travel with the data. */
export function link(mode: LinkMode, label?: string): Raw {
    return html`<div class="k-link ${mode}" ${label ? raw(`aria-label="${label}"`) : ""}>
        <span class="k-line solid"></span><span class="k-line dashed"></span>
        <span class="k-flow ${mode === "back" ? "rev" : ""}"><i></i><i></i><i></i></span>
    </div>`;
}

/** Countdown ring (SVG); data-countdown lets the ticker update it live. */
export function ring(createdISO: string | undefined, ttlSec = 900, size = 20): Raw {
    return html`<span class="cd" data-keep="cd-${createdISO}" data-countdown="${createdISO ?? ""}" data-ttl="${ttlSec}">
        <span class="cd-left"></span>
        <svg width="${size}" height="${size}" viewBox="0 0 20 20"><circle cx="10" cy="10" r="8" class="cd-track"/><circle cx="10" cy="10" r="8" class="cd-arc"/></svg>
    </span>`;
}

/** Elapsed clock (ticker-updated, digits roll). */
export function elapsed(sinceISO?: string, untilISO?: string): Raw {
    return html`<span class="el mono" data-keep="el-${sinceISO}-${untilISO ?? ""}" data-since="${sinceISO ?? ""}" data-until="${untilISO ?? ""}"></span>`;
}

export function btn(label: Raw | string, act: string, kind: "accent" | "std" | "danger" | "subtle" = "std", o: { arg?: string; disabled?: boolean; small?: boolean; wide?: boolean; ic?: string; title?: string } = {}): Raw {
    return html`<button class="btn ${kind} ${o.small ? "small" : ""} ${o.wide ? "wide" : ""}" data-act="${act}" ${o.arg !== undefined ? raw(`data-arg="${escAttr(o.arg)}"`) : ""} ${o.disabled ? raw("disabled") : ""} ${o.title ? raw(`title="${escAttr(o.title)}"`) : ""}>${o.ic ? raw(icon(o.ic, 16)) : ""}${label}</button>`;
}

export function iconBtn(ic: string, act: string, title: string, o: { arg?: string; on?: boolean } = {}): Raw {
    return html`<button class="ibtn ${o.on ? "on" : ""}" data-act="${act}" ${o.arg !== undefined ? raw(`data-arg="${escAttr(o.arg)}"`) : ""} title="${title}" aria-label="${title}">${raw(icon(ic, 16))}</button>`;
}

export function toggle(on: boolean, act: string, label: string, disabled = false): Raw {
    return html`<button class="toggle ${on ? "on" : ""}" role="switch" aria-checked="${on}" aria-label="${label}" data-act="${act}" ${disabled ? raw("disabled") : ""}><span class="knob"></span></button>`;
}

export function seg(opts: [string, string][], value: string, act: string): Raw {
    return html`<div class="seg" role="radiogroup">${opts.map(([v, l]) =>
        html`<button class="seg-i ${v === value ? "on" : ""}" role="radio" aria-checked="${v === value}" data-act="${act}" data-arg="${v}">${l}</button>`)}</div>`;
}

export function pivots(opts: [string, string, number?][], value: string, act: string): Raw {
    return html`<div class="pivots" role="tablist">${opts.map(([v, l, n]) =>
        html`<button class="pivot ${v === value ? "on" : ""}" role="tab" aria-selected="${v === value}" data-act="${act}" data-arg="${v}">${l}${n !== undefined ? html`<span class="pivot-n">${n}</span>` : ""}<span class="pivot-bar"></span></button>`)}</div>`;
}

export type Sev = "info" | "success" | "caution" | "error";
const SEV_IC: Record<Sev, string> = { info: "info", success: "check", caution: "triangle-alert", error: "x" };

export function infobar(sev: Sev, title: Raw | string, body: Raw | string = "", action?: Raw, close?: string): Raw {
    return html`<div class="infobar ${sev}" role="status">
        <span class="ib-ic">${raw(icon(SEV_IC[sev], 11))}</span>
        <div class="ib-body"><span class="ib-title">${title}</span> <span>${body}</span>${action ? html` ${action}` : ""}</div>
        ${close ? html`<button class="ibtn" data-act="${close}" aria-label="Dismiss">${raw(icon("x", 16))}</button>` : html`<span></span>`}
    </div>`;
}

export function glyph(ic: string, size = 48, dim = false): Raw {
    return html`<span class="glyph ${dim ? "dim" : ""}" style="width:${size}px;height:${size}px">${raw(icon(ic, Math.round(size * 0.5)))}</span>`;
}

export function dot(on: boolean, color?: "green" | "orange" | "red" | "acc" | "muted"): Raw {
    return html`<span class="dot ${on ? color ?? "green" : "off"}"></span>`;
}

export const spinner = (size = 16) => html`<span class="spin" style="width:${size}px;height:${size}px"></span>`;

/** The Kith mark (small cut, branding/kith-mark-small.svg): two halves passing the task between them.
 *  Halves take currentColor; the dot takes --kdot (the accent unless the class says otherwise). */
const KMARK = `<svg viewBox="27 48 202 160" aria-hidden="true">`
    + `<path d="M92 178.4A64.8 64.8 0 0 1 92 48.8a19.8 19.8 0 0 1 0 39.6 25.2 25.2 0 0 0 0 50.4 19.8 19.8 0 0 1 0 39.6Z"/>`
    + `<path d="M164 77.6a64.8 64.8 0 0 1 0 129.6 19.8 19.8 0 0 1 0-39.6 25.2 25.2 0 0 0 0-50.4 19.8 19.8 0 0 1 0-39.6Z"/>`
    + `<circle class="kdot" cx="128" cy="128" r="18.9"/></svg>`;

export function kmark(cls = ""): Raw {
    return html`<span class="kmark ${cls}">${raw(KMARK)}</span>`;
}

/** Device symbol for a peer from its OS. */
export function osIcon(os?: string): string {
    const o = (os ?? "").toLowerCase();
    if (o.startsWith("windows")) return "monitor";
    if (o.startsWith("darwin") || o.startsWith("mac")) return "laptop";
    if (o.startsWith("linux")) return "server";
    return "monitor-smartphone";
}

function escAttr(s: string) { return s.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;"); }
