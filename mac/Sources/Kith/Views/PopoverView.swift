import SwiftUI

/// The menu bar popover (360 pt): peers, approvals, running, recent.
struct PopoverView: View {
    @Environment(KithStore.self) private var store
    @Environment(\.openSettings) private var openSettings

    private var off: Bool { store.loaded && !store.running }
    private var noPeers: Bool { store.running && store.peers.isEmpty }

    var body: some View {
        VStack(spacing: 0) {
            header
            if !store.loaded {
                HStack { KSpinner(); Text("Connecting to kithd…").font(K.ui(12)).foregroundStyle(K.l2) }
                    .frame(maxWidth: .infinity).padding(22)
            } else if off {
                offState
            } else if noPeers {
                noPeersState
            } else {
                peerList
                if !store.pending.isEmpty { approvals }
                if !store.active.isEmpty { running }
                if !store.finished.isEmpty { recent }
            }
            footer
        }
        .frame(width: 360)
        .font(K.ui(13))
        .foregroundStyle(K.l1)
        .animation(K.layout, value: store.pending.map(\.id))
        .animation(K.layout, value: store.active.map(\.id))
        .animation(K.layout, value: off)
        .task {
            store.interactive = true
            await store.refresh()
            while !Task.isCancelled {
                await store.refreshTails()
                try? await Task.sleep(for: .milliseconds(1200))
            }
        }
        .onDisappear { store.interactive = AppDelegate.shared.map { _ in NSApp.windows.contains { $0.isVisible && $0.title == "Kith" } } ?? false }
    }

    // MARK: Header

    private var header: some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 1) {
                Text(store.thisName).font(K.ui(13, .semibold)).lineLimit(1)
                HStack(spacing: 5) {
                    Circle().fill(daemonColor).frame(width: 7, height: 7)
                    Text(daemonLabel).font(K.ui(11.5)).foregroundStyle(K.l2).contentTransition(.interpolate)
                }
            }
            Spacer(minLength: 0)
            Menu {
                Button("Open Kith") { AppDelegate.shared?.showMain() }
                Button("Settings…") { NSApp.activate(); openSettings() }
                Divider()
                if store.running {
                    Button("Restart daemon") { Task { await store.daemon("restart") } }
                    Button("Stop daemon") { Task { await store.daemon("stop") } }
                } else {
                    Button("Start daemon") { Task { await store.daemon("start") } }
                }
                Divider()
                Button("Quit Kith") { NSApp.terminate(nil) }
            } label: {
                Image(systemName: "ellipsis").font(.system(size: 13, weight: .medium)).foregroundStyle(K.l2)
                    .frame(width: 26, height: 26).contentShape(Circle())
            }
            .menuStyle(.button).buttonStyle(.plain).menuIndicator(.hidden).fixedSize()
        }
        .padding(.leading, 14).padding(.trailing, 10).padding(.vertical, 11)
    }

    private var daemonLabel: String {
        if let b = store.daemonBusy { return b.replacingOccurrences(of: "…", with: " kithd…") }
        if off { return "Kith isn't running" }
        if !store.loaded { return "Connecting…" }
        if noPeers { return "Running · no peers yet" }
        return "Running · \(store.reachable.count) of \(store.peers.count) peer\(store.peers.count == 1 ? "" : "s") reachable"
    }

    private var daemonColor: Color {
        store.daemonBusy != nil ? K.orangeDot : off || !store.loaded ? K.l3 : K.greenDot
    }

    // MARK: Empty states

    private var offState: some View {
        VStack(spacing: 10) {
            Image(systemName: "power").font(.system(size: 24, weight: .light)).foregroundStyle(K.l3)
            Text(offText).font(K.ui(12.5)).foregroundStyle(K.l2).multilineTextAlignment(.center).frame(maxWidth: 270)
            Button(store.daemonBusy ?? "Start Kith") { Task { await store.daemon("start") } }
                .buttonStyle(.k(.primary, height: 30, fullWidth: true))
                .disabled(store.daemonBusy != nil)
                .padding(.top, 4)
        }
        .padding(.horizontal, 22).padding(.top, 22).padding(.bottom, 20)
        .overlay(alignment: .top) { Divider05() }
        .transition(.opacity)
    }

    private var offText: String {
        let names = store.peers.map(\.name)
        let who = names.isEmpty ? "Paired machines" : names.count == 1 ? names[0] : "Your peers"
        return "The Kith daemon is stopped. \(who) can't reach this Mac, and tasks you send won't run."
    }

    private var noPeersState: some View {
        VStack(spacing: 10) {
            HStack(spacing: 6) {
                Image(systemName: "laptopcomputer").font(.system(size: 18))
                Rectangle().fill(.clear).frame(width: 56, height: 1.5)
                    .overlay(Line().stroke(K.l3, style: StrokeStyle(lineWidth: 1.5, dash: [3, 3])))
                RoundedRectangle(cornerRadius: 6).strokeBorder(K.l3, lineWidth: 1.5).frame(width: 22, height: 22)
            }
            .foregroundStyle(K.l3)
            Text("No machines paired. Pair once with a 6-character code; after that they find each other on this network.")
                .font(K.ui(12.5)).foregroundStyle(K.l2).multilineTextAlignment(.center).frame(maxWidth: 280)
            Button("Pair a machine") { store.pair() }
                .buttonStyle(.k(.primary, height: 30, fullWidth: true)).padding(.top, 4)
        }
        .padding(.horizontal, 22).padding(.top, 22).padding(.bottom, 20)
        .overlay(alignment: .top) { Divider05() }
    }

    // MARK: Sections

    private var peerList: some View {
        VStack(spacing: 0) {
            ForEach(store.peers) { p in
                HStack(spacing: 8) {
                    Image(systemName: p.symbol).font(.system(size: 16)).symbolRenderingMode(.hierarchical)
                        .foregroundStyle(p.reachable ? K.l1 : K.l3).frame(width: 26)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(p.name).font(K.ui(12.5, .medium))
                        Text(capsLine(p)).font(K.mono(10.5)).foregroundStyle(K.l2).lineLimit(1).truncationMode(.tail)
                    }
                    Spacer(minLength: 4)
                    HStack(spacing: 5) {
                        StatusDot(on: p.reachable)
                        Text(p.reachable ? "reachable" : "offline").font(K.ui(11))
                            .foregroundStyle(p.reachable ? K.green : K.l2)
                    }
                }
                .padding(.horizontal, 8).padding(.vertical, 5)
                .hoverFill()
                .onTapGesture { store.open(.machines) }
            }
        }
        .padding(.horizontal, 6).padding(.vertical, 8)
        .overlay(alignment: .top) { Divider05() }
    }

    private func capsLine(_ p: Peer) -> String {
        if p.reachable, let m = p.manifest { return m.summary }
        let os = p.manifest.map { Fmt.osName($0.os) + " · " } ?? ""
        if let seen = p.lastSeen.flatMap(Fmt.date) { return "\(os)last seen \(Fmt.agoShort(seen)) ago" }
        return os + "not seen yet"
    }

    private var approvals: some View {
        VStack(alignment: .leading, spacing: 7) {
            Text("Needs approval").sectionLabel().padding(.horizontal, 2)
            ForEach(store.pending.prefix(2)) { t in
                ApprovalCard(task: t, compact: true)
                    .transition(.asymmetric(insertion: .opacity.combined(with: .offset(y: 6)),
                                            removal: .move(edge: .trailing).combined(with: .opacity)))
            }
            if store.pending.count > 2 {
                Button("\(store.pending.count - 2) more waiting…") { store.open(.approvals) }
                    .buttonStyle(.plain).font(K.ui(11.5)).foregroundStyle(K.accText).padding(.horizontal, 2)
            }
        }
        .padding(.horizontal, 12).padding(.top, 10).padding(.bottom, 12)
        .overlay(alignment: .top) { Divider05() }
    }

    private var running: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Running").sectionLabel().padding(.horizontal, 8).padding(.bottom, 4)
            ForEach(store.active.prefix(3)) { t in
                RunningRow(task: t, tail: store.tails[t.id])
                    .transition(.opacity.combined(with: .offset(y: 6)))
                    .onTapGesture { store.openTask(t.id) }
            }
        }
        .padding(.horizontal, 6).padding(.top, 10).padding(.bottom, 8)
        .overlay(alignment: .top) { Divider05() }
    }

    private var recent: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Recent").sectionLabel().padding(.horizontal, 8).padding(.bottom, 4)
            ForEach(store.finished.prefix(3)) { t in
                HStack(spacing: 8) {
                    OutcomeIcon(state: t.state).frame(width: 20)
                    Text("\(Text(t.name).font(K.ui(12.5, .medium)))  \(Text(outcomeDetail(t)).font(K.ui(11.5)).foregroundStyle(K.l2))")
                        .lineLimit(1).truncationMode(.tail)
                    Spacer(minLength: 4)
                    Text(Fmt.agoShort(t.updated)).font(K.ui(11)).foregroundStyle(K.l3)
                }
                .padding(.horizontal, 8).padding(.vertical, 4)
                .hoverFill()
                .onTapGesture { store.openTask(t.id) }
            }
        }
        .padding(.horizontal, 6).padding(.top, 10).padding(.bottom, 8)
        .overlay(alignment: .top) { Divider05() }
    }

    private var footer: some View {
        VStack(spacing: 0) {
            if off {
                FooterItem(label: "Open Kith", key: "") { AppDelegate.shared?.showMain() }
                FooterItem(label: "Quit Kith", key: "⌘Q") { NSApp.terminate(nil) }
            } else {
                FooterItem(label: "New task…", key: "⌘N") { store.newTask() }
                FooterItem(label: "Open Kith", key: "⌘O") { AppDelegate.shared?.showMain() }
            }
        }
        .padding(6)
        .overlay(alignment: .top) { Divider05() }
    }
}

func outcomeDetail(_ t: KTask) -> String {
    switch t.state {
    case TaskState.rejected: return [t.code, t.detail].compactMap { $0 }.joined(separator: " · ")
    case TaskState.failed: return t.detail ?? t.code ?? "failed"
    case TaskState.cancelled: return t.detail ?? "cancelled"
    default: return t.detail ?? "on \(t.peerLabel)"
    }
}

struct OutcomeIcon: View {
    var state: String
    var body: some View {
        let (sym, c): (String, Color) = switch state {
        case TaskState.completed: ("checkmark.circle.fill", K.green)
        case TaskState.failed: ("xmark.circle.fill", K.red)
        case TaskState.rejected: ("nosign", K.red)
        default: ("minus.circle.fill", K.l3)
        }
        Image(systemName: sym).font(.system(size: 13)).foregroundStyle(c)
    }
}

struct RunningRow: View {
    var task: KTask
    var tail: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 8) {
                StateChip(state: task.state)
                Text(task.name).font(K.ui(12.5, .medium)).lineLimit(1)
                Spacer(minLength: 4)
                Elapsed(since: task.started ?? task.created).font(K.mono(11)).foregroundStyle(K.l2)
            }
            Text(meta).font(K.ui(11)).foregroundStyle(K.l2).padding(.top, 1)
            if let tail {
                Text(tail).font(K.mono(10.5)).foregroundStyle(K.l1).lineLimit(1).truncationMode(.tail)
                    .id(tail).transition(.opacity.animation(.easeOut(duration: 0.12)))
            }
        }
        .padding(.horizontal, 8).padding(.vertical, 6)
        .hoverFill()
    }

    private var meta: String {
        var parts = [task.sent ? (task.state == TaskState.transferring ? "to \(task.peerLabel)" : "on \(task.peerLabel)") : "on this Mac · from \(task.peerLabel)"]
        if task.attempt > 1 { parts.append("attempt \(task.attempt)") }
        if let d = task.detail, !d.isEmpty { parts.append(d) }
        return parts.joined(separator: " · ")
    }
}

/// Live elapsed clock with rolling digits.
struct Elapsed: View {
    var since: Date?
    var until: Date? = nil
    var body: some View {
        TimelineView(.periodic(from: .now, by: 1)) { tl in
            let s = (until ?? tl.date).timeIntervalSince(since ?? tl.date)
            Text(Fmt.clock(s)).monospacedDigit().contentTransition(.numericText()).animation(.snappy, value: Int(s))
        }
    }
}

struct FooterItem: View {
    var label: String
    var key: String
    var action: () -> Void
    var body: some View {
        HStack {
            Text(label).font(K.ui(12.5))
            Spacer()
            Text(key).font(K.ui(11.5)).foregroundStyle(K.l3)
        }
        .padding(.horizontal, 8).frame(height: 26)
        .hoverFill(radius: 6)
        .onTapGesture(perform: action)
    }
}

struct Line: Shape {
    func path(in r: CGRect) -> Path {
        var p = Path()
        p.move(to: CGPoint(x: r.minX, y: r.midY))
        p.addLine(to: CGPoint(x: r.maxX, y: r.midY))
        return p
    }
}
