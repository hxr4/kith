import morphdom from "morphdom";

/** Markup that is already safe (built by html``). */
export class Raw {
    constructor(readonly s: string) {}
    toString() { return this.s; }
}

export const raw = (s: string) => new Raw(s);

export function esc(v: unknown): string {
    return String(v ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
}

function part(v: unknown): string {
    if (v === null || v === undefined || v === false || v === true) return "";
    if (v instanceof Raw) return v.s;
    if (Array.isArray(v)) return v.map(part).join("");
    return esc(v);
}

/** Tagged template: interpolations are escaped unless they are Raw. */
export function html(strings: TemplateStringsArray, ...vals: unknown[]): Raw {
    let out = strings[0];
    for (let i = 0; i < vals.length; i++) out += part(vals[i]) + strings[i + 1];
    return new Raw(out);
}

/** Inline style string from an object (only defined values). */
export function sty(o: Record<string, string | number | undefined | false>): string {
    return Object.entries(o).filter(([, v]) => v !== undefined && v !== false && v !== "")
        .map(([k, v]) => `${k}:${v}`).join(";");
}

// Patch a container with new markup. Existing nodes are kept (so their
// animations and focus survive); new nodes are inserted and animate in.
// Elements marked data-keep are owned by imperative code (the log console,
// live counters) and are never touched after their first render.
export function patch(el: HTMLElement, content: Raw) {
    const next = el.cloneNode(false) as HTMLElement;
    next.innerHTML = content.s;
    morphdom(el, next, {
        childrenOnly: true,
        getNodeKey: (n) => (n as HTMLElement).id || (n as HTMLElement).dataset?.key || undefined,
        onBeforeElUpdated(from, to) {
            if (from.hasAttribute("data-keep") && to.hasAttribute("data-keep")
                && from.getAttribute("data-keep") === to.getAttribute("data-keep")) return false;
            if ((from.tagName === "INPUT" || from.tagName === "TEXTAREA") && from === document.activeElement) {
                // Never fight the user's typing; copy everything but the value.
                for (const a of Array.from(to.attributes)) if (a.name !== "value") from.setAttribute(a.name, a.value);
                return false;
            }
            if (from.classList.contains("dg") && from.textContent !== to.textContent) {
                from.textContent = to.textContent;
                roll(from);
                return false;
            }
            return true;
        },
    });
}

/** Restart a CSS animation class. */
export function replay(el: Element, cls: string) {
    el.classList.remove(cls);
    void (el as HTMLElement).offsetWidth;
    el.classList.add(cls);
}

/** Digits that roll when they change (rendered as .dg spans). */
export function rolling(text: string): Raw {
    return raw(String(text).split("").map((c, i) =>
        /\d/.test(c) ? `<span class="dg" data-key="d${i}">${c}</span>` : esc(c)).join(""));
}

/** Imperatively update a rolling-number element without a full render. */
export function setRolling(el: HTMLElement, text: string) {
    if (el.dataset.v === text) return;
    el.dataset.v = text;
    const kids = Array.from(el.children) as HTMLElement[];
    const chars = text.split("");
    if (kids.length !== chars.filter((c) => /\d/.test(c)).length || el.textContent!.length !== text.length) {
        el.innerHTML = rolling(text).s;
        return;
    }
    let k = 0;
    chars.forEach((c) => {
        if (!/\d/.test(c)) return;
        const span = kids[k++];
        if (span.textContent !== c) { span.textContent = c; roll(span); }
    });
}

/** Roll one digit in (Fluent "point to point", 167 ms). The safety cancel
 *  keeps the digit visible even if the page's animation clock is paused
 *  (occluded window, background tab). */
export function roll(el: HTMLElement) {
    if (matchMedia("(prefers-reduced-motion: reduce)").matches || document.documentElement.classList.contains("lite")) return;
    const a = el.animate([{ transform: "translateY(-60%)", opacity: 0 }, { transform: "none", opacity: 1 }],
        { duration: 167, easing: "cubic-bezier(0.1, 0.9, 0.2, 1)" });
    setTimeout(() => a.cancel(), 400);
}
