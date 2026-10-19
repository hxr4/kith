// Typed wrappers over the generated Go bindings. Daemon JSON passes through
// as-is; these interfaces describe the fields the UI reads.
import * as K from "../../bindings/kith/gui/kithservice";

export interface Transition { seq: number; state: string; detail?: string; ts: string }
export interface Artifact {
    id: string; task_id: string; path: string; sha256: string; size: number; location: string;
    producer_fp: string; fetched: boolean; copy_back: boolean; producer_offline: boolean; status: string;
}
export interface Requirement { req: string; have: string; status: "pass" | "fail" | "unknown" }
export interface FileOffer { path: string; size: number }
export interface Task {
    id: string; role: "requester" | "executor"; peer_fp: string; project: string; name: string; state: string;
    detail?: string; attempt: number; pending_approval: boolean; created_at: string; updated_at: string; started_at?: string;
    code?: string; remediation?: string; log_size: number; project_dir?: string; ws_dir?: string; peer_name?: string;
    argv?: string[]; timeline?: Transition[]; artifacts?: Artifact[]; pid?: number;
    inputs?: FileOffer[]; outputs?: string[]; requirements?: Requirement[];
}
export interface GPU { name: string; vram_gb: number; free_vram_gb: number }
export interface Manifest {
    os: string; arch: string; python: string[] | null; packages: Record<string, string> | null;
    gpus: GPU[] | null; free_disk_gb: number; ram_gb: number; kith_ver?: string;
}
export interface Peer {
    fp: string; display: string; name: string; policy?: string; last_addr?: string; last_seen?: string;
    reachable: boolean; caps?: string; manifest?: Manifest;
}
export interface Status { fp: string; display: string; name: string; port: number; home: string; version: string; pid: number; peers: number; online: number; mdns: boolean }
export interface Snapshot {
    running: boolean; error?: string; service?: string; status?: Status; peers?: Peer[]; tasks?: Task[];
    pending: number; active: number; lite: boolean; platform: string; gui_version: string;
}
export interface LogsResult { task_id: string; offset: number; state: string; done: boolean; data?: string }
export interface FetchResult { task_id: string; path: string; local_path: string; sha256: string; size: number }
export interface Check { name: string; status: "ok" | "warn" | "fail" | "info"; detail: string; fix?: string }
export interface CheckResult {
    file: string; project_dir: string; name: string; project: string; command: string[];
    inputs: FileOffer[]; input_bytes: number; outputs: string[] | null; timeout_minutes: number; on_interrupt: string;
    probe?: string[]; peer?: string; peer_name?: string; reachable: boolean; requirements: Requirement[]; problem?: string; ok: boolean;
}
export interface SubmitResult { task_id: string; state: string; pending_approval?: boolean; code?: string; detail?: string; remediation?: string }
export interface AppInfo { platform: string; chrome: "windows" | "linux" | "mac"; lite: boolean; mica: boolean; home: string; user: string; version: string; log_path: string }
export interface Found { name: string; fp: string; addr: string; mode: string; paired: boolean }
export interface CLIInfo { on_path: boolean; path: string }
export interface PairEvent { kind: "waiting" | "code" | "done" | "error"; data?: any; error?: string }

const j = <T>(p: Promise<any>) => p.then((v) => v as T);

export const api = {
    info: () => j<AppInfo>(K.Info()),
    snapshot: () => j<Snapshot>(K.Snapshot()),
    task: (id: string) => j<Task>(K.Task(id)),
    logs: (id: string, offset: number) => j<LogsResult>(K.Logs(id, offset)),
    approve: (id: string) => j<Task>(K.Approve(id)),
    deny: (id: string) => j<Task>(K.Deny(id)),
    cancel: (id: string) => j<Task>(K.Cancel(id)),
    fetch: (id: string, path: string) => j<FetchResult>(K.Fetch(id, path)),
    setPolicy: (peer: string, policy: string) => K.SetPolicy(peer, policy),
    unpair: (peer: string) => K.Unpair(peer),
    probe: () => j<Manifest>(K.Probe()),
    doctor: () => j<Check[]>(K.Doctor()),
    configList: () => j<Record<string, string>>(K.ConfigList()),
    configSet: (k: string, v: string) => K.ConfigSet(k, v),
    daemon: (action: string) => j<string>(K.Daemon(action)),
    serviceStatus: () => j<string>(K.ServiceStatus()),
    startPairing: (ip: string) => K.StartPairing(ip),
    confirmPairing: (ok: boolean, code: string) => K.ConfirmPairing(ok, code),
    cancelPairing: () => K.CancelPairing(),
    discover: () => j<Found[]>(K.Discover()),
    chooseTaskFile: () => j<string>(K.ChooseTaskFile()),
    resolveTaskFile: (p: string) => j<string>(K.ResolveTaskFile(p)),
    check: (path: string, peer: string) => j<CheckResult>(K.Check(path, peer)),
    submitFile: (path: string, peer: string) => j<SubmitResult>(K.SubmitFile(path, peer)),
    reveal: (path: string) => K.Reveal(path),
    openPath: (path: string) => K.OpenPath(path),
    autostart: () => j<boolean>(K.Autostart()),
    setAutostart: (on: boolean) => K.SetAutostart(on),
    cliStatus: () => j<CLIInfo>(K.CLIStatus()),
    addToPath: () => j<string>(K.AddToPath()),
    addFirewallRules: () => j<string>(K.AddFirewallRules()),
    showMain: (route: string) => K.ShowMain(route),
    hideFlyout: () => K.HideFlyout(),
    fitFlyout: (h: number) => K.FitFlyout(h),
    quit: () => K.Quit(),
};

/** Errors from Go arrive as Error objects (or strings). */
export function errText(e: unknown): string {
    if (e instanceof Error) return e.message;
    if (typeof e === "string") return e;
    try { return JSON.stringify(e); } catch { return String(e); }
}
