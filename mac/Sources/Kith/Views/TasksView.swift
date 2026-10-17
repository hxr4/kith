import SwiftUI

enum TaskFilter: Hashable { case all, sent, received }

/// Every task in both directions, filterable and searchable.
struct TasksView: View {
    var ns: Namespace.ID
    @Environment(KithStore.self) private var store
    @State private var filter: TaskFilter = .all
    @State private var appeared = false

    private var rows: [KTask] {
        let q = store.search.trimmingCharacters(in: .whitespaces).lowercased()
        return store.tasks.filter { t in
            (filter == .all || (filter == .sent) == t.sent) &&
                (q.isEmpty || "\(t.name) \(t.project) \(t.peerLabel) \(t.state) \(t.id)".lowercased().contains(q))
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Segmented(options: [(.all, "All"), (.sent, "Sent"), (.received, "Received")], selection: $filter,
                      counts: [.all: store.tasks.count, .sent: store.tasks.filter(\.sent).count,
                               .received: store.tasks.filter { !$0.sent }.count])
                .padding(.horizontal, 22).padding(.top, 14).padding(.bottom, 10)
            HStack(spacing: 12) {
                Text("State").frame(width: 128, alignment: .leading)
                Text("Name").frame(maxWidth: .infinity, alignment: .leading)
                Text("Project").frame(width: 150, alignment: .leading)
                Text("Peer").frame(width: 130, alignment: .leading)
                Text("Attempt").frame(width: 60, alignment: .leading)
                Text("Updated").frame(width: 88, alignment: .trailing)
            }
            .font(K.ui(11, .medium)).foregroundStyle(K.l2)
            .padding(.horizontal, 30).padding(.bottom, 6)
            .overlay(alignment: .bottom) { Divider05().padding(.horizontal, 18) }
            ScrollView {
                LazyVStack(spacing: 0) {
                    ForEach(Array(rows.enumerated()), id: \.element.id) { i, t in
                        TaskRow(task: t, ns: ns)
                            .opacity(appeared ? 1 : 0)
                            .offset(y: appeared ? 0 : 6)
                            .animation(K.layout.delay(Double(min(i, 10)) * 0.03), value: appeared)
                            .transition(.opacity.combined(with: .offset(y: 6)))
                    }
                    if rows.isEmpty { empty }
                }
                .padding(.horizontal, 18).padding(.top, 4).padding(.bottom, 24)
                .animation(K.layout, value: rows.map(\.id))
                .animation(K.layout, value: filter)
            }
        }
        .onAppear { appeared = true }
    }

    @ViewBuilder private var empty: some View {
        let q = store.search.trimmingCharacters(in: .whitespaces)
        VStack(spacing: 8) {
            if !q.isEmpty {
                Text("No tasks match “\(q)”.")
            } else {
                Text("No tasks yet.")
                Text("Send one with New task, `kith run` in a terminal, or from an AI agent over MCP.").font(K.ui(12)).foregroundStyle(K.l3)
            }
        }
        .font(K.ui(13)).foregroundStyle(K.l2)
        .frame(maxWidth: .infinity).padding(40)
    }
}

private struct TaskRow: View {
    var task: KTask
    var ns: Namespace.ID
    @Environment(KithStore.self) private var store

    var body: some View {
        HStack(spacing: 12) {
            StateChip(state: task.state)
                .matchedGeometryEffect(id: "chip-\(task.id)", in: ns)
                .frame(width: 128, alignment: .leading)
            Text(task.name).font(K.ui(13, .medium)).lineLimit(1)
                .matchedGeometryEffect(id: "title-\(task.id)", in: ns, properties: .position)
                .frame(maxWidth: .infinity, alignment: .leading)
            Text(task.project.ifEmpty("—")).foregroundStyle(K.l2).lineLimit(1).frame(width: 150, alignment: .leading)
            HStack(spacing: 4) {
                Image(systemName: task.sent ? "arrow.up.right" : "arrow.down.left").font(.system(size: 10, weight: .semibold)).foregroundStyle(K.l3)
                Text(task.peerLabel).lineLimit(1)
            }
            .foregroundStyle(K.l2).frame(width: 130, alignment: .leading)
            Text("\(task.attempt)").font(K.mono(11.5)).foregroundStyle(K.l2).frame(width: 60, alignment: .leading)
            TimelineView(.periodic(from: .now, by: 15)) { tl in
                Text(Fmt.ago(task.updated, now: tl.date)).font(K.ui(12)).foregroundStyle(K.l2)
            }
            .frame(width: 88, alignment: .trailing)
        }
        .font(K.ui(13))
        .padding(.horizontal, 12).frame(height: 34)
        .hoverFill()
        .onTapGesture {
            if task.needsApproval { store.open(.approvals) } else { store.openTask(task.id) }
        }
        .contextMenu {
            if !task.ended {
                Button("Cancel") { Task { await store.cancel(task) } }
            }
            Button("Copy ID") { store.copy(task.id, label: "task ID") }
            if let dir = task.sent ? task.projectDir : task.wsDir {
                Divider()
                Button("Reveal \(task.sent ? "project folder" : "workspace")") { store.reveal(dir) }
            }
        }
    }
}
