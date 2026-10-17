import SwiftUI
import AppKit
import UniformTypeIdentifiers

/// New task: drop a kith.toml, see it parsed, watch the preflight against the
/// peer resolve line by line, then send. Send unlocks only when it passes.
struct NewTaskSheet: View {
    var initialFile: URL?
    @Environment(KithStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var file: URL?
    @State private var peerFP: String?
    @State private var check: CheckResult?
    @State private var error: KithError?
    @State private var checking = false
    @State private var revealed = 0
    @State private var sending = false
    @State private var dropping = false

    private var peer: Peer? { store.peers.first { $0.fp == peerFP } }
    private var allRevealed: Bool { revealed >= (check?.requirements.count ?? 0) }
    private var canSend: Bool { (check?.ok ?? false) && allRevealed && !sending && peer != nil }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 2) {
                Text("New task").font(K.ui(15, .semibold))
                Text("Kith checks the task against the peer before any file moves.").font(K.ui(12)).foregroundStyle(K.l2)
            }
            .padding(.horizontal, 22).padding(.top, 18)

            dropZone.padding(.horizontal, 22).padding(.top, 14)

            if let c = check {
                parsed(c).padding(.horizontal, 22).padding(.vertical, 16)
                    .transition(.opacity.combined(with: .offset(y: 6)))
                preflight(c).padding(.horizontal, 22)
                    .transition(.opacity.combined(with: .offset(y: 6)))
            } else if let error {
                Label(error.message, systemImage: "exclamationmark.triangle.fill")
                    .font(K.ui(12.5)).foregroundStyle(K.orange).padding(.horizontal, 22).padding(.top, 14)
                if let r = error.remediation { Text(r).font(K.ui(12)).foregroundStyle(K.l2).padding(.horizontal, 22).padding(.top, 4) }
            } else if file != nil {
                HStack { KSpinner(); Text("Reading the task…").foregroundStyle(K.l2) }.padding(22)
            }

            HStack(spacing: 8) {
                Text("⌘↩ to send").font(K.ui(11.5)).foregroundStyle(K.l3)
                Spacer()
                Button("Cancel") { dismiss() }.buttonStyle(.kSecondary).keyboardShortcut(.cancelAction)
                Button {
                    send()
                } label: {
                    HStack(spacing: 6) {
                        if sending { KSpinner(size: 11).tint(.white) }
                        Text(sending ? "Sending…" : "Send to \(peer?.name ?? "peer")")
                    }
                }
                .buttonStyle(.k(canSend ? .primary : .disabled))
                .disabled(!canSend)
                .keyboardShortcut(.return, modifiers: .command)
                .animation(.smooth(duration: 0.25), value: canSend)
            }
            .padding(.horizontal, 22).padding(.top, 16).padding(.bottom, 18)
        }
        .frame(width: 620)
        .font(K.ui(12.5))
        .foregroundStyle(K.l1)
        .background(K.card)
        .animation(K.layout, value: check)
        .animation(K.layout, value: error)
        .onAppear {
            peerFP = store.peers.first(where: \.reachable)?.fp ?? store.peers.first?.fp
            if let f = initialFile { load(f) }
        }
        .onChange(of: peerFP) { _, _ in if let file { load(file) } }
    }

    // MARK: Drop zone

    private var dropZone: some View {
        HStack(spacing: 12) {
            Image(systemName: file == nil ? "doc.badge.plus" : "doc.text").font(.system(size: 20)).foregroundStyle(K.accText)
            VStack(alignment: .leading, spacing: 1) {
                if let file {
                    Text(file.lastPathComponent).font(K.mono(12.5, .semibold))
                    Text(abbreviate(file.deletingLastPathComponent().path) + (check.map { " · parsed, \($0.inputs.count) input\($0.inputs.count == 1 ? "" : "s")" } ?? ""))
                        .font(K.ui(11.5)).foregroundStyle(K.l2).lineLimit(1).truncationMode(.middle)
                } else {
                    Text("Drop a kith.toml, or a folder with one").font(K.ui(12.5, .medium))
                    Text("The [task] file `kith run` and `kith submit` use").font(K.ui(11.5)).foregroundStyle(K.l2)
                }
            }
            Spacer(minLength: 8)
            if file != nil { Text("Drop another to replace").font(K.ui(11.5)).foregroundStyle(K.l2) }
            Button("Choose…") { choose() }.buttonStyle(.k(.secondary, height: 24))
        }
        .padding(.horizontal, 14).padding(.vertical, 12)
        .background(RoundedRectangle(cornerRadius: 10).fill(dropping ? K.accSoft.opacity(1.6) : K.accSoft))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(K.acc, style: StrokeStyle(lineWidth: 1.5, dash: [5, 3])))
        .scaleEffect(dropping ? 1.01 : 1)
        .animation(K.snappy, value: dropping)
        .dropDestination(for: URL.self) { urls, _ in
            guard let f = urls.lazy.compactMap(TaskFile.resolve).first else { return false }
            load(f)
            return true
        } isTargeted: { dropping = $0 }
    }

    // MARK: Parsed task

    private func parsed(_ c: CheckResult) -> some View {
        Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 14, verticalSpacing: 9) {
            GridRow {
                label("Command")
                Text(Fmt.argv(c.command)).font(K.mono(12)).textSelection(.enabled).lineLimit(2)
            }
            GridRow {
                label("Inputs")
                Text("\(Text(c.inputs.map(\.path).prefix(4).joined(separator: ", ") + (c.inputs.count > 4 ? ", …" : "")).font(K.mono(12)))\(Text("  · \(c.inputs.count) file\(c.inputs.count == 1 ? "" : "s"), \(Fmt.bytes(c.inputBytes))").foregroundStyle(K.l2))")
                    .lineLimit(2)
            }
            if !c.requirements.isEmpty {
                GridRow {
                    label("Requires")
                    FlowTokens(tokens: c.requirements.map(\.req))
                }
            }
            if let o = c.outputs, !o.isEmpty {
                GridRow { label("Outputs"); Text(o.joined(separator: ", ")).font(K.mono(12)).lineLimit(2) }
            }
            GridRow { label("Timeout"); Text(c.timeoutMinutes > 0 ? "\(c.timeoutMinutes) min" : "none") }
            GridRow {
                label("On interrupt")
                Text(c.onInterrupt.isEmpty ? "fail" : c.onInterrupt).font(K.mono(11))
                    .padding(.horizontal, 7).padding(.vertical, 2)
                    .background(RoundedRectangle(cornerRadius: 5).fill(K.fill))
            }
            GridRow {
                label("Send to")
                peerPicker
            }
        }
    }

    private func label(_ s: String) -> some View {
        Text(s).foregroundStyle(K.l2).frame(width: 96, alignment: .leading)
    }

    private var peerPicker: some View {
        Menu {
            ForEach(store.peers) { p in
                Button { peerFP = p.fp } label: { Text("\(p.name)\(p.reachable ? "" : " (offline)")") }
            }
        } label: {
            HStack(spacing: 7) {
                StatusDot(on: peer?.reachable ?? false)
                Text(peer?.name ?? "No peers").font(K.ui(12.5, .medium))
                Text(peer?.reachable == true ? "reachable" : "offline").font(K.ui(11.5)).foregroundStyle(K.l2)
                Image(systemName: "chevron.up.chevron.down").font(.system(size: 10)).foregroundStyle(K.l2)
            }
            .padding(.leading, 10).padding(.trailing, 8).frame(height: 26)
            .background(RoundedRectangle(cornerRadius: 7).fill(K.btn))
            .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(K.btnEdge, lineWidth: 0.5))
        }
        .menuStyle(.button).buttonStyle(.plain).menuIndicator(.hidden).fixedSize()
    }

    // MARK: Preflight

    private func preflight(_ c: CheckResult) -> some View {
        VStack(spacing: 0) {
            HStack {
                Text("Preflight on \(c.peerName ?? peer?.name ?? "peer")").font(K.ui(12, .semibold))
                Spacer()
                Text(preflightHead(c)).font(K.ui(11.5)).foregroundStyle(K.l2).contentTransition(.numericText())
            }
            .padding(.horizontal, 14).padding(.vertical, 9)
            .background(K.bg2)
            .overlay(alignment: .bottom) { Divider05() }
            VStack(spacing: 0) {
                ForEach(Array(c.requirements.enumerated()), id: \.element.id) { i, q in
                    let done = i < revealed
                    HStack(spacing: 10) {
                        Group {
                            if done {
                                Image(systemName: q.status == "pass" ? "checkmark.circle.fill" : q.status == "fail" ? "xmark.circle.fill" : "questionmark.circle")
                                    .foregroundStyle(q.status == "pass" ? K.green : q.status == "fail" ? K.red : K.l3)
                                    .symbolEffect(.bounce, value: done)
                                    .transition(.scale.combined(with: .opacity))
                            } else {
                                KSpinner(size: 12)
                            }
                        }
                        .frame(width: 18)
                        Text(q.req).frame(maxWidth: .infinity, alignment: .leading)
                        Image(systemName: "arrow.right").font(.system(size: 10)).foregroundStyle(K.l3)
                        Text(done ? q.have : "checking…").foregroundStyle(done ? K.l1 : K.l3)
                            .frame(maxWidth: .infinity, alignment: .leading).lineLimit(1)
                        Text(done ? (q.status == "unknown" ? "on send" : q.status) : "")
                            .font(K.ui(11)).foregroundStyle(q.status == "pass" ? K.green : q.status == "fail" ? K.red : K.l3)
                            .frame(width: 48, alignment: .trailing)
                    }
                    .font(K.mono(11.5))
                    .padding(.horizontal, 14).padding(.vertical, 5)
                    .animation(K.layout, value: done)
                }
                if let p = c.problem, !p.isEmpty {
                    Label(p, systemImage: "exclamationmark.triangle.fill").font(K.ui(12)).foregroundStyle(K.orange)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 14).padding(.vertical, 8)
                }
                if let probe = c.probe, !probe.isEmpty, allRevealed {
                    HStack(spacing: 6) {
                        Image(systemName: "terminal").font(.system(size: 11)).foregroundStyle(K.l3)
                        Text("Probe runs on \(c.peerName ?? "the peer") when sent: ").foregroundStyle(K.l2)
                        Text(Fmt.argv(probe)).font(K.mono(11)).lineLimit(1).truncationMode(.middle)
                    }
                    .font(K.ui(11.5))
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 14).padding(.vertical, 7)
                }
            }
            .padding(.vertical, 4)
        }
        .card(radius: 10, fill: .clear)
    }

    private func preflightHead(_ c: CheckResult) -> String {
        let n = c.requirements.count
        if checking { return "Reading…" }
        if n == 0 { return c.problem == nil ? "Nothing to check" : "Can't check" }
        if !allRevealed { return "Checking \(min(revealed + 1, n)) of \(n)…" }
        let fails = c.requirements.filter { $0.status == "fail" }.count
        if fails > 0 { return "\(fails) of \(n) don't match" }
        return "All \(n) requirements pass"
    }

    // MARK: Actions

    private func choose() {
        let p = NSOpenPanel()
        p.title = "Choose a Kith task file"
        p.allowedContentTypes = [UTType(filenameExtension: "toml") ?? .plainText, .folder]
        p.canChooseDirectories = true
        p.canChooseFiles = true
        p.allowsMultipleSelection = false
        if p.runModal() == .OK, let u = p.url {
            if let f = TaskFile.resolve(u) { load(f) } else {
                error = KithError(code: "NOT_FOUND", message: "No kith.toml in \(u.lastPathComponent).",
                                  remediation: "Choose the task's .toml file, or a folder that holds exactly one.")
                check = nil
            }
        }
    }

    private func load(_ f: URL) {
        file = f
        error = nil
        checking = true
        revealed = 0
        Task {
            do {
                let c = try await store.check(file: f, peer: peerFP)
                withAnimation(K.layout) { check = c; checking = false }
                for i in 1...max(1, c.requirements.count) {
                    try? await Task.sleep(for: .milliseconds(260))
                    withAnimation(K.layout) { revealed = i }
                }
            } catch {
                withAnimation(K.layout) {
                    self.error = error as? KithError ?? KithError.transport(error.localizedDescription)
                    check = nil
                    checking = false
                }
            }
        }
    }

    private func send() {
        guard canSend, let f = file, let p = peer else { return }
        sending = true
        Task {
            let r = await store.submit(file: f, peer: p.fp)
            sending = false
            guard let r else { return }
            if r.state == TaskState.rejected {
                error = KithError(code: r.code ?? "REJECTED", message: "\(p.name) rejected it: \(r.detail ?? "")", remediation: r.remediation)
                check = nil
                return
            }
            dismiss()
            store.flash("Sent to \(p.name)")
            store.openTask(r.taskId)
        }
    }
}

/// Requirement tokens that wrap onto new lines.
struct FlowTokens: View {
    var tokens: [String]
    var body: some View {
        FlowLayout(spacing: 5) {
            ForEach(tokens, id: \.self) { t in
                Text(t).font(K.mono(11)).padding(.horizontal, 7).padding(.vertical, 2)
                    .background(RoundedRectangle(cornerRadius: 5).fill(K.fill))
            }
        }
    }
}

struct FlowLayout: Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? 400
        var x: CGFloat = 0, y: CGFloat = 0, row: CGFloat = 0
        for s in subviews {
            let sz = s.sizeThatFits(.unspecified)
            if x > 0 && x + sz.width > width { x = 0; y += row + spacing; row = 0 }
            x += sz.width + spacing
            row = max(row, sz.height)
        }
        return CGSize(width: width, height: y + row)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, row: CGFloat = 0
        for s in subviews {
            let sz = s.sizeThatFits(.unspecified)
            if x > bounds.minX && x + sz.width > bounds.maxX { x = bounds.minX; y += row + spacing; row = 0 }
            s.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(sz))
            x += sz.width + spacing
            row = max(row, sz.height)
        }
    }
}
