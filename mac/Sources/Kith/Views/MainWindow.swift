import SwiftUI
import AppKit

/// Sidebar + content, as in the design's "Main window" frames.
struct MainWindow: View {
    @Environment(KithStore.self) private var store
    @Namespace private var ns
    @FocusState private var searchFocused: Bool
    @State private var dropping = false

    var body: some View {
        @Bindable var store = store
        HStack(spacing: 0) {
            Sidebar()
                .frame(width: 212)
            Rectangle().fill(K.sep).frame(width: 0.5)
            VStack(spacing: 0) {
                header
                Divider05()
                if store.loaded && !store.running { DaemonOffBanner() }
                if store.configDirty && store.running { RestartBanner() }
                content
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .background(K.bg)
        }
        .ignoresSafeArea()
        .tint(K.acc)
        .font(K.ui(13))
        .foregroundStyle(K.l1)
        .frame(minWidth: 900, minHeight: 580)
        .overlay(alignment: .bottom) {
            if let t = store.toast {
                ToastView(toast: t).padding(.bottom, 18)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
                    .id(t.id)
            }
        }
        .overlay {
            if dropping {
                RoundedRectangle(cornerRadius: 14).strokeBorder(K.acc, style: StrokeStyle(lineWidth: 2, dash: [6, 4]))
                    .background(K.accSoft.clipShape(RoundedRectangle(cornerRadius: 14)))
                    .overlay(Label("Drop a kith.toml to send it", systemImage: "doc.badge.arrow.up").font(K.ui(15, .medium)).foregroundStyle(K.accText))
                    .padding(8).allowsHitTesting(false).transition(.opacity)
            }
        }
        .dropDestination(for: URL.self) { urls, _ in
            guard let f = urls.lazy.compactMap(TaskFile.resolve).first else { return false }
            store.newTask(f)
            return true
        } isTargeted: { t in withAnimation(K.snappy) { dropping = t } }
        .sheet(isPresented: $store.showNewTask) { NewTaskSheet(initialFile: store.newTaskFile).environment(store) }
        .sheet(isPresented: $store.showPairing) { PairingSheet().environment(store) }
        .background {
            // ⌘F focuses search; ⌘. cancels the open task.
            Button("") { searchFocused = true }.keyboardShortcut("f").hidden()
        }
    }

    // MARK: Header

    private var header: some View {
        @Bindable var store = store
        return HStack(spacing: 10) {
            if store.openTaskID != nil {
                Button { withAnimation(K.layout) { store.openTaskID = nil } } label: {
                    Image(systemName: "chevron.left").font(.system(size: 13, weight: .semibold)).foregroundStyle(K.l2)
                        .frame(width: 28, height: 28).background(Circle().fill(K.fill))
                }
                .buttonStyle(.plain)
                .keyboardShortcut("[", modifiers: .command)
                .transition(.move(edge: .leading).combined(with: .opacity))
            }
            VStack(alignment: .leading, spacing: 0) {
                Text(title).font(K.ui(14, .semibold)).lineLimit(1)
                Text(subtitle).font(K.ui(11)).foregroundStyle(K.l2).lineLimit(1).contentTransition(.numericText())
            }
            Spacer(minLength: 12)
            Button { store.newTask() } label: {
                HStack(spacing: 4) {
                    Image(systemName: "plus").font(.system(size: 12, weight: .semibold)).foregroundStyle(K.l2)
                    Text("New task").font(K.ui(12.5, .medium))
                }
                .padding(.leading, 9).padding(.trailing, 12).frame(height: 28)
                .glassEffect(.regular.interactive(), in: Capsule())
            }
            .buttonStyle(.plain)
            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass").font(.system(size: 12)).foregroundStyle(K.l3)
                TextField("Search tasks", text: $store.search)
                    .textFieldStyle(.plain).font(K.ui(12.5)).focused($searchFocused)
                    .onChange(of: store.search) { _, q in
                        if !q.isEmpty && (store.screen != .tasks || store.openTaskID != nil) {
                            withAnimation(K.smooth) { store.screen = .tasks; store.openTaskID = nil }
                        }
                    }
                if store.search.isEmpty {
                    Text("⌘F").font(K.ui(10.5)).foregroundStyle(K.l3)
                } else {
                    Button { store.search = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(K.l3) }
                        .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 10).frame(width: 200, height: 28)
            .background(Capsule().fill(K.fill))
        }
        .padding(.leading, 16).padding(.trailing, 14)
        .frame(height: 52)
        .animation(K.layout, value: store.openTaskID)
    }

    private var title: String {
        if let id = store.openTaskID { return store.task(id)?.name ?? "Task" }
        return store.screen.title
    }

    private var subtitle: String {
        if store.openTaskID != nil { return "Tasks" }
        switch store.screen {
        case .overview:
            let other = store.peers.first(where: \.reachable)?.name ?? store.peers.first?.name
            return other.map { "\(store.thisName) ↔ \($0)" } ?? store.thisName
        case .tasks:
            let a = store.tasks.filter { !$0.ended }.count
            return "\(store.tasks.count) task\(store.tasks.count == 1 ? "" : "s") · \(a) in progress"
        case .approvals:
            return store.pending.isEmpty ? "Nothing waiting" : "\(store.pending.count) waiting"
        case .machines:
            return "\(store.reachable.count) of \(store.peers.count) reachable"
        case .diagnostics:
            return "Network and daemon health"
        }
    }

    // MARK: Content

    @ViewBuilder
    private var content: some View {
        ZStack {
            if let id = store.openTaskID {
                TaskDetailView(taskID: id, ns: ns)
                    .id(id) // fresh log stream and state per task
                    .transition(.opacity)
            } else {
                switch store.screen {
                case .overview: OverviewView(ns: ns).transition(.opacity)
                case .tasks: TasksView(ns: ns).transition(.opacity)
                case .approvals: ApprovalsView().transition(.opacity)
                case .machines: MachinesView().transition(.opacity)
                case .diagnostics: DiagnosticsView().transition(.opacity)
                }
            }
        }
        .animation(K.smooth, value: store.screen)
    }
}

// MARK: - Sidebar

private struct Sidebar: View {
    @Environment(KithStore.self) private var store

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Spacer().frame(height: 52)
            VStack(spacing: 1) {
                ForEach(Screen.allCases, id: \.self) { s in
                    let on = store.screen == s
                    HStack(spacing: 8) {
                        Image(systemName: s.symbol).font(.system(size: 13))
                            .foregroundStyle(on ? K.acc : K.l2).frame(width: 20)
                        Text(s.title).font(K.ui(13, on ? .medium : .regular))
                        Spacer(minLength: 4)
                        if s == .approvals && !store.pending.isEmpty {
                            Text("\(store.pending.count)").font(K.ui(11, .semibold)).foregroundStyle(K.l2)
                                .padding(.horizontal, 5).frame(minWidth: 18, minHeight: 16)
                                .background(Capsule().fill(K.fill2))
                                .contentTransition(.numericText())
                                .transition(.scale.combined(with: .opacity))
                        }
                    }
                    .padding(.horizontal, 8).frame(height: 28)
                    .hoverFill(selected: on)
                    .onTapGesture { store.open(s) }
                }
            }
            Spacer()
            if !store.peers.isEmpty {
                Text("Peers").font(K.ui(11, .semibold)).foregroundStyle(K.l3).padding(.horizontal, 8).padding(.bottom, 6)
                ForEach(store.peers) { p in
                    HStack(spacing: 8) {
                        StatusDot(on: p.reachable)
                        Text(p.name).font(K.ui(12)).foregroundStyle(p.reachable ? K.l1 : K.l2).lineLimit(1)
                        Spacer(minLength: 4)
                        Text(p.reachable ? "online" : Fmt.agoShort(p.lastSeen.flatMap(Fmt.date)))
                            .font(K.ui(11)).foregroundStyle(p.reachable ? K.l2 : K.l3)
                    }
                    .padding(.horizontal, 8).padding(.vertical, 4)
                    .contentShape(Rectangle())
                    .onTapGesture { store.open(.machines) }
                }
            }
            Button { store.pair() } label: {
                Label("Pair a machine", systemImage: "link.badge.plus").font(K.ui(12)).foregroundStyle(K.l2)
            }
            .buttonStyle(.plain).padding(.horizontal, 8).padding(.top, 8)
        }
        .padding(.horizontal, 10).padding(.bottom, 14)
        .frame(maxHeight: .infinity, alignment: .top)
        .background(VisualEffect(material: .sidebar).ignoresSafeArea())
        .animation(K.layout, value: store.pending.count)
    }
}

struct VisualEffect: NSViewRepresentable {
    var material: NSVisualEffectView.Material
    func makeNSView(context: Context) -> NSVisualEffectView {
        let v = NSVisualEffectView()
        v.material = material
        v.blendingMode = .behindWindow
        v.state = .followsWindowActiveState
        return v
    }
    func updateNSView(_ v: NSVisualEffectView, context: Context) { v.material = material }
}

// MARK: - Banners

private struct DaemonOffBanner: View {
    @Environment(KithStore.self) private var store
    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: "power").foregroundStyle(K.l2)
            Text("The Kith daemon isn't running. Peers can't reach this Mac and nothing below updates.")
                .font(K.ui(12.5)).foregroundStyle(K.l2)
            Spacer(minLength: 8)
            Button(store.daemonBusy ?? "Start Kith") { Task { await store.daemon("start") } }
                .buttonStyle(.k(.primary, height: 24)).disabled(store.daemonBusy != nil)
        }
        .padding(.horizontal, 16).padding(.vertical, 9)
        .background(K.bg2)
        .overlay(alignment: .bottom) { Divider05() }
        .transition(.move(edge: .top).combined(with: .opacity))
    }
}

private struct RestartBanner: View {
    @Environment(KithStore.self) private var store
    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: "arrow.counterclockwise").foregroundStyle(K.accText)
            Text("Settings changed. Restart the daemon to apply them; running tasks become INTERRUPTED and resume by their policy.")
                .font(K.ui(12.5)).foregroundStyle(K.l2)
            Spacer(minLength: 8)
            Button("Later") { store.configDirty = false }.buttonStyle(.k(.secondary, height: 24))
            Button(store.daemonBusy ?? "Restart") { Task { await store.daemon("restart") } }
                .buttonStyle(.k(.primary, height: 24)).disabled(store.daemonBusy != nil)
        }
        .padding(.horizontal, 16).padding(.vertical, 9)
        .background(K.accSoft)
        .overlay(alignment: .bottom) { Divider05() }
    }
}
