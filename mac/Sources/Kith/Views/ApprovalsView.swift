import SwiftUI

/// Loads the full task view (argv, inputs, requirements) for a list row.
@MainActor @Observable
final class TaskDetails {
    static let shared = TaskDetails()
    var byID: [String: KTask] = [:]

    func load(_ id: String) async {
        if let t: KTask = try? await IPC.call("task", ["id": id], timeout: 10) { byID[id] = t }
    }
}

/// "HARI-PC wants to run render-batch" with Approve / Deny. Compact in the
/// popover, full on the Approvals screen.
struct ApprovalCard: View {
    var task: KTask
    var compact = false
    @Environment(KithStore.self) private var store
    @State private var details = TaskDetails.shared
    @State private var argvOpen = false
    @State private var busy = false

    private var full: KTask { details.byID[task.id] ?? task }
    private var argv: String { full.argv.map(Fmt.argv) ?? "loading…" }
    private var inputBytes: Int64 { full.inputs?.reduce(0) { $0 + $1.size } ?? 0 }

    var body: some View {
        Group { if compact { compactBody } else { fullBody } }
            .task(id: task.id) { await details.load(task.id) }
    }

    // MARK: Popover

    private var compactBody: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .top, spacing: 10) {
                Text("\(Text(task.peerLabel).fontWeight(.semibold)) wants to run \(Text(task.name).fontWeight(.semibold))")
                    .font(K.ui(12.5)).frame(maxWidth: .infinity, alignment: .leading)
                ApprovalCountdown(created: task.created)
            }
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Text(argv).font(K.mono(11)).lineLimit(argvOpen ? nil : 1).truncationMode(.tail)
                    .frame(maxWidth: .infinity, alignment: .leading).textSelection(.enabled)
                Button(argvOpen ? "less" : "more") { withAnimation(K.layout) { argvOpen.toggle() } }
                    .buttonStyle(.plain).font(K.ui(11)).foregroundStyle(K.accText)
            }
            .padding(.top, 6)
            if let line = summaryLine {
                Text(line).font(K.ui(11)).foregroundStyle(K.l2).padding(.top, 5).lineLimit(2)
            }
            Text("Runs as \(NSUserName()) on this Mac, without a sandbox.").font(K.ui(11)).foregroundStyle(K.l2).padding(.top, 2)
            buttons(height: 26).padding(.top, 9)
        }
        .padding(.horizontal, 11).padding(.vertical, 10)
        .card(radius: 10, shadow: true)
    }

    private var summaryLine: String? {
        var parts: [String] = []
        if let i = full.inputs { parts.append("\(i.count) file\(i.count == 1 ? "" : "s") · \(Fmt.bytes(inputBytes))") }
        let reqs = (full.requirements ?? []).filter { !$0.req.hasPrefix("disk") }.map(\.req)
        if !reqs.isEmpty { parts.append("needs " + reqs.joined(separator: ", ")) }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    private func buttons(height: CGFloat) -> some View {
        HStack(spacing: 6) {
            Button("Deny") { decide(false) }.buttonStyle(.k(.secondary, height: height, fullWidth: compact))
            Button("Approve") { decide(true) }.buttonStyle(.k(.primary, height: height, fullWidth: compact))
        }
        .disabled(busy)
    }

    private func decide(_ ok: Bool) {
        busy = true
        Task {
            if ok { await store.approve(task) } else { await store.deny(task) }
            busy = false
        }
    }

    // MARK: Approvals screen

    private var fullBody: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 12) {
                DeviceGlyph(symbol: store.peer(task.peerFp)?.symbol ?? "desktopcomputer", size: 36)
                VStack(alignment: .leading, spacing: 2) {
                    Text("\(Text(task.peerLabel).fontWeight(.semibold)) wants to run \(Text(task.name).fontWeight(.semibold))")
                        .font(K.ui(14))
                    HStack(spacing: 0) {
                        Text("Project \(task.project) · requested \(Fmt.time(task.created)) · ")
                        Text(store.peer(task.peerFp)?.display ?? task.peerFp.prefix(16).uppercased()).font(K.mono(11.5))
                        Text(" verified")
                    }
                    .font(K.ui(11.5)).foregroundStyle(K.l2).lineLimit(1)
                }
                Spacer(minLength: 8)
                ApprovalCountdown(created: task.created, size: 26)
            }
            .padding(.horizontal, 18).padding(.top, 16).padding(.bottom, 12)

            HStack(alignment: .firstTextBaseline, spacing: 10) {
                Text(argv).font(K.mono(11.5)).lineLimit(argvOpen ? nil : 1).truncationMode(.tail)
                    .frame(maxWidth: .infinity, alignment: .leading).textSelection(.enabled)
                Button(argvOpen ? "Less" : "Show all") { withAnimation(K.layout) { argvOpen.toggle() } }
                    .buttonStyle(.plain).font(K.ui(11.5)).foregroundStyle(K.accText)
            }
            .padding(.horizontal, 12).padding(.vertical, 10)
            .card(radius: 8, fill: K.con)
            .padding(.horizontal, 18)

            HStack(alignment: .top, spacing: 20) {
                VStack(alignment: .leading, spacing: 6) {
                    let n = full.inputs?.count ?? 0
                    Text("\(n) file\(n == 1 ? "" : "s") · \(Fmt.bytes(inputBytes))").sectionLabel()
                    VStack(alignment: .leading, spacing: 3) {
                        ForEach(full.inputs?.prefix(8) ?? [], id: \.path) { f in
                            HStack {
                                Text(f.path).lineLimit(1).truncationMode(.middle)
                                Spacer(minLength: 8)
                                Text(Fmt.bytes(f.size)).foregroundStyle(K.l2)
                            }
                        }
                        if n > 8 { Text("and \(n - 8) more").foregroundStyle(K.l2) }
                        if n == 0 { Text("No input files").foregroundStyle(K.l2) }
                    }
                    .font(K.mono(11.5))
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                VStack(alignment: .leading, spacing: 6) {
                    Text("Asked for · this Mac has").sectionLabel()
                    RequirementList(reqs: full.requirements ?? [], received: true)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(.horizontal, 18).padding(.vertical, 14)

            HStack(spacing: 8) {
                Text("Runs as \(NSUserName()) in \(abbreviate(full.wsDir ?? "~/.kith/work"))")
                    .font(K.ui(11.5)).foregroundStyle(K.l2).lineLimit(1).truncationMode(.middle)
                Spacer(minLength: 8)
                buttons(height: 28)
            }
            .padding(.horizontal, 18).padding(.vertical, 12)
            .background(K.bg2)
            .overlay(alignment: .top) { Divider05() }
        }
        .card(shadow: true)
    }
}

func abbreviate(_ path: String) -> String {
    let home = FileManager.default.homeDirectoryForCurrentUser.path
    return path.hasPrefix(home) ? "~" + path.dropFirst(home.count) : path
}

/// Requirement lines: ✓ / ✕ / ? with the machine's value.
struct RequirementList: View {
    var reqs: [CheckResult.Requirement]
    var received = false

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            ForEach(reqs) { q in
                HStack(spacing: 6) {
                    Image(systemName: q.status == "pass" ? "checkmark" : q.status == "fail" ? "xmark" : "questionmark")
                        .font(.system(size: 10, weight: .bold))
                        .foregroundStyle(q.status == "pass" ? K.green : q.status == "fail" ? K.red : K.l3)
                        .frame(width: 14)
                    Text(q.req)
                    Spacer(minLength: 8)
                    Text(received && q.status == "unknown" ? "checked before it runs" : q.have).foregroundStyle(K.l2).lineLimit(1)
                }
                .font(K.mono(11.5))
            }
            if reqs.isEmpty { Text("No requirements").font(K.mono(11.5)).foregroundStyle(K.l2) }
        }
    }
}

/// Approvals: the waiting requests, then what was decided.
struct ApprovalsView: View {
    @Environment(KithStore.self) private var store

    private var history: [KTask] {
        store.tasks.filter { !$0.sent && !$0.needsApproval }.prefix(20).map { $0 }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                HStack(alignment: .top, spacing: 10) {
                    Image(systemName: "exclamationmark.shield").font(.system(size: 15)).foregroundStyle(K.orange)
                    Text("Approving lets the peer run its command on this Mac as \(Text(NSUserName()).font(K.mono(12.5)).foregroundStyle(K.l1)), with your files and your network. Kith checks requirements, not intent: there is no sandbox.")
                        .font(K.ui(12.5)).foregroundStyle(K.l2).fixedSize(horizontal: false, vertical: true)
                }
                if store.pending.isEmpty {
                    Text(emptyMessage).font(K.ui(13)).foregroundStyle(K.l2)
                        .frame(maxWidth: .infinity).padding(28)
                        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(K.sep, lineWidth: 0.5))
                        .transition(.opacity.combined(with: .offset(y: 6)))
                }
                ForEach(store.pending) { t in
                    ApprovalCard(task: t)
                        .transition(.asymmetric(insertion: .opacity.combined(with: .offset(y: 6)),
                                                removal: .move(edge: .trailing).combined(with: .opacity)))
                }
                if !history.isEmpty {
                    VStack(alignment: .leading, spacing: 0) {
                        Text("History").font(K.ui(13, .semibold)).padding(.bottom, 8)
                        ForEach(history) { t in
                            HistoryRow(task: t, decision: store.decided[t.id])
                                .onTapGesture { store.openTask(t.id) }
                        }
                    }
                }
            }
            .padding(.horizontal, 28).padding(.top, 22).padding(.bottom, 32)
            .frame(maxWidth: 820)
            .frame(maxWidth: .infinity)
            .animation(K.layout, value: store.pending.map(\.id))
        }
    }

    private var emptyMessage: String {
        if let last = history.first, store.decided[last.id] == "Approved", !last.ended {
            return "\(last.name) is running on this Mac. Nothing else is waiting."
        }
        return "Nothing is waiting for approval."
    }
}

private struct HistoryRow: View {
    var task: KTask
    var decision: String?
    @State private var details = TaskDetails.shared

    /// The daemon keeps the outcome as state: denied/expired requests end
    /// REJECTED with a detail; anything that ran was approved (or auto).
    private var outcome: (String, String, Color) {
        if task.state == TaskState.rejected {
            let d = (task.detail ?? "").lowercased()
            if d.contains("not approved") { return ("Expired", "clock", K.l2) }
            if d.contains("denied") { return ("Denied", "nosign", K.l2) }
            return ("Rejected", "nosign", K.red)
        }
        return (decision ?? "Approved", "checkmark", K.green)
    }

    var body: some View {
        let o = outcome
        HStack(spacing: 12) {
            Text(task.name).font(K.ui(13, .medium)).frame(width: 130, alignment: .leading).lineLimit(1)
            Text(task.peerLabel).foregroundStyle(K.l2).frame(width: 90, alignment: .leading).lineLimit(1)
            Label(o.0, systemImage: o.1).font(K.ui(12, .medium)).foregroundStyle(o.2).frame(width: 96, alignment: .leading)
            Text(details.byID[task.id]?.argv.map(Fmt.argv) ?? task.project).font(K.mono(11)).foregroundStyle(K.l2)
                .lineLimit(1).truncationMode(.tail).frame(maxWidth: .infinity, alignment: .leading)
            Text(Fmt.ago(task.created)).font(K.ui(12)).foregroundStyle(K.l2).frame(width: 92, alignment: .trailing)
        }
        .font(K.ui(13))
        .padding(.horizontal, 4).padding(.vertical, 8)
        .overlay(alignment: .top) { Divider05() }
        .contentShape(Rectangle())
        .task(id: task.id) { if details.byID[task.id] == nil { await details.load(task.id) } }
    }
}
