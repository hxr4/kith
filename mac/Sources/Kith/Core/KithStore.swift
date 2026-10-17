import SwiftUI
import Observation
import AppKit

enum Screen: String, Hashable, CaseIterable {
    case overview, tasks, approvals, machines, diagnostics

    var title: String { rawValue.prefix(1).uppercased() + rawValue.dropFirst() }
    var symbol: String {
        switch self {
        case .overview: "square.grid.2x2"
        case .tasks: "list.bullet"
        case .approvals: "hand.raised"
        case .machines: "display.2"
        case .diagnostics: "stethoscope"
        }
    }
}

/// Everything the UI shows comes from the daemon (IPC) or the kith CLI; the
/// app keeps no state of its own beyond navigation.
@MainActor @Observable
final class KithStore {
    static let shared = KithStore()

    // Daemon
    var running = false
    var loaded = false
    var daemonBusy: String?          // "Starting…", "Stopping…", "Restarting…"
    var status: StatusView?
    var peers: [Peer] = []
    var tasks: [KTask] = []
    var service = ""
    var config: [String: String] = [:]
    var configDirty = false          // saved settings need a daemon restart
    var localManifest: Manifest?
    var localAddress: String?

    // Navigation
    var screen: Screen = .overview
    var openTaskID: String?
    var showNewTask = false
    var newTaskFile: URL?
    var showPairing = false
    var search = ""
    var toast: Toast?

    // Diagnostics
    var checks: [Check] = []
    var checksShown = 0
    var checking = false
    var checkedAt: Date?

    // Approvals decided in this session (the daemon keeps the outcome as task state)
    var decided: [String: String] = [:]

    var interactive = false // a window or the popover is visible: poll faster

    private var pollTask: Task<Void, Never>?
    private var prevTasks: [String: KTask]?
    private var peerState: [String: (on: Bool, since: Date, told: Bool?)] = [:]
    private var serviceAt = Date.distantPast
    private var toastTask: Task<Void, Never>?

    // MARK: Derived

    var pending: [KTask] { tasks.filter(\.needsApproval) }
    var active: [KTask] { tasks.filter { !$0.ended && !$0.needsApproval } }
    var finished: [KTask] { tasks.filter(\.ended) }
    var reachable: [Peer] { peers.filter(\.reachable) }
    var thisName: String { status?.name ?? config["name"] ?? Host.current().localizedName ?? "This Mac" }

    func peer(_ fp: String) -> Peer? { peers.first { $0.fp == fp } }
    func task(_ id: String) -> KTask? { tasks.first { $0.id == id } }

    // MARK: Polling

    func start() {
        guard pollTask == nil else { return }
        localAddress = Net.lanIPv4()
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                await self.refresh()
                try? await Task.sleep(for: .milliseconds(self.interactive ? 1200 : 3000))
            }
        }
        Task { await loadConfig(); await probeLocal() }
    }

    func refresh() async {
        do {
            let st: StatusView = try await IPC.call("status", timeout: 5)
            async let p: [Peer] = IPC.call("peers", timeout: 10)
            async let t: [KTask] = IPC.call("tasks", ["limit": 300], timeout: 10)
            let (pp, tt) = try await (p, t)
            withAnimation(K.layout) {
                running = true
                if status != st { status = st }
                if peers != pp { peers = pp }
                if tasks != tt { tasks = tt }
            }
            notices(tt, pp)
        } catch {
            withAnimation(K.layout) {
                running = false
                status = nil
            }
            prevTasks = nil
        }
        loaded = true
        if Date().timeIntervalSince(serviceAt) > 15 { await refreshService() }
    }

    func refreshService() async {
        struct S: Decodable { var service: String? }
        serviceAt = Date()
        if let s = try? await KithCLI.json(["status"], as: S.self, timeout: 15) { service = s.service ?? "" }
    }

    var serviceInstalled: Bool { service.hasPrefix("installed") }

    func probeLocal() async {
        if let m: Manifest = try? await IPC.call("probe", timeout: 60) { localManifest = m }
    }

    func loadConfig() async {
        if let c = try? await KithCLI.json(["config", "list"], as: [String: String].self) { config = c }
    }

    /// Latest log line per active task (popover, Overview's Now strip).
    var tails: [String: String] = [:]

    @ObservationIgnored private var tailOffsets: [String: Int64] = [:]

    /// Reads each active task's log incrementally and keeps the last line.
    /// (The task list's log_size lags while a task runs, so track offsets.)
    func refreshTails(limit: Int = 3) async {
        for t in active.prefix(limit) {
            let from = tailOffsets[t.id] ?? max(0, t.logSize - 4096)
            guard let r: LogsResult = try? await IPC.call("logs", ["id": t.id, "offset": from, "follow": false, "max": 1 << 20], timeout: 5)
            else { continue }
            tailOffsets[t.id] = r.offset
            guard let data = r.data, !data.isEmpty else { continue }
            let line = data.split(whereSeparator: \.isNewline).last.map(String.init)?
                .trimmingCharacters(in: .whitespaces) ?? ""
            if !line.isEmpty, tails[t.id] != line {
                withAnimation(.easeOut(duration: 0.12)) { tails[t.id] = line }
            }
        }
    }

    // MARK: Notifications (diffs between polls; the first poll is a baseline)

    private func notices(_ tasks: [KTask], _ peers: [Peer]) {
        let now = Date()
        defer { prevTasks = Dictionary(uniqueKeysWithValues: tasks.map { ($0.id, $0) }) }
        for p in peers {
            if var s = peerState[p.fp] {
                if s.on != p.reachable { s = (p.reachable, now, s.told) }
                if now.timeIntervalSince(s.since) >= 30, s.told != s.on {
                    if s.told != nil { Notifier.shared.peer(p, online: s.on) }
                    s.told = s.on
                }
                peerState[p.fp] = s
            } else {
                peerState[p.fp] = (p.reachable, now.addingTimeInterval(-30), p.reachable)
            }
        }
        guard let prev = prevTasks else { return }
        for t in tasks {
            let old = prev[t.id]
            if t.needsApproval && !(old?.needsApproval ?? false) { Notifier.shared.approval(t) }
            if t.sent && t.ended && !(old?.ended ?? true) { Notifier.shared.finished(t) }
        }
    }

    // MARK: Actions

    @discardableResult
    func act(_ done: String?, _ op: () async throws -> Void) async -> Bool {
        do {
            try await op()
            if let done { flash(done) }
            await refresh()
            return true
        } catch {
            fail(error)
            return false
        }
    }

    func approve(_ t: KTask) async {
        if await act(nil, { try await IPC.send("approve", ["id": t.id]) }) {
            decided[t.id] = "Approved"
        }
    }

    func deny(_ t: KTask) async {
        if await act(nil, { try await IPC.send("deny", ["id": t.id]) }) {
            decided[t.id] = "Denied"
        }
    }

    func cancel(_ t: KTask) async {
        await act(nil) { try await IPC.send("cancel", ["id": t.id]) }
    }

    func fetch(_ t: KTask, _ a: Artifact) async -> FetchResult? {
        do {
            let r: FetchResult = try await IPC.call("fetch", ["id": t.id, "path": a.path], timeout: 3600)
            await refresh()
            return r
        } catch {
            fail(error)
            return nil
        }
    }

    func setPolicy(_ p: Peer, _ policy: String) async {
        await act(nil) { try await IPC.send("set_policy", ["peer": p.fp, "policy": policy]) }
    }

    func unpair(_ p: Peer) async {
        await act("Unpaired \(p.name)") { try await IPC.send("unpair", ["peer": p.fp]) }
    }

    func reprobe() async -> Bool {
        // A fresh local probe; peers re-advertise on reconnect.
        await probeLocal()
        await refresh()
        return localManifest != nil
    }

    /// start | stop | restart | install | uninstall
    func daemon(_ action: String) async {
        let label = ["start": "Starting…", "stop": "Stopping…", "restart": "Restarting…",
                     "install": "Installing…", "uninstall": "Removing…"][action]
        withAnimation(K.snappy) { daemonBusy = label }
        defer { withAnimation(K.snappy) { daemonBusy = nil } }
        do {
            try await KithCLI.plain(["daemon", action], timeout: 90)
            if action == "restart" || action == "start" { configDirty = false }
        } catch {
            fail(error)
        }
        serviceAt = .distantPast
        await refresh()
        await refreshService()
    }

    func setConfig(_ key: String, _ value: String) async {
        guard config[key] != value else { return }
        do {
            try await KithCLI.plain(["config", "set", key, value])
            config[key] = value
            if running { configDirty = true }
        } catch {
            fail(error)
            await loadConfig()
        }
    }

    func check(file: URL, peer: String?) async throws -> CheckResult {
        var args = ["check", "-f", file.path]
        if let peer { args += ["--peer", peer] }
        return try await KithCLI.json(args, as: CheckResult.self, timeout: 120)
    }

    func submit(file: URL, peer: String) async -> SubmitResult? {
        do {
            let r = try await KithCLI.json(["submit", "-f", file.path, "--peer", peer], as: SubmitResult.self, timeout: 600)
            await refresh()
            return r
        } catch {
            fail(error)
            return nil
        }
    }

    func runDiagnostics() async {
        guard !checking else { return }
        withAnimation(K.layout) { checking = true; checksShown = 0 }
        let result = (try? await KithCLI.json(["doctor"], as: [Check].self, timeout: 60)) ?? [
            Check(name: "doctor", status: "fail", detail: "kith doctor could not run", fix: "Reinstall Kith or check that the kith CLI is installed.")
        ]
        checks = result
        // Reveal one by one (230 ms stagger).
        for i in 1...max(1, result.count) {
            try? await Task.sleep(for: .milliseconds(230))
            withAnimation(K.layout) { checksShown = i }
        }
        checkedAt = Date()
        checking = false
    }

    // MARK: Feedback

    func flash(_ text: String, error: Bool = false) {
        toastTask?.cancel()
        withAnimation(K.pill) { toast = Toast(text: text, isError: error) }
        toastTask = Task {
            try? await Task.sleep(for: .milliseconds(error ? 4000 : 1400))
            guard !Task.isCancelled else { return }
            withAnimation(K.smooth) { toast = nil }
        }
    }

    func fail(_ error: Error) {
        if let e = error as? KithError {
            flash(e.remediation.map { "\(e.message) \($0)" } ?? e.message, error: true)
        } else {
            flash(error.localizedDescription, error: true)
        }
    }

    func copy(_ text: String, label: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        flash("Copied \(label)")
    }

    func reveal(_ path: String) {
        NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: path)])
    }

    // MARK: Navigation

    func open(_ screen: Screen) {
        withAnimation(K.smooth) {
            openTaskID = nil
            self.screen = screen
        }
        AppDelegate.shared?.showMain()
    }

    func openTask(_ id: String) {
        withAnimation(K.layout) {
            screen = .tasks
            openTaskID = id
        }
        AppDelegate.shared?.showMain()
    }

    func newTask(_ file: URL? = nil) {
        newTaskFile = file
        if screen == .overview || openTaskID != nil { openTaskID = nil }
        showNewTask = true
        AppDelegate.shared?.showMain()
    }

    func pair() {
        showPairing = true
        AppDelegate.shared?.showMain()
    }
}

enum Net {
    /// The first IPv4 address on a physical interface (en*), for "This Mac · 10.x.x.x".
    static func lanIPv4() -> String? {
        var ifaddr: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&ifaddr) == 0, let first = ifaddr else { return nil }
        defer { freeifaddrs(ifaddr) }
        var p: UnsafeMutablePointer<ifaddrs>? = first
        while let cur = p {
            let i = cur.pointee
            if let sa = i.ifa_addr, sa.pointee.sa_family == UInt8(AF_INET) {
                let name = String(cString: i.ifa_name)
                if name.hasPrefix("en") {
                    var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
                    if getnameinfo(sa, socklen_t(sa.pointee.sa_len), &host, socklen_t(host.count), nil, 0, NI_NUMERICHOST) == 0 {
                        return String(cString: host)
                    }
                }
            }
            p = i.ifa_next
        }
        return nil
    }
}
