import SwiftUI

struct LogLine: Identifiable, Equatable {
    enum Kind { case out, kith, warn, error }
    var id: Int
    var text: String
    var kind: Kind
}

/// Polls a task's log from a byte offset (IPC `logs`) and splits it into
/// classified lines. Tracebacks are tinted until the block ends.
@MainActor @Observable
final class LogStream {
    var lines: [LogLine] = []
    var done = false
    var loaded = false
    private var offset: Int64 = 0
    private var partial = ""
    private var inTraceback = false
    private let id: String
    static let maxLines = 20_000

    init(id: String) { self.id = id }

    func run() async {
        while !Task.isCancelled {
            if let r: LogsResult = try? await IPC.call("logs", ["id": id, "offset": offset, "follow": false, "max": 256 << 10], timeout: 10) {
                if let d = r.data, !d.isEmpty { append(d) }
                offset = r.offset
                loaded = true
                if r.done {
                    if !partial.isEmpty { push(partial); partial = "" }
                    done = true
                    return
                }
            }
            try? await Task.sleep(for: .milliseconds(700))
        }
    }

    private func append(_ chunk: String) {
        var text = partial + chunk
        partial = ""
        if !text.hasSuffix("\n") {
            if let nl = text.lastIndex(of: "\n") {
                partial = String(text[text.index(after: nl)...])
                text = String(text[...nl])
            } else {
                partial = text
                return
            }
        }
        var batch: [LogLine] = []
        for raw in text.split(separator: "\n", omittingEmptySubsequences: false).dropLast() {
            batch.append(classify(String(raw).replacingOccurrences(of: "\r", with: ""), id: lines.count + batch.count + 1))
        }
        withAnimation(.easeOut(duration: 0.12)) { lines.append(contentsOf: batch) }
        if lines.count > Self.maxLines { lines.removeFirst(lines.count - Self.maxLines) }
    }

    private func push(_ s: String) { lines.append(classify(s, id: lines.count + 1)) }

    private func classify(_ s: String, id: Int) -> LogLine {
        if s.hasPrefix("Traceback (most recent call last)") { inTraceback = true }
        let lower = s.lowercased()
        let kind: LogLine.Kind
        if inTraceback {
            kind = .error
            if !s.hasPrefix(" ") && !s.hasPrefix("Traceback") { inTraceback = false } // the final "XError: …" line
        } else if s.hasPrefix("[kith]") {
            kind = .kith
        } else if lower.contains("error") && (lower.contains("error:") || lower.hasPrefix("error")) {
            kind = .error
        } else if lower.contains("warning") {
            kind = .warn
        } else {
            kind = .out
        }
        return LogLine(id: id, text: s, kind: kind)
    }
}

/// One task: header with the link, timeline + info on the left, failure,
/// console and artifacts on the right.
struct TaskDetailView: View {
    var taskID: String
    var ns: Namespace.ID
    @Environment(KithStore.self) private var store
    @State private var full: KTask?
    @State private var logs: LogStream
    @State private var confirmCancel = false

    init(taskID: String, ns: Namespace.ID) {
        self.taskID = taskID
        self.ns = ns
        _logs = State(initialValue: LogStream(id: taskID))
    }

    /// The polled list row is fresher for state; the full view adds detail.
    private var t: KTask? {
        guard var f = full ?? store.task(taskID) else { return nil }
        if let row = store.task(taskID) {
            f.state = row.state; f.detail = row.detail; f.attempt = row.attempt
            f.code = row.code; f.remediation = row.remediation; f.updatedAt = row.updatedAt
        }
        return f
    }

    var body: some View {
        Group {
            if let t {
                VStack(spacing: 0) {
                    header(t)
                    Divider05()
                    HStack(spacing: 0) {
                        ScrollView { sidebar(t).padding(.horizontal, 18).padding(.top, 16).padding(.bottom, 20) }
                            .frame(width: 268)
                        Rectangle().fill(K.sep).frame(width: 0.5)
                        VStack(spacing: 12) {
                            if t.state == TaskState.failed || t.state == TaskState.rejected {
                                FailureCallout(task: t, lastError: logs.lines.last { $0.kind == .error }?.text)
                            }
                            ConsoleView(logs: logs, live: !t.ended, rejected: t.state == TaskState.rejected, peer: t.peerLabel)
                            if !(full?.artifacts ?? []).isEmpty { ArtifactsTable(task: full!) }
                        }
                        .padding(.horizontal, 18).padding(.top, 14).padding(.bottom, 16)
                    }
                }
            } else {
                VStack(spacing: 8) { KSpinner(); Text("Loading task…").foregroundStyle(K.l2) }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .task(id: taskID) { await logs.run() }
        .task(id: taskID) {
            while !Task.isCancelled {
                if let f: KTask = try? await IPC.call("task", ["id": taskID], timeout: 10) {
                    withAnimation(K.layout) { full = f }
                }
                try? await Task.sleep(for: .milliseconds(t?.ended == true ? 4000 : 1200))
            }
        }
        .background {
            Button("") { if t?.ended == false { confirmCancel = true } }.keyboardShortcut(".", modifiers: .command).hidden()
        }
    }

    // MARK: Header

    private func header(_ t: KTask) -> some View {
        HStack(spacing: 20) {
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 10) {
                    Text(t.name).font(K.ui(20, .semibold)).tracking(-0.2)
                        .matchedGeometryEffect(id: "title-\(t.id)", in: ns, properties: .position)
                    StateChip(state: t.state, big: true)
                        .matchedGeometryEffect(id: "chip-\(t.id)", in: ns)
                }
                Text(subline(t)).font(K.ui(12)).foregroundStyle(K.l2).lineLimit(1)
            }
            Spacer(minLength: 12)
            VStack(spacing: 4) {
                HStack(spacing: 6) {
                    Image(systemName: "laptopcomputer").font(.system(size: 14)).foregroundStyle(K.l2)
                    LinkLine(mode: linkMode(t))
                    Image(systemName: store.peer(t.peerFp)?.symbol ?? "desktopcomputer").font(.system(size: 14)).foregroundStyle(K.l2)
                }
                Text(linkLabel(t)).font(K.ui(10.5)).foregroundStyle(K.l3).contentTransition(.interpolate)
            }
            .frame(width: 150)
            if !t.ended {
                Button("Cancel…") { confirmCancel = true }
                    .buttonStyle(.k(.destructive, height: 26))
                    .popover(isPresented: $confirmCancel, arrowEdge: .bottom) { cancelConfirm(t) }
            }
            if t.ended, let dir = t.sent ? t.projectDir : t.wsDir {
                Button { store.reveal(dir) } label: { Label("Reveal in Finder", systemImage: "folder") }
                    .buttonStyle(.k(.secondary, height: 26))
            }
        }
        .padding(.horizontal, 22).padding(.vertical, 16)
    }

    private func cancelConfirm(_ t: KTask) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Cancel \(t.name)\(t.sent ? " on \(t.peerLabel)" : "")?").font(K.ui(13, .semibold))
            Text("Kith stops the process. Files already written stay \(t.sent ? "on \(t.peerLabel)" : "in the workspace") and can still be fetched.")
                .font(K.ui(12)).foregroundStyle(K.l2).fixedSize(horizontal: false, vertical: true)
            HStack(spacing: 8) {
                Spacer()
                Button("Keep running") { confirmCancel = false }.buttonStyle(.k(.secondary, height: 24))
                Button("Cancel task") {
                    confirmCancel = false
                    Task { await store.cancel(t) }
                }
                .buttonStyle(.k(.destructiveFilled, height: 24))
            }
            .padding(.top, 8)
        }
        .padding(14).frame(width: 290)
    }

    private func subline(_ t: KTask) -> String {
        var p = [t.sent ? "Sent to \(t.peerLabel)" : "From \(t.peerLabel)", "project \(t.project)"]
        if t.attempt > 1 { p.append("attempt \(t.attempt)") }
        if t.state == TaskState.rejected { p.append("never started") }
        else if let s = t.started {
            let end = t.ended ? t.updated : nil
            p.append((t.ended ? "ran " : "") + Fmt.clock((end ?? .now).timeIntervalSince(s)) + (t.ended ? "" : " elapsed"))
        }
        p.append("id \(t.id)")
        return p.joined(separator: " · ")
    }

    private func linkMode(_ t: KTask) -> LinkMode {
        guard store.peer(t.peerFp)?.reachable == true else { return .off }
        switch t.state {
        case TaskState.transferring: return t.sent ? .flow : .flowBack
        case TaskState.running: return t.sent ? .flowBack : .flow
        default: return .idle
        }
    }

    private func linkLabel(_ t: KTask) -> String {
        if store.peer(t.peerFp)?.reachable != true { return "\(t.peerLabel) offline" }
        switch t.state {
        case TaskState.transferring: return t.sent ? "sending files" : "receiving files"
        case TaskState.running: return "streaming logs"
        case TaskState.rejected: return "nothing transferred"
        case TaskState.cancelling: return "stopping"
        default:
            let arts = full?.artifacts ?? []
            let local = arts.filter { $0.location == "local" }.count
            return arts.isEmpty ? "idle" : "idle · \(local) of \(arts.count) fetched"
        }
    }

    // MARK: Left column

    private func sidebar(_ t: KTask) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Timeline").sectionLabel().padding(.bottom, 10)
            let tl = t.timeline ?? full?.timeline ?? []
            ForEach(Array(tl.enumerated()), id: \.element.seq) { i, tr in
                TimelineRow(tr: tr, last: i == tl.count - 1, current: i == tl.count - 1 && !t.ended,
                            resumes: tr.state == TaskState.interrupted && tl.dropFirst(i + 1).contains { $0.state == TaskState.running })
                    .transition(.opacity.combined(with: .offset(y: 6)))
            }
            Text("Info").sectionLabel().padding(.top, 10).padding(.bottom, 8)
            VStack(alignment: .leading, spacing: 8) {
                info("argv", t.argv.map(Fmt.argv) ?? "—")
                info("Working directory", (t.sent ? t.projectDir : t.wsDir).map(abbreviate) ?? "—")
                if let pid = t.pid { info("Process", "pid \(pid)") }
                info("Created · updated", "\(Fmt.time(t.created)) · \(Fmt.time(t.updated))")
                if let outs = t.outputs, !outs.isEmpty { info("Outputs", outs.joined(separator: ", ")) }
                if let reqs = t.requirements, !reqs.isEmpty {
                    VStack(alignment: .leading, spacing: 3) {
                        Text("Requirements").font(K.ui(11)).foregroundStyle(K.l3)
                        RequirementList(reqs: reqs, received: !t.sent).font(K.mono(11))
                    }
                }
            }
        }
        .animation(K.layout, value: t.timeline?.count)
    }

    private func info(_ k: String, _ v: String) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(k).font(K.ui(11)).foregroundStyle(K.l3)
            Text(v).font(K.mono(11)).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
        }
    }
}

private struct TimelineRow: View {
    var tr: Transition
    var last: Bool
    var current: Bool
    var resumes: Bool

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            VStack(spacing: 3) {
                ZStack {
                    if current { Circle().fill(K.accSoft).frame(width: 19, height: 19) }
                    Circle().fill(current ? K.acc : K.bg).frame(width: 9, height: 9)
                        .overlay(Circle().strokeBorder(dotColor, lineWidth: 1.5))
                }
                .frame(width: 12, height: 12).padding(.top, 3)
                if !last {
                    Rectangle().fill(.clear).frame(width: 1.5)
                        .overlay {
                            if resumes {
                                GeometryReader { g in
                                    Path { p in p.move(to: .zero); p.addLine(to: CGPoint(x: 0, y: g.size.height)) }
                                        .stroke(K.orange, style: StrokeStyle(lineWidth: 1.5, dash: [3, 3]))
                                }
                            } else {
                                K.sep
                            }
                        }
                }
            }
            .frame(width: 12)
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    StateChip(state: tr.state)
                    Spacer()
                    Text(Fmt.time(Fmt.date(tr.ts))).font(K.mono(10.5)).foregroundStyle(K.l3)
                }
                if let d = tr.detail, !d.isEmpty {
                    Text(d).font(K.ui(11.5)).foregroundStyle(K.l2).fixedSize(horizontal: false, vertical: true)
                }
                if resumes {
                    Label("resumes as a new attempt", systemImage: "arrow.counterclockwise")
                        .font(K.ui(11, .medium)).foregroundStyle(K.orange)
                }
            }
            .padding(.bottom, 13)
        }
    }

    private var dotColor: Color {
        switch tr.state {
        case TaskState.running, TaskState.accepted, TaskState.transferring, TaskState.finalizing: K.acc
        case TaskState.completed: K.green
        case TaskState.failed, TaskState.rejected: K.red
        case TaskState.interrupted, TaskState.cancelling: K.orange
        default: K.l3
        }
    }
}

/// A calm callout with the error code and what to do about it.
private struct FailureCallout: View {
    var task: KTask
    var lastError: String?
    @Environment(KithStore.self) private var store

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: "exclamationmark.circle.fill").font(.system(size: 17)).foregroundStyle(K.red)
            VStack(alignment: .leading, spacing: 3) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(title).font(K.ui(13, .semibold))
                    if let c = task.code { Text(c).font(K.mono(11, .semibold)).foregroundStyle(K.red) }
                }
                if let d = detailText, !d.isEmpty { Text(d).font(K.ui(12.5)).fixedSize(horizontal: false, vertical: true) }
                if let e = lastError, !e.isEmpty {
                    Text(e).font(K.mono(11.5)).foregroundStyle(K.red).lineLimit(2).textSelection(.enabled)
                }
                if let r = task.remediation, !r.isEmpty {
                    Text(r).font(K.ui(12)).foregroundStyle(K.l2).padding(.top, 3).fixedSize(horizontal: false, vertical: true)
                }
            }
            Spacer(minLength: 8)
            Button { store.copy(details, label: "error details") } label: { Label("Copy details", systemImage: "doc.on.doc") }
                .buttonStyle(.k(.secondary, height: 24))
        }
        .padding(.horizontal, 14).padding(.vertical, 13)
        .card(radius: 10, fill: K.bg2)
        .transition(.opacity.combined(with: .offset(y: -6)))
    }

    private var title: String {
        task.state == TaskState.rejected ? "\(task.peerLabel) can't run this task" : "\(task.argv?.first ?? task.name) failed"
    }

    private var detailText: String? {
        guard var d = task.detail else { return nil }
        if let c = task.code, d.hasPrefix(c + ": ") { d = String(d.dropFirst(c.count + 2)) }
        return d
    }

    private var details: String {
        [task.name + " (" + task.id + ")", task.state, task.code, task.detail, lastError, task.remediation.map { "Fix: " + $0 }]
            .compactMap { $0 }.joined(separator: "\n")
    }
}

// MARK: - Console

struct ConsoleView: View {
    var logs: LogStream
    var live: Bool
    var rejected: Bool
    var peer: String
    @Environment(KithStore.self) private var store
    @State private var query = ""
    @State private var wrap = false
    @State private var scrolledUp = false
    @State private var userScrolling = false
    @State private var seenAt = 0
    @State private var position = ScrollPosition(edge: .bottom)

    private var shown: [LogLine] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        return q.isEmpty ? logs.lines : logs.lines.filter { $0.text.lowercased().contains(q) }
    }

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            Divider05()
            ZStack(alignment: .bottom) {
                if logs.lines.isEmpty {
                    Text(emptyText).font(K.ui(12.5)).foregroundStyle(K.l2)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    ScrollView(.vertical) {
                        LazyVStack(alignment: .leading, spacing: 0) {
                            ForEach(shown) { l in
                                LogLineView(line: l, wrap: wrap)
                                    .transition(.opacity)
                            }
                        }
                        .padding(.top, 8).padding(.bottom, 10)
                    }
                    .scrollPosition($position)
                    .defaultScrollAnchor(.top, for: .alignment)
                    .defaultScrollAnchor(.bottom, for: .initialOffset)
                    .defaultScrollAnchor(.bottom, for: .sizeChanges)
                    // Only the user's own scrolling pauses following; new
                    // lines growing the content must not.
                    .onScrollPhaseChange { _, phase in userScrolling = phase != .idle }
                    .onScrollGeometryChange(for: Bool.self) { g in
                        g.contentSize.height - g.contentOffset.y - g.containerSize.height > 12
                    } action: { _, up in
                        if up && userScrolling && !scrolledUp {
                            seenAt = logs.lines.count
                            withAnimation(K.pill) { scrolledUp = true }
                        } else if !up && scrolledUp {
                            withAnimation(K.pill) { scrolledUp = false }
                        }
                    }
                    .onChange(of: logs.lines.count) { _, _ in
                        if !scrolledUp && query.isEmpty { position.scrollTo(edge: .bottom) }
                    }
                }
                if scrolledUp && live {
                    Button {
                        withAnimation(K.layout) { position.scrollTo(edge: .bottom) }
                        scrolledUp = false
                    } label: {
                        HStack(spacing: 5) {
                            Image(systemName: "arrow.down").font(.system(size: 12, weight: .semibold))
                            Text("Jump to live").font(K.ui(12, .medium))
                            let n = logs.lines.count - seenAt
                            if n > 0 {
                                Text("+\(n)").font(K.mono(11)).opacity(0.8).contentTransition(.numericText())
                            }
                        }
                        .foregroundStyle(.white)
                        .padding(.leading, 9).padding(.trailing, 12).frame(height: 28)
                        .background(Capsule().fill(K.acc))
                        .shadow(color: .black.opacity(0.2), radius: 10, y: 6)
                    }
                    .buttonStyle(.plain)
                    .padding(.bottom, 14)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
                }
            }
            .background(K.con)
        }
        .card(radius: 10, fill: K.con)
        .frame(minHeight: 180)
    }

    private var emptyText: String {
        if rejected { return "No logs. \(peer) rejected the task before it started." }
        if !logs.loaded { return "Loading logs…" }
        return live ? "Waiting for output…" : "This task printed nothing."
    }

    private var toolbar: some View {
        HStack(spacing: 8) {
            Text("Logs").font(K.ui(12, .semibold))
            if live && !scrolledUp {
                HStack(spacing: 5) { LiveDot(color: K.acc, live: true); Text("Following") }
                    .font(K.ui(11)).foregroundStyle(K.accText).transition(.opacity)
            } else {
                Text(live ? "Paused" : "\(logs.lines.count) lines").font(K.ui(11)).foregroundStyle(K.l2)
                    .contentTransition(.numericText())
            }
            Spacer()
            HStack(spacing: 5) {
                Image(systemName: "magnifyingglass").font(.system(size: 11)).foregroundStyle(K.l3)
                TextField("Find in log", text: $query).textFieldStyle(.plain).font(K.ui(11.5))
                if !query.isEmpty { Text("\(shown.count) found").font(K.ui(10.5)).foregroundStyle(K.l3) }
            }
            .padding(.horizontal, 8).frame(width: 170, height: 22)
            .background(RoundedRectangle(cornerRadius: 6).fill(K.fill))
            Button { withAnimation(K.snappy) { wrap.toggle() } } label: { Image(systemName: "text.word.spacing") }
                .buttonStyle(IconButtonStyle(on: wrap)).help("Wrap lines")
            Button {
                store.copy(logs.lines.map(\.text).joined(separator: "\n"), label: "\(logs.lines.count) lines")
            } label: { Image(systemName: "doc.on.doc") }
                .buttonStyle(IconButtonStyle()).help("Copy log")
        }
        .padding(.leading, 12).padding(.trailing, 8).frame(height: 36)
        .background(K.bg)
        .animation(K.snappy, value: scrolledUp)
    }
}

private struct LogLineView: View {
    var line: LogLine
    var wrap: Bool

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text("\(line.id)").foregroundStyle(K.l3).frame(width: 40, alignment: .trailing)
            Text(line.text.isEmpty ? " " : line.text)
                .foregroundStyle(color)
                .lineLimit(wrap ? nil : 1)
                .truncationMode(.tail)
                .fixedSize(horizontal: false, vertical: wrap)
                .textSelection(.enabled)
        }
        .font(K.mono(11.5))
        .lineSpacing(2)
        .padding(.leading, 0).padding(.trailing, 14).padding(.vertical, 1.5)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(line.kind == .error ? K.redSoft : .clear)
    }

    private var color: Color {
        switch line.kind {
        case .out: K.l1
        case .kith: K.l2
        case .warn: K.orange
        case .error: K.red
        }
    }
}

// MARK: - Artifacts

private struct ArtifactsTable: View {
    var task: KTask
    @Environment(KithStore.self) private var store
    @State private var fetching: Set<String> = []

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 12) {
                Text("Artifacts").frame(maxWidth: .infinity, alignment: .leading)
                Text("Size").frame(width: 64, alignment: .trailing)
                Text("SHA-256").frame(width: 120, alignment: .leading)
                Text("Status").frame(width: 120, alignment: .leading)
                Spacer().frame(width: 76)
            }
            .font(K.ui(11, .medium)).foregroundStyle(K.l2)
            .padding(.horizontal, 12).padding(.vertical, 8)
            .background(K.bg)
            ForEach(task.artifacts ?? []) { a in
                Divider05()
                row(a)
            }
        }
        .card(radius: 10, fill: .clear)
        .frame(maxHeight: 190)
    }

    private func row(_ a: Artifact) -> some View {
        let isFetching = fetching.contains(a.path)
        let local = a.location == "local"
        return HStack(spacing: 12) {
            Text(a.path).font(K.mono(11.5)).lineLimit(1).truncationMode(.middle).frame(maxWidth: .infinity, alignment: .leading)
            Text(Fmt.bytes(a.size)).font(K.ui(12)).foregroundStyle(K.l2).frame(width: 64, alignment: .trailing)
            Text(Fmt.sha(a.sha256)).font(K.mono(11)).foregroundStyle(K.l2).frame(width: 120, alignment: .leading)
                .onTapGesture { store.copy(a.sha256, label: "SHA-256") }.help("Copy SHA-256")
            HStack(spacing: 6) {
                Circle().fill(local ? K.greenDot : a.producerOffline ? K.orangeDot : .clear)
                    .overlay(Circle().strokeBorder(local || a.producerOffline ? .clear : K.l3, lineWidth: 1.5))
                    .frame(width: 7, height: 7)
                Text(isFetching ? "fetching…" : local ? (a.copyBack ? "copied back" : "fetched") : a.producerOffline ? "peer offline" : "on peer")
                    .font(K.ui(12)).foregroundStyle(K.l2)
            }
            .frame(width: 120, alignment: .leading)
            HStack {
                if isFetching {
                    ProgressView().progressViewStyle(.linear).tint(K.acc).frame(width: 70)
                } else if local {
                    Button { store.reveal(localPath(a)) } label: { Image(systemName: "folder") }
                        .buttonStyle(IconButtonStyle()).help("Reveal in Finder")
                } else if task.sent && !a.producerOffline {
                    Button("Fetch") { fetch(a) }.buttonStyle(.k(.secondary, height: 22))
                }
            }
            .frame(width: 76, alignment: .trailing)
        }
        .padding(.horizontal, 12).padding(.vertical, 6)
        .hoverFill(radius: 0)
        .animation(K.snappy, value: isFetching)
    }

    private func localPath(_ a: Artifact) -> String {
        let base = task.sent ? (task.projectDir ?? "") : (task.wsDir ?? "")
        return (base as NSString).appendingPathComponent(a.path)
    }

    private func fetch(_ a: Artifact) {
        fetching.insert(a.path)
        Task {
            if let r = await store.fetch(task, a) { store.flash("Fetched \((r.path as NSString).lastPathComponent)") }
            fetching.remove(a.path)
        }
    }
}
