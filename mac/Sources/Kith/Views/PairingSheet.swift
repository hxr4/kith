import SwiftUI

/// Drives one `pair` IPC session: events arrive as lines (waiting, code),
/// the final line is the paired peer or an error; confirmations go back on
/// the same connection.
@MainActor @Observable
final class PairSession {
    enum Step: Equatable { case choose, waiting, compare, confirming, paired, error }
    var step: Step = .choose
    var code = ""
    var peerName = ""
    var target = ""          // ip we dialed, "" when waiting for the other side
    var peer: Peer?
    var error: KithError?
    var startedAt = Date()
    private var conn: IPCConnection?

    func start(ip: String, name: String) {
        cancel()
        error = nil
        code = ""
        peer = nil
        peerName = name
        target = ip
        startedAt = Date()
        withAnimation(K.layout) { step = .waiting }
        let ipArg = ip
        Task.detached { [weak self] in
            do {
                let c = try IPCConnection(timeout: 400)
                await self?.attach(c)
                try c.send(["method": "pair", "params": ["ip": ipArg]])
                while true {
                    let msg = try IPCMessage(line: try c.readLine())
                    switch msg {
                    case .event(let ev, let data):
                        let e = try? IPC.decoder.decode(PairEvent.self, from: data)
                        await self?.event(ev, e)
                    case .result(let d):
                        let p = try? IPC.decoder.decode(Peer.self, from: d)
                        await self?.finish(p, nil)
                        c.close()
                        return
                    case .failure(let e):
                        await self?.finish(nil, e)
                        c.close()
                        return
                    }
                }
            } catch {
                await self?.finish(nil, error as? KithError ?? .transport(error.localizedDescription))
            }
        }
    }

    private func attach(_ c: IPCConnection) { conn = c }

    private func event(_ ev: String, _ e: PairEvent?) {
        guard ev == "code", let e else { return }
        code = e.code ?? ""
        if let n = e.peer?.name, !n.isEmpty { peerName = n }
        withAnimation(K.layout) { step = .compare }
        NSApp.activate()
    }

    private func finish(_ p: Peer?, _ e: KithError?) {
        conn = nil
        guard step != .choose else { return } // cancelled
        if let p {
            peer = p
            peerName = p.name
            withAnimation(K.layout) { step = .paired }
            Task { await KithStore.shared.refresh() }
        } else {
            error = e
            withAnimation(K.layout) { step = .error }
        }
    }

    func confirm(_ ok: Bool) {
        guard let c = conn else { return }
        try? c.send(["confirm": ok])
        withAnimation(K.layout) { step = ok ? .confirming : .error }
        if !ok { error = KithError(code: "MISMATCH", message: "codes did not match") }
    }

    func cancel() {
        conn?.cancel()
        conn = nil
        step = .choose
    }
}

struct PairingSheet: View {
    @Environment(KithStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var session = PairSession()
    @State private var discovery = Discovery()
    @State private var pick: String?
    @State private var manualIP = ""
    @State private var manual = false

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 14) {
                Text("Pair a machine").font(K.ui(14, .semibold))
                Spacer()
                ForEach(Array(["Choose", "Connect", "Compare", "Paired"].enumerated()), id: \.offset) { i, s in
                    HStack(spacing: 5) {
                        Circle().fill(i <= stepIndex ? (session.step == .error && i == stepIndex ? K.orangeDot : K.acc) : .clear)
                            .overlay(Circle().strokeBorder(i > stepIndex ? K.l3 : .clear, lineWidth: 1.5))
                            .frame(width: 6, height: 6)
                        Text(s).font(K.ui(11.5, i == stepIndex ? .medium : .regular))
                            .foregroundStyle(i == stepIndex ? K.l1 : i < stepIndex ? K.l2 : K.l3)
                    }
                }
            }
            .padding(.horizontal, 20).padding(.vertical, 14)
            Divider05()

            Group {
                switch session.step {
                case .choose: choose
                case .waiting, .compare, .confirming, .paired: ceremony
                case .error: errorView
                }
            }
            .transition(.opacity)
        }
        .frame(width: 500)
        .font(K.ui(13))
        .foregroundStyle(K.l1)
        .background(K.card)
        .animation(K.layout, value: session.step)
        .onAppear { discovery.start(excluding: store.status?.display) }
        .onDisappear {
            discovery.stop()
            if session.step != .paired { session.cancel() }
        }
    }

    private var stepIndex: Int {
        switch session.step {
        case .choose: 0
        case .waiting: 1
        case .compare, .confirming, .error: 2
        case .paired: 3
        }
    }

    // MARK: Choose

    private var selected: Discovery.Machine? { discovery.machines.first { $0.id == pick } }

    private func isPaired(_ m: Discovery.Machine) -> Bool { store.peers.contains { $0.display == m.display } }

    private var choose: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Machines running Kith on this network").font(K.ui(12.5)).foregroundStyle(K.l2).padding(.bottom, 10)
            VStack(spacing: 0) {
                ForEach(discovery.machines) { m in
                    let paired = isPaired(m), sel = pick == m.id
                    HStack(spacing: 12) {
                        Image(systemName: "desktopcomputer").font(.system(size: 18)).symbolRenderingMode(.hierarchical)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(m.name).font(K.ui(13, .medium))
                            HStack(spacing: 0) {
                                Text(m.address.map { String($0.split(separator: ":").first ?? "") } ?? "resolving…").font(K.mono(11))
                                Text(" · ")
                                Text(m.display).font(K.mono(11))
                            }
                            .font(K.ui(11)).foregroundStyle(K.l2)
                        }
                        Spacer()
                        if paired { Text("already paired").font(K.ui(11.5)).foregroundStyle(K.l2) }
                        Image(systemName: "checkmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(K.accText)
                            .opacity(sel ? 1 : 0)
                    }
                    .padding(.horizontal, 14).padding(.vertical, 9)
                    .background(sel ? K.accSoft : .clear)
                    .opacity(paired ? 0.5 : 1)
                    .contentShape(Rectangle())
                    .onTapGesture { if !paired { withAnimation(K.snappy) { pick = m.id; manual = false } } }
                    .overlay(alignment: .bottom) { Divider05() }
                    .transition(.opacity.combined(with: .offset(y: 6)))
                }
                HStack(spacing: 8) {
                    KSpinner(size: 12)
                    Text(discovery.failed ?? (discovery.machines.isEmpty ? "Looking for machines… open Kith on the other one." : "Looking for more…"))
                }
                .font(K.ui(12)).foregroundStyle(K.l3)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 14).padding(.vertical, 10)
            }
            .frame(minHeight: 150, alignment: .top)
            .card(radius: 10, fill: .clear)
            .animation(K.layout, value: discovery.machines)

            HStack(spacing: 14) {
                if manual {
                    TextField("IP address, e.g. 10.157.240.135", text: $manualIP)
                        .textFieldStyle(.roundedBorder).font(K.mono(12)).frame(width: 240)
                        .onSubmit(go)
                } else {
                    Button("Enter IP address…") { withAnimation(K.snappy) { manual = true; pick = nil } }
                }
                Button("Wait for the other machine") { session.start(ip: "", name: "the other machine") }
            }
            .buttonStyle(.plain).font(K.ui(12.5)).foregroundStyle(K.accText)
            .padding(.top, 10)

            HStack(spacing: 8) {
                Spacer()
                Button("Cancel") { dismiss() }.buttonStyle(.kSecondary).keyboardShortcut(.cancelAction)
                Button(goLabel) { go() }.buttonStyle(.kPrimary).disabled(!canGo).keyboardShortcut(.defaultAction)
            }
            .padding(.top, 18)
        }
        .padding(.horizontal, 20).padding(.top, 16).padding(.bottom, 18)
    }

    private var canGo: Bool {
        manual ? !manualIP.trimmingCharacters(in: .whitespaces).isEmpty : selected?.address != nil
    }

    private var goLabel: String {
        if manual { return "Pair" }
        return selected.map { "Pair with \($0.name)" } ?? "Pair"
    }

    private func go() {
        guard canGo else { return }
        if manual {
            let ip = manualIP.trimmingCharacters(in: .whitespaces)
            session.start(ip: ip, name: ip)
        } else if let m = selected, let a = m.address {
            session.start(ip: a, name: m.name)
        }
    }

    // MARK: Waiting · Compare · Paired

    private var ceremony: some View {
        VStack(spacing: 0) {
            PairLink(step: session.step).padding(.top, 34).padding(.bottom, 8)
            switch session.step {
            case .waiting: waiting
            case .compare, .confirming: compare
            default: paired
            }
        }
    }

    private var waiting: some View {
        VStack(spacing: 6) {
            Text(session.target.isEmpty ? "On your other machine, open Kith and choose Pair." : "Open Kith on \(session.peerName) and choose Pair.")
                .font(K.ui(17, .semibold)).multilineTextAlignment(.center)
            HStack(spacing: 6) {
                KSpinner(size: 12)
                TimelineView(.periodic(from: .now, by: 1)) { tl in
                    let left = max(0, 300 - tl.date.timeIntervalSince(session.startedAt))
                    if session.target.isEmpty {
                        Text("Waiting as \(store.thisName) · \(Fmt.clock(left)) left")
                    } else {
                        Text("Waiting for \(session.peerName) at \(Text(session.target).font(K.mono(12))) · \(Fmt.clock(left)) left")
                    }
                }
            }
            .font(K.ui(12.5)).foregroundStyle(K.l2)
            HStack {
                Spacer()
                Button("Cancel") { session.cancel() }.buttonStyle(.kSecondary)
            }
            .padding(.top, 30)
        }
        .padding(.horizontal, 20).padding(.top, 14).padding(.bottom, 18)
    }

    private var compare: some View {
        VStack(spacing: 4) {
            CodeReveal(code: Fmt.pairCode(session.code)).padding(.top, 8).padding(.bottom, 10)
            Text("Does \(session.peerName) show the same code?").font(K.ui(15, .semibold))
            Text("Compare every character. If they differ, something else on this network is answering for \(session.peerName).")
                .font(K.ui(12)).foregroundStyle(K.l2).multilineTextAlignment(.center).padding(.horizontal, 30)
            HStack(spacing: 8) {
                if session.step == .confirming {
                    KSpinner(size: 12)
                    Text("Waiting for \(session.peerName) to confirm…").font(K.ui(12)).foregroundStyle(K.l2)
                }
                Spacer()
                Button("They don't match") { session.confirm(false) }.buttonStyle(.kSecondary)
                Button("They match") { session.confirm(true) }.buttonStyle(.kPrimary).keyboardShortcut(.defaultAction)
            }
            .disabled(session.step == .confirming)
            .padding(.top, 24)
        }
        .padding(.horizontal, 20).padding(.bottom, 18)
    }

    private var paired: some View {
        VStack(spacing: 5) {
            Text("\(session.peerName) is paired").font(K.ui(17, .semibold))
            Text(session.peer?.policy == "auto"
                 ? "Tasks from \(session.peerName) run without asking. You can change that in Machines."
                 : "Tasks from \(session.peerName) will ask before they run. You can change that in Machines.")
                .font(K.ui(12.5)).foregroundStyle(K.l2).multilineTextAlignment(.center)
            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 4) {
                GridRow { Text("This Mac").foregroundStyle(K.l2); Text(store.status?.display ?? "").font(K.mono(11.5)) }
                GridRow { Text(session.peerName).foregroundStyle(K.l2); Text(session.peer?.display ?? "").font(K.mono(11.5)) }
            }
            .font(K.ui(11.5)).padding(.top, 14)
            HStack {
                Spacer()
                Button("Done") { dismiss() }.buttonStyle(.kPrimary).keyboardShortcut(.defaultAction)
            }
            .padding(.top, 22)
        }
        .padding(.horizontal, 20).padding(.top, 14).padding(.bottom, 18)
        .transition(.opacity.animation(.easeOut(duration: 0.4).delay(0.7)))
    }

    // MARK: Errors

    private var errorView: some View {
        let e = describe(session.error)
        return VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .top, spacing: 14) {
                Image(systemName: e.icon).font(.system(size: 26)).foregroundStyle(K.orange)
                VStack(alignment: .leading, spacing: 5) {
                    Text(e.title).font(K.ui(15, .semibold))
                    Text(e.body).font(K.ui(12.5)).foregroundStyle(K.l2).fixedSize(horizontal: false, vertical: true)
                }
            }
            .padding(.horizontal, 26).padding(.top, 26)
            HStack(spacing: 8) {
                Spacer()
                Button("Close") { dismiss() }.buttonStyle(.kSecondary)
                Button(e.cta) {
                    if e.cta == "Run diagnostics" { dismiss(); store.open(.diagnostics) } else { session.cancel() }
                }
                .buttonStyle(.kPrimary).keyboardShortcut(.defaultAction)
            }
            .padding(.horizontal, 20).padding(.top, 26).padding(.bottom, 18)
        }
    }

    private func describe(_ e: KithError?) -> (icon: String, title: String, body: String, cta: String) {
        let who = session.peerName.isEmpty ? "The other machine" : session.peerName
        let msg = (e?.message ?? "").lowercased()
        switch e?.code {
        case "MISMATCH":
            return ("exclamationmark.shield", "The codes didn't match",
                    "Nothing was paired and no keys were saved. Start over on both machines and compare the new code.", "Start over")
        case "RATE_LIMITED":
            return ("timer", "Too many attempts", "\(who) paused pairing after repeated failed tries. \(e?.remediation ?? "Wait a while, then try again.")", "OK")
        case "PEER_UNREACHABLE" where !msg.contains("daemon is not running"):
            return ("wifi.slash", "Can't reach \(session.target.isEmpty ? who : session.target)",
                    "Check that both machines are on the same network and that a VPN isn't routing local traffic.", "Run diagnostics")
        case "TIMEOUT":
            return ("clock", "\(who) didn't answer in time", e?.remediation ?? "Open Kith on both machines and choose Pair on each.", "Try again")
        default:
            if msg.contains("pairing mode") || msg.contains("not pairing") || msg.contains("not accepting") {
                return ("antenna.radiowaves.left.and.right.slash", "\(who) isn't in pairing mode",
                        "Kith on \(who) answered but isn't waiting to pair. On \(who), open Kith and choose Pair, then try again.", "Try again")
            }
            if msg.contains("rejected by the user") {
                return ("exclamationmark.shield", "\(who) said the codes didn't match",
                        "Nothing was paired and no keys were saved. Start over on both machines and compare the new code.", "Start over")
            }
            if msg.contains("mismatch") || msg.contains("did not match") || msg.contains("aborted") {
                return ("exclamationmark.shield", "Pairing didn't finish",
                        "Nothing was paired and no keys were saved. \(e?.message ?? "")", "Start over")
            }
            return ("exclamationmark.triangle", "Pairing failed", [e?.message, e?.remediation].compactMap { $0 }.joined(separator: " "), "Try again")
        }
    }
}

/// The hero link: draws itself while waiting, dims while comparing, snaps
/// solid with one radial pulse and a check when paired.
private struct PairLink: View {
    var step: PairSession.Step
    @State private var drawn: CGFloat = 0
    @State private var breathe = false
    @State private var ring = false
    @State private var check = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(spacing: 0) {
            DeviceGlyph(symbol: "laptopcomputer", size: 60)
            ZStack {
                Line().trim(from: 0, to: drawn)
                    .stroke(K.acc, style: StrokeStyle(lineWidth: step == .paired ? 2 : 1.5, lineCap: .round))
                    .opacity(step == .waiting ? (breathe ? 1 : 0.45) : step == .paired ? 1 : 0.55)
                    .padding(.horizontal, 10)
                if step == .paired {
                    Circle().strokeBorder(K.acc, lineWidth: 1.5).frame(width: 18, height: 18)
                        .scaleEffect(ring ? 5 : 0.2).opacity(ring ? 0 : 0.7)
                    Image(systemName: "checkmark").font(.system(size: 11, weight: .heavy)).foregroundStyle(.white)
                        .frame(width: 22, height: 22).background(Circle().fill(K.acc))
                        .scaleEffect(check ? 1 : 0.4).opacity(check ? 1 : 0)
                }
            }
            .frame(width: 170, height: 20)
            DeviceGlyph(symbol: "desktopcomputer", size: 60, dim: step == .waiting)
                .animation(.smooth(duration: 0.4), value: step)
        }
        .onAppear { animate(step) }
        .onChange(of: step) { _, s in animate(s) }
    }

    private func animate(_ s: PairSession.Step) {
        if reduceMotion { drawn = 1; ring = false; check = s == .paired; return }
        switch s {
        case .waiting:
            drawn = 0
            withAnimation(.easeInOut(duration: 1.2)) { drawn = 1 }
            withAnimation(.easeInOut(duration: 1.2).repeatForever(autoreverses: true).delay(1.2)) { breathe = true }
        case .paired:
            drawn = 1
            breathe = false
            withAnimation(.easeOut(duration: 1.1).delay(0.35)) { ring = true }
            withAnimation(.spring(response: 0.4, dampingFraction: 0.6).delay(0.55)) { check = true }
        default:
            drawn = 1
            breathe = false
        }
    }
}

/// "347 X0F": characters reveal one by one (blur 6 → 0, 60 ms stagger).
private struct CodeReveal: View {
    var code: String
    @State private var shown = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(spacing: 2) {
            ForEach(Array(code.enumerated()), id: \.offset) { i, ch in
                Text(ch == " " ? "\u{2002}" : String(ch))
                    .font(.system(size: 62, weight: .medium, design: .monospaced))
                    .blur(radius: shown ? 0 : 6)
                    .opacity(shown ? 1 : 0)
                    .offset(y: shown ? 0 : 4)
                    .animation(.spring(response: 0.4, dampingFraction: 0.8).delay(0.25 + Double(i) * 0.06), value: shown)
            }
        }
        .textSelection(.enabled)
        .onAppear { if reduceMotion { shown = true } else { DispatchQueue.main.async { shown = true } } }
        .accessibilityLabel("Pairing code \(code)")
    }
}
