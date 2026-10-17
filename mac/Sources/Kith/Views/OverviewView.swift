import SwiftUI

/// The link between this Mac and a peer, both manifests, what's running now
/// and recent activity.
struct OverviewView: View {
    var ns: Namespace.ID
    @Environment(KithStore.self) private var store
    @State private var peerFP: String?

    private var peer: Peer? {
        store.peers.first { $0.fp == peerFP } ?? store.peers.first(where: \.reachable) ?? store.peers.first
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 28) {
                if store.loaded && store.running && store.peers.isEmpty {
                    NoPeersHero()
                } else {
                    machines
                }
                if !store.active.isEmpty { now }
                if !activity.isEmpty { activityList }
            }
            .padding(.horizontal, 28).padding(.top, 26).padding(.bottom, 32)
            .frame(maxWidth: 1040)
            .frame(maxWidth: .infinity)
        }
        .task {
            while !Task.isCancelled {
                await store.refreshTails(limit: 4)
                try? await Task.sleep(for: .milliseconds(1200))
            }
        }
    }

    // MARK: Link + manifests

    private var linkMode: LinkMode {
        guard let p = peer, p.reachable else { return .off }
        let mine = store.active.filter { $0.peerFp == p.fp }
        if mine.contains(where: { $0.state == TaskState.transferring && $0.sent }) { return .flow }
        if mine.contains(where: { $0.state == TaskState.running && $0.sent }) { return .flowBack }
        if mine.contains(where: { $0.state == TaskState.running && !$0.sent }) { return .flow }
        if mine.contains(where: { $0.state == TaskState.transferring }) { return .flowBack }
        return .idle
    }

    private var linkLabel: String {
        switch linkMode {
        case .off: "offline"
        case .idle: "connected"
        case .flow: store.active.contains { $0.state == TaskState.transferring } ? "sending files" : "streaming logs"
        case .flowBack: store.active.contains { $0.state == TaskState.transferring && !$0.sent } ? "receiving files" : "streaming logs"
        }
    }

    private var machines: some View {
        VStack(spacing: 16) {
            HStack(spacing: 0) {
                HStack(spacing: 12) {
                    Spacer(minLength: 0)
                    VStack(alignment: .trailing, spacing: 1) {
                        Text(store.thisName).font(K.ui(15, .semibold)).lineLimit(1)
                        Text("This Mac" + (store.localAddress.map { " · \($0)" } ?? "")).font(K.ui(11.5)).foregroundStyle(K.l2)
                    }
                    DeviceGlyph(symbol: "laptopcomputer")
                }
                .frame(maxWidth: .infinity)
                VStack(spacing: 7) {
                    Text(linkLabel).font(K.ui(10.5, .medium)).foregroundStyle(linkMode == .off ? K.l3 : K.accText)
                        .contentTransition(.interpolate)
                    LinkLine(mode: linkMode)
                    Text(linkMode == .off ? "not connected" : "TLS 1.3 · pinned")
                        .font(K.mono(10)).foregroundStyle(K.l3)
                }
                .padding(.horizontal, 4)
                .frame(width: 170)
                HStack(spacing: 12) {
                    DeviceGlyph(symbol: peer?.symbol ?? "desktopcomputer", dim: !(peer?.reachable ?? false))
                    VStack(alignment: .leading, spacing: 1) {
                        HStack(spacing: 7) {
                            if store.peers.count > 1 {
                                Menu {
                                    ForEach(store.peers) { p in Button(p.name) { withAnimation(K.layout) { peerFP = p.fp } } }
                                } label: { Text(peer?.name ?? "—").font(K.ui(15, .semibold)) }
                                    .menuStyle(.button).buttonStyle(.plain).fixedSize()
                            } else {
                                Text(peer?.name ?? "—").font(K.ui(15, .semibold))
                            }
                            StatusDot(on: peer?.reachable ?? false)
                        }
                        Text(peerSubtitle).font(K.ui(11.5)).foregroundStyle(K.l2).lineLimit(1)
                    }
                    Spacer(minLength: 0)
                }
                .frame(maxWidth: .infinity)
            }
            HStack(alignment: .top, spacing: 0) {
                ManifestPane(rows: macRows).frame(maxWidth: .infinity)
                Color.clear.frame(width: 170, height: 1)
                if let p = peer {
                    ManifestPane(rows: peerRows(p)).frame(maxWidth: .infinity)
                } else {
                    Color.clear.frame(maxWidth: .infinity, maxHeight: 1)
                }
            }
        }
    }

    private var peerSubtitle: String {
        guard let p = peer else { return "" }
        if p.reachable { return "Reachable · \(p.lastAddr ?? "")" }
        return "Offline · last seen \(Fmt.ago(p.lastSeen.flatMap(Fmt.date)))"
    }

    private var macRows: [ManifestRow] {
        var r: [ManifestRow] = []
        if let s = store.status { r.append(.init(key: "Fingerprint", value: s.display, mono: true, copy: s.display)) }
        let m = store.localManifest
        r.append(.init(key: "OS", value: "\(Fmt.osLong(m?.os ?? "macOS")) · \(m?.arch ?? SysInfo.arch)"))
        r.append(.init(key: "CPU", value: SysInfo.cpu))
        r.append(.init(key: "RAM", value: Fmt.gb(m?.ramGb ?? SysInfo.ramGB)))
        r.append(.init(key: "GPU", value: SysInfo.cpu.hasPrefix("Apple") ? "\(SysInfo.cpu) GPU" : "Integrated",
                       sub: "Metal · shares memory with RAM"))
        if let m {
            r.append(.init(key: "Disk", value: "\(Fmt.gb(m.freeDiskGb)) free"))
            r.append(.init(key: "Python", value: (m.python ?? []).joined(separator: ", ").ifEmpty("none found"), mono: true))
            r.append(.init(key: "Packages", value: packages(m).ifEmpty("none of torch, numpy"), mono: true))
        } else {
            r.append(.init(key: "Probe", value: "reading capabilities…"))
        }
        return r
    }

    private func peerRows(_ p: Peer) -> [ManifestRow] {
        var r: [ManifestRow] = [.init(key: "Fingerprint", value: p.display, mono: true, copy: p.display)]
        guard let m = p.manifest else {
            r.append(.init(key: "Capabilities", value: "sent when \(p.name) connects"))
            return r
        }
        r.append(.init(key: "OS", value: "\(Fmt.osLong(m.os)) · \(m.arch)"))
        r.append(.init(key: "RAM", value: Fmt.gb(m.ramGb)))
        for g in m.gpus ?? [] {
            let used = g.vramGb > 0 ? max(0, min(1, 1 - g.freeVramGb / g.vramGb)) : 0
            r.append(.init(key: "GPU", value: g.name, sub: "\(Fmt.gb(g.freeVramGb)) free of \(Fmt.gb(g.vramGb))", meter: used, gpu: g))
        }
        if (m.gpus ?? []).isEmpty { r.append(.init(key: "GPU", value: "no NVIDIA GPU")) }
        r.append(.init(key: "Disk", value: "\(Fmt.gb(m.freeDiskGb)) free"))
        r.append(.init(key: "Python", value: (m.python ?? []).joined(separator: ", ").ifEmpty("none found"), mono: true))
        r.append(.init(key: "Packages", value: packages(m).ifEmpty("none of torch, numpy"), mono: true))
        return r
    }

    private func packages(_ m: Manifest) -> String {
        (m.packages ?? [:]).sorted { $0.key < $1.key }.map { "\($0.key) \($0.value)" }.joined(separator: " · ")
    }

    // MARK: Now + activity

    private var now: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Now").font(K.ui(13, .semibold))
            VStack(spacing: 0) {
                ForEach(store.active) { t in
                    HStack(spacing: 14) {
                        StateChip(state: t.state).frame(width: 118, alignment: .leading)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(t.name).font(K.ui(13, .medium)).lineLimit(1)
                            Text(nowMeta(t)).font(K.ui(11)).foregroundStyle(K.l2).lineLimit(1)
                        }
                        .frame(width: 160, alignment: .leading)
                        Text(store.tails[t.id] ?? t.detail ?? "").font(K.mono(11)).foregroundStyle(K.l2)
                            .lineLimit(1).truncationMode(.tail).frame(maxWidth: .infinity, alignment: .leading)
                            .id(store.tails[t.id]).transition(.opacity.animation(.easeOut(duration: 0.12)))
                        Elapsed(since: t.started ?? t.created).font(K.mono(11.5)).frame(width: 56, alignment: .trailing)
                    }
                    .padding(.horizontal, 14).padding(.vertical, 10)
                    .hoverFill(radius: 0)
                    .overlay(alignment: .bottom) { Divider05() }
                    .onTapGesture { store.openTask(t.id) }
                    .transition(.opacity.combined(with: .offset(y: 6)))
                }
            }
            .card(radius: 10, fill: .clear)
        }
    }

    private func nowMeta(_ t: KTask) -> String {
        var s = t.sent ? t.peerLabel : "this Mac · from \(t.peerLabel)"
        if t.attempt > 1 { s += " · attempt \(t.attempt)" }
        return s
    }

    private var activity: [KTask] {
        store.tasks.sorted { ($0.updated ?? .distantPast) > ($1.updated ?? .distantPast) }.prefix(8).map { $0 }
    }

    private var activityList: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Activity").font(K.ui(13, .semibold)).padding(.bottom, 4)
            ForEach(activity) { t in
                HStack(spacing: 12) {
                    Text(Fmt.time(t.updated)).font(K.mono(11)).foregroundStyle(K.l3).frame(width: 62, alignment: .leading)
                    StateChip(state: t.state).frame(width: 112, alignment: .leading)
                    Text("\(Text(t.name).fontWeight(.medium)) \(Text(describe(t)).foregroundStyle(K.l2))")
                        .font(K.ui(12.5)).lineLimit(1).truncationMode(.tail)
                    Spacer(minLength: 0)
                }
                .padding(.vertical, 3)
                .contentShape(Rectangle())
                .onTapGesture { store.openTask(t.id) }
            }
        }
    }

    private func describe(_ t: KTask) -> String {
        let peer = t.peerLabel
        switch t.state {
        case TaskState.submitted: return t.needsApproval ? "from \(peer) is waiting for your approval" : (t.sent ? "sent to \(peer)" : "received from \(peer)")
        case TaskState.accepted: return t.sent ? "accepted by \(peer)" : "accepted from \(peer)"
        case TaskState.transferring: return t.sent ? "sending files to \(peer)" : "receiving files from \(peer)"
        case TaskState.running: return t.attempt > 1 ? "running as attempt \(t.attempt)" : (t.sent ? "running on \(peer)" : "running on this Mac")
        case TaskState.finalizing: return "hashing outputs"
        case TaskState.interrupted: return t.detail ?? "\(peer) stopped responding"
        case TaskState.cancelling: return "stopping"
        case TaskState.rejected: return outcomeDetail(t)
        default: return t.detail ?? (t.sent ? "on \(peer)" : "from \(peer)")
        }
    }
}

struct ManifestRow: Identifiable {
    var key: String
    var value: String
    var sub: String? = nil
    var mono = false
    var copy: String? = nil
    var meter: Double? = nil
    var gpu: GPU? = nil
    var id: String { key + value }
}

struct ManifestPane: View {
    var rows: [ManifestRow]
    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(rows) { r in ManifestRowView(row: r) }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.vertical, 6)
        .card(radius: 10, fill: K.bg2)
    }
}

private struct ManifestRowView: View {
    var row: ManifestRow
    @State private var pop = false

    var body: some View {
        KVRow(key: row.key) {
            VStack(alignment: .leading, spacing: 4) {
                if let c = row.copy {
                    CopyableMono(text: c, label: row.key.lowercased())
                } else {
                    Text(row.value).font(row.mono ? K.mono(11.5) : K.ui(12.5)).lineLimit(2).textSelection(.enabled)
                }
                if let s = row.sub { Text(s).font(K.ui(11)).foregroundStyle(K.l2) }
                if let m = row.meter {
                    GeometryReader { g in
                        ZStack(alignment: .leading) {
                            Capsule().fill(K.fill2)
                            Capsule().fill(K.acc).frame(width: g.size.width * m)
                        }
                    }
                    .frame(maxWidth: 160).frame(height: 4)
                }
            }
        }
        .onHover { h in if row.gpu != nil { withAnimation(K.snappy) { pop = h } } }
        .popover(isPresented: $pop, arrowEdge: .bottom) {
            if let g = row.gpu {
                Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 4) {
                    GridRow { Text("Total").foregroundStyle(K.l2); Text(mib(g.vramGb)).font(K.mono(11.5)).gridColumnAlignment(.trailing) }
                    GridRow { Text("Free").foregroundStyle(K.l2); Text(mib(g.freeVramGb)).font(K.mono(11.5)) }
                    GridRow { Text("In use").foregroundStyle(K.l2); Text(mib(g.vramGb - g.freeVramGb)).font(K.mono(11.5)) }
                }
                .font(K.ui(11.5)).padding(12)
            }
        }
    }

    private func mib(_ gb: Double) -> String { "\(Int((gb * 1024).rounded()).formatted()) MiB" }
}

struct NoPeersHero: View {
    @Environment(KithStore.self) private var store
    var body: some View {
        VStack(spacing: 14) {
            HStack(spacing: 10) {
                DeviceGlyph(symbol: "laptopcomputer")
                LinkLine(mode: .off).frame(width: 120)
                DeviceGlyph(symbol: "desktopcomputer", dim: true)
            }
            Text("Pair your other machine").font(K.ui(17, .semibold))
            Text("Open Kith on both machines and pair once with a 6-character code. After that they find each other on this network, and you can send work from here, the kith CLI, or an AI agent.")
                .font(K.ui(12.5)).foregroundStyle(K.l2).multilineTextAlignment(.center).frame(maxWidth: 440)
            Button("Pair a machine") { store.pair() }.buttonStyle(.kPrimary).padding(.top, 4)
        }
        .frame(maxWidth: .infinity).padding(.vertical, 36)
    }
}

enum SysInfo {
    static let cpu: String = sysctl("machdep.cpu.brand_string") ?? "Apple silicon"
    static let arch: String = {
        #if arch(arm64)
        "arm64"
        #else
        "amd64"
        #endif
    }()
    static let ramGB: Double = Double(ProcessInfo.processInfo.physicalMemory) / Double(1 << 30)

    private static func sysctl(_ name: String) -> String? {
        var size = 0
        guard sysctlbyname(name, nil, &size, nil, 0) == 0, size > 0 else { return nil }
        var buf = [CChar](repeating: 0, count: size)
        guard sysctlbyname(name, &buf, &size, nil, 0) == 0 else { return nil }
        return String(cString: buf)
    }
}

extension String {
    func ifEmpty(_ s: String) -> String { isEmpty ? s : self }
}
