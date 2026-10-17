import SwiftUI

/// Paired machines: manifest, Ask / Auto policy, unpair.
struct MachinesView: View {
    @Environment(KithStore.self) private var store

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                HStack {
                    Text(headline).font(K.ui(12.5)).foregroundStyle(K.l2)
                    Spacer()
                    Button { store.pair() } label: { Label("Pair a machine", systemImage: "link.badge.plus") }
                        .buttonStyle(.kPrimary)
                }
                if store.peers.isEmpty && store.loaded { NoPeersHero() }
                ForEach(store.peers) { p in
                    if p.reachable || p.manifest != nil { PeerCard(peer: p) } else { OfflinePeerCard(peer: p) }
                }
            }
            .padding(.horizontal, 28).padding(.top, 20).padding(.bottom, 32)
            .frame(maxWidth: 900)
            .frame(maxWidth: .infinity)
            .animation(K.layout, value: store.peers.map(\.fp))
        }
    }

    private var headline: String {
        let n = store.peers.count
        return n == 0 ? "No paired machines yet." : "\(n) paired machine\(n == 1 ? "" : "s"). Each one was verified with a code on both screens."
    }
}

private struct PeerCard: View {
    var peer: Peer
    @Environment(KithStore.self) private var store
    @State private var policy = "ask"
    @State private var confirmUnpair = false
    @State private var refreshing = false

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 12) {
                DeviceGlyph(symbol: peer.symbol, size: 40, dim: !peer.reachable)
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 7) {
                        Text(peer.name).font(K.ui(15, .semibold)).foregroundStyle(peer.reachable ? K.l1 : K.l2)
                        StatusDot(on: peer.reachable)
                        Text(peer.reachable ? "Reachable" : "Offline").font(K.ui(12)).foregroundStyle(peer.reachable ? K.green : K.l2)
                    }
                    HStack(spacing: 0) {
                        Text(peer.lastAddr ?? "").font(K.mono(11.5))
                        Text(" · " + (peer.reachable ? "connected" : "seen \(Fmt.ago(peer.lastSeen.flatMap(Fmt.date)))"))
                    }
                    .font(K.ui(11.5)).foregroundStyle(K.l2)
                }
                Spacer(minLength: 8)
                Button {
                    refreshing = true
                    Task { await store.refresh(); refreshing = false; store.flash("\(peer.name) is up to date") }
                } label: {
                    HStack(spacing: 4) {
                        Image(systemName: "arrow.clockwise").rotationEffect(.degrees(refreshing ? 360 : 0))
                            .animation(refreshing ? .linear(duration: 0.9).repeatForever(autoreverses: false) : .default, value: refreshing)
                        Text(refreshing ? "Refreshing…" : "Refresh")
                    }
                }
                .buttonStyle(.k(.secondary, height: 26)).disabled(!peer.reachable)
                Button("Unpair…") { confirmUnpair = true }.buttonStyle(.k(.destructive, height: 26))
            }
            .padding(.horizontal, 18).padding(.vertical, 16)
            Divider05()
            LazyVGrid(columns: [GridItem(.flexible(), alignment: .top), GridItem(.flexible(), alignment: .top)], spacing: 0) {
                ForEach(rows) { r in
                    KVRow(key: r.key) {
                        VStack(alignment: .leading, spacing: 3) {
                            if let c = r.copy { CopyableMono(text: c, label: "fingerprint") }
                            else { Text(r.value).font(r.mono ? K.mono(11.5) : K.ui(12.5)).textSelection(.enabled) }
                            if let s = r.sub { Text(s).font(K.ui(11)).foregroundStyle(K.l2) }
                        }
                    }
                }
            }
            .padding(.horizontal, 4).padding(.vertical, 8)
            Divider05()
            HStack(spacing: 14) {
                Text("When \(peer.name) sends a task to this Mac").font(K.ui(12.5))
                Spacer()
                Segmented(options: [("ask", "Ask"), ("auto", "Auto")], selection: $policy)
            }
            .padding(.horizontal, 18).padding(.vertical, 12)
            if policy == "auto" {
                Label("Auto runs every task from \(peer.name) as \(NSUserName()) without asking. Use it only for machines you fully control.",
                      systemImage: "exclamationmark.triangle.fill")
                    .font(K.ui(12)).foregroundStyle(K.orange)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 18).padding(.bottom, 14)
                    .transition(.opacity.combined(with: .offset(y: -4)))
            }
        }
        .card(shadow: true)
        .animation(K.layout, value: policy)
        .onAppear { policy = peer.policy ?? "ask" }
        .onChange(of: peer.policy) { _, p in policy = p ?? "ask" }
        .onChange(of: policy) { _, p in
            if p != peer.policy { Task { await store.setPolicy(peer, p) } }
        }
        .confirmationDialog("Unpair \(peer.name)?", isPresented: $confirmUnpair) {
            Button("Unpair", role: .destructive) { Task { await store.unpair(peer) } }
        } message: {
            Text("\(peer.name) can no longer send tasks here or receive yours. To use it again, pair with a new code on both machines.")
        }
    }

    private var rows: [ManifestRow] {
        var r: [ManifestRow] = [.init(key: "Fingerprint", value: peer.display, copy: peer.display)]
        guard let m = peer.manifest else { return r }
        r.append(.init(key: "OS", value: "\(Fmt.osLong(m.os)) · \(m.arch)"))
        r.append(.init(key: "RAM", value: Fmt.gb(m.ramGb)))
        for g in m.gpus ?? [] { r.append(.init(key: "GPU", value: g.name, sub: "\(Fmt.gb(g.freeVramGb)) free of \(Fmt.gb(g.vramGb))")) }
        if (m.gpus ?? []).isEmpty { r.append(.init(key: "GPU", value: "no NVIDIA GPU")) }
        r.append(.init(key: "Disk", value: "\(Fmt.gb(m.freeDiskGb)) free"))
        r.append(.init(key: "Python", value: (m.python ?? []).joined(separator: ", ").ifEmpty("none found"), mono: true))
        let pk = (m.packages ?? [:]).sorted { $0.key < $1.key }.map { "\($0.key) \($0.value)" }.joined(separator: " · ")
        r.append(.init(key: "Packages", value: pk.ifEmpty("none of torch, numpy"), mono: true))
        if let v = m.kithVer { r.append(.init(key: "Kith", value: v, mono: true)) }
        return r
    }
}

private struct OfflinePeerCard: View {
    var peer: Peer
    @Environment(KithStore.self) private var store
    @State private var confirmUnpair = false

    var body: some View {
        HStack(spacing: 12) {
            DeviceGlyph(symbol: peer.symbol, size: 40, dim: true)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 7) {
                    Text(peer.name).font(K.ui(15, .semibold)).foregroundStyle(K.l2)
                    StatusDot(on: false)
                    Text("Offline").font(K.ui(12)).foregroundStyle(K.l2)
                }
                HStack(spacing: 0) {
                    Text("Last seen \(Fmt.ago(peer.lastSeen.flatMap(Fmt.date)))")
                    if let a = peer.lastAddr { Text(" at "); Text(a).font(K.mono(11.5)) }
                    Text(" · " + peer.display).font(K.mono(11.5))
                }
                .font(K.ui(11.5)).foregroundStyle(K.l2).lineLimit(1)
            }
            Spacer(minLength: 8)
            Button("Unpair…") { confirmUnpair = true }.buttonStyle(.k(.destructive, height: 26))
        }
        .padding(.horizontal, 18).padding(.vertical, 16)
        .card()
        .confirmationDialog("Unpair \(peer.name)?", isPresented: $confirmUnpair) {
            Button("Unpair", role: .destructive) { Task { await store.unpair(peer) } }
        } message: {
            Text("To use it again, pair with a new code on both machines.")
        }
    }
}

/// `kith doctor`, one check at a time.
struct DiagnosticsView: View {
    @Environment(KithStore.self) private var store
    @State private var open: Set<String> = []

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                HStack(spacing: 12) {
                    Text(headline).font(K.ui(12.5)).foregroundStyle(K.l2).contentTransition(.numericText())
                    Spacer()
                    Button("Run checks") { Task { await store.runDiagnostics() } }
                        .buttonStyle(.kPrimary).disabled(store.checking)
                }
                VStack(spacing: 0) {
                    ForEach(Array(store.checks.enumerated()), id: \.element.id) { i, c in
                        row(c, shown: i < store.checksShown)
                        if i < store.checks.count - 1 { Divider05() }
                    }
                    if store.checks.isEmpty {
                        HStack { KSpinner(); Text("Running checks…").foregroundStyle(K.l2) }.padding(20)
                    }
                }
                .card()
            }
            .padding(.horizontal, 28).padding(.top, 20).padding(.bottom, 32)
            .frame(maxWidth: 820)
            .frame(maxWidth: .infinity)
        }
        .task { if store.checks.isEmpty { await store.runDiagnostics() } }
    }

    private var headline: String {
        if store.checking { return "Running \(min(store.checksShown + 1, max(store.checks.count, 1))) of \(max(store.checks.count, 1)) checks…" }
        let c = store.checks
        guard !c.isEmpty else { return "Checks the daemon, network, discovery and each peer." }
        func n(_ s: String) -> Int { c.filter { $0.status == s }.count }
        var parts = ["\(n("ok")) ok"]
        if n("warn") > 0 { parts.append("\(n("warn")) warning\(n("warn") == 1 ? "" : "s")") }
        if n("fail") > 0 { parts.append("\(n("fail")) failing") }
        if n("info") > 0 { parts.append("\(n("info")) note\(n("info") == 1 ? "" : "s")") }
        return parts.joined(separator: ", ") + " · checked " + Fmt.ago(store.checkedAt)
    }

    private func row(_ c: Check, shown: Bool) -> some View {
        let (icon, color): (String, Color) = switch c.status {
        case "ok": ("checkmark.circle.fill", K.green)
        case "warn": ("exclamationmark.triangle.fill", K.orange)
        case "fail": ("xmark.circle.fill", K.red)
        default: ("info.circle", K.l2)
        }
        let isOpen = open.contains(c.id)
        return VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 12) {
                Group {
                    if shown {
                        Image(systemName: icon).foregroundStyle(color).font(.system(size: 15))
                            .transition(.scale.combined(with: .opacity))
                    } else {
                        KSpinner(size: 13)
                    }
                }
                .frame(width: 22)
                Text(checkTitle(c.name)).font(K.ui(13, .medium)).frame(width: 170, alignment: .leading).lineLimit(1)
                Text(shown ? c.detail : "Checking…").font(K.ui(12)).foregroundStyle(K.l2)
                    .frame(maxWidth: .infinity, alignment: .leading).fixedSize(horizontal: false, vertical: true)
                if shown, c.fix != nil {
                    Button {
                        withAnimation(.spring(response: 0.3, dampingFraction: 0.8)) {
                            if isOpen { open.remove(c.id) } else { open.insert(c.id) }
                        }
                    } label: {
                        HStack(spacing: 2) {
                            Text("How to fix")
                            Image(systemName: "chevron.right").font(.system(size: 10, weight: .semibold))
                                .rotationEffect(.degrees(isOpen ? 90 : 0))
                        }
                        .font(K.ui(12)).foregroundStyle(K.accText)
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 16).padding(.vertical, 11)
            if shown, isOpen, let fix = c.fix {
                Text(fix).font(K.ui(12)).textSelection(.enabled)
                    .padding(.horizontal, 12).padding(.vertical, 10)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(RoundedRectangle(cornerRadius: 8).fill(K.bg2))
                    .padding(.leading, 50).padding(.trailing, 16).padding(.bottom, 12)
                    .transition(.opacity.combined(with: .offset(y: 6)))
            }
        }
        .animation(K.layout, value: shown)
    }
}

/// Doctor check names, as people write them.
func checkTitle(_ n: String) -> String {
    let known = ["vpn": "VPN", "mdns": "mDNS", "lan address": "LAN address", "local network": "Local Network permission",
                 "firewall mdns": "Firewall (mDNS)", "network profile": "Network profile"]
    if let k = known[n.lowercased()] { return k }
    if n.hasPrefix("peer ") { return "Peer " + n.dropFirst(5) }
    return n.prefix(1).uppercased() + n.dropFirst()
}
