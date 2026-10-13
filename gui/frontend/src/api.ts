// Typed wrappers over the generated Go bindings. The daemon's JSON is
// passed through as-is; these interfaces describe the fields the UI uses.
import * as K from "../bindings/kith/gui/kithservice";

export interface Transition { seq: number; state: string; detail?: string; ts: string }
export interface Artifact {
    task_id: string; path: string; sha256: string; size: number; location: string;
    producer_fp: string; fetched: boolean; copy_back: boolean; producer_offline: boolean; status: string;
}
export interface Task {
    id: string; role: "requester" | "executor"; peer_fp: string; project: string; name: string; state: string;
    detail?: string; attempt: number; pending_approval: boolean; created_at: string; updated_at: string;
    code?: string; remediation?: string; log_size: number; project_dir?: string; peer_name?: string;
    argv?: string[]; timeline?: Transition[]; artifacts?: Artifact[]; pid?: number;
}
export interface GPU { name: string; vram_gb: number; free_vram_gb: number }
export interface Manifest {
    os: string; arch: string; python: string[] | null; packages: Record<string, string> | null;
    gpus: GPU[] | null; free_disk_gb: number; ram_gb: number;
}
export interface Peer {
    fp: string; display: string; name: string; policy: string; last_addr: string; last_seen: string;
    reachable: boolean; caps?: string; manifest?: Manifest;
}
export interface Status { fp: string; display: string; name: string; port: number; home: string; version: string; pid: number }
export interface Snapshot {
    running: boolean; error?: string; service?: string; status?: Status; peers?: Peer[]; tasks?: Task[];
    pending: number; active: number; lite: boolean; platform: string; gui_version: string;
}
export interface LogsResult { task_id: string; offset: number; state: string; done: boolean; data?: string }
export interface FetchResult { task_id: string; path: string; local_path: string; sha256: string; size: number }
export interface Check { name: string; status: "ok" | "warn" | "fail" | "info"; detail: string; fix?: string }
export interface PairEvent { kind: "waiting" | "code" | "done" | "error"; data?: any; error?: string }

const j = <T>(p: Promise<any>) => p.then((v) => v as T);

export const api = {
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
    daemon: (action: string) => K.Daemon(action),
    startPairing: (ip: string) => K.StartPairing(ip),
    confirmPairing: (ok: boolean) => K.ConfirmPairing(ok),
    cancelPairing: () => K.CancelPairing(),
    chooseTaskFile: () => K.ChooseTaskFile(),
    submitFile: (path: string, peer: string) => j<any>(K.SubmitFile(path, peer)),
    reveal: (path: string) => K.Reveal(path),
    autostart: () => K.Autostart(),
    setAutostart: (on: boolean) => K.SetAutostart(on),
};

// Errors from Go arrive as Error objects (or strings); show their message.
export function errText(e: unknown): string {
    if (e instanceof Error) return e.message;
    if (typeof e === "string") return e;
    try { return JSON.stringify(e); } catch { return String(e); }
}
