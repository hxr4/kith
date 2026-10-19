// Formatting shared by every screen.

export function date(s?: string): Date | null {
    if (!s) return null;
    const d = new Date(s);
    return isNaN(+d) ? null : d;
}

export function bytes(n: number): string {
    if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + " GB";
    if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MB";
    if (n >= 1 << 10) return Math.round(n / (1 << 10)) + " KB";
    return n + " B";
}

export function gb(v: number): string {
    return v >= 10 || Math.round(v) === v ? Math.round(v) + " GB" : v.toFixed(1) + " GB";
}

/** 0:42 · 9:40 · 1:02:03 */
export function clock(sec: number): string {
    const s = Math.max(0, Math.floor(sec));
    const two = (n: number) => String(n).padStart(2, "0");
    if (s >= 3600) return `${Math.floor(s / 3600)}:${two(Math.floor(s / 60) % 60)}:${two(s % 60)}`;
    return `${Math.floor(s / 60)}:${two(s % 60)}`;
}

/** 24-hour wall clock. */
export function hms(d: Date | null): string {
    if (!d) return "";
    const two = (n: number) => String(n).padStart(2, "0");
    return `${two(d.getHours())}:${two(d.getMinutes())}:${two(d.getSeconds())}`;
}

export function ago(d: Date | null, now = Date.now()): string {
    if (!d) return "";
    const s = (now - +d) / 1000;
    if (s < 5) return "now";
    if (s < 60) return `${Math.floor(s)} s ago`;
    if (s < 3600) return `${Math.floor(s / 60)} min ago`;
    if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
    const y = new Date(now); y.setDate(y.getDate() - 1);
    if (d.toDateString() === y.toDateString()) return "yesterday";
    return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

export function agoShort(d: Date | null, now = Date.now()): string {
    if (!d) return "";
    const s = (now - +d) / 1000;
    if (s < 60) return "now";
    if (s < 3600) return `${Math.floor(s / 60)} m`;
    if (s < 86400) return `${Math.floor(s / 3600)} h`;
    return `${Math.floor(s / 86400)} d`;
}

export function osName(os: string): string {
    const o = (os || "").toLowerCase();
    if (o.startsWith("windows")) return "Windows";
    if (o.startsWith("darwin") || o.startsWith("mac")) return "macOS";
    if (o.startsWith("linux")) return "Linux";
    return os;
}

/** "windows-11" → "Windows 11" */
export function osLong(os: string): string {
    const i = os.indexOf("-");
    return osName(os) + (i > 0 ? " " + os.slice(i + 1) : "");
}

export function gpuShort(n: string): string {
    return n.replace("NVIDIA ", "").replace("GeForce ", "").replace(" Laptop GPU", "");
}

export function sha(s: string): string { return s.length > 12 ? `${s.slice(0, 6)}…${s.slice(-4)}` : s; }

export function argv(a?: string[]): string {
    return (a ?? []).map((x) => (/\s/.test(x) ? `"${x}"` : x)).join(" ");
}

export function pairCode(c: string): string {
    const s = (c || "").replace(/[\s-]/g, "");
    return s.length === 6 ? `${s.slice(0, 3)} ${s.slice(3)}` : c;
}

export const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"}`;
