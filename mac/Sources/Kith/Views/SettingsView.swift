import SwiftUI
import ServiceManagement

/// The standard Settings window: General, Daemon, Command line, About.
struct SettingsView: View {
    var body: some View {
        TabView {
            GeneralTab().tabItem { Label("General", systemImage: "gearshape") }
            DaemonTab().tabItem { Label("Daemon", systemImage: "server.rack") }
            CLITab().tabItem { Label("Command line", systemImage: "terminal") }
            AboutTab().tabItem { Label("About", systemImage: "info.circle") }
        }
        .frame(width: 640, height: 470)
        .tint(K.acc)
        .font(K.ui(13))
        .foregroundStyle(K.l1)
    }
}

/// A grouped card of rows, as in the design.
private struct SettingsCard<Content: View>: View {
    @ViewBuilder var content: Content
    var body: some View {
        VStack(spacing: 0) { content }.card(radius: 10)
    }
}

private struct SettingsRow<Trailing: View>: View {
    var title: String
    var note: String? = nil
    var divider = true
    @ViewBuilder var trailing: Trailing

    var body: some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                if let note { Text(note).font(K.ui(11.5)).foregroundStyle(K.l2).fixedSize(horizontal: false, vertical: true) }
            }
            Spacer(minLength: 12)
            trailing
        }
        .padding(.horizontal, 14).padding(.vertical, 10)
        .overlay(alignment: .bottom) { if divider { Divider05() } }
    }
}

private struct SettingsPage<Content: View>: View {
    @ViewBuilder var content: Content
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) { content }
                .padding(.horizontal, 28).padding(.vertical, 20)
        }
        .background(K.win)
    }
}

// MARK: - General

private struct GeneralTab: View {
    @Environment(KithStore.self) private var store
    @State private var name = ""
    @State private var policy = "ask"
    @State private var login = SMAppService.mainApp.status == .enabled

    var body: some View {
        SettingsPage {
            SettingsCard {
                SettingsRow(title: "Device name", note: "How this Mac appears to paired machines.") {
                    TextField("", text: $name).textFieldStyle(.roundedBorder).frame(width: 220)
                        .onSubmit { Task { await store.setConfig("name", name.trimmingCharacters(in: .whitespaces)) } }
                }
                SettingsRow(title: "Open Kith at login", note: "Starts quietly in the menu bar.", divider: false) {
                    Toggle("", isOn: $login).toggleStyle(.switch).tint(K.acc).labelsHidden()
                        .onChange(of: login) { _, on in
                            do {
                                if on { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
                            } catch {
                                store.fail(KithError(code: "LOGIN_ITEM", message: "Couldn't change the login item: \(error.localizedDescription)",
                                                     remediation: "Move Kith to Applications and try again."))
                                login = SMAppService.mainApp.status == .enabled
                            }
                        }
                }
            }
            SettingsCard {
                SettingsRow(title: "Default policy for new peers",
                            note: policy == "auto" ? "New peers run tasks as you without asking." : "You approve each task a new peer sends.",
                            divider: false) {
                    Segmented(options: [("ask", "Ask"), ("auto", "Auto")], selection: $policy)
                }
            }
            Text("Each paired machine keeps its own policy; change it in Machines.").font(K.ui(11.5)).foregroundStyle(K.l2)
        }
        .onAppear { sync() }
        .onChange(of: store.config) { _, _ in sync() }
        .onChange(of: policy) { _, p in Task { await store.setConfig("default_policy", p) } }
    }

    private func sync() {
        name = store.config["name"] ?? store.thisName
        policy = store.config["default_policy"] ?? "ask"
    }
}

// MARK: - Daemon

private struct DaemonTab: View {
    @Environment(KithStore.self) private var store
    @State private var port = ""
    @State private var mdns = true
    @State private var logLevel = "info"
    @State private var bind = "0.0.0.0"
    @State private var agent = false

    var body: some View {
        SettingsPage {
            SettingsCard {
                HStack(spacing: 12) {
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            Circle().fill(dotColor).frame(width: 8, height: 8)
                            Text(statusText).font(K.ui(13, .medium)).contentTransition(.interpolate)
                        }
                        Text(detail).font(K.mono(11.5)).foregroundStyle(K.l2).lineLimit(1).truncationMode(.middle)
                        Text("Service: \(store.service.ifEmpty("unknown"))").font(K.ui(11.5)).foregroundStyle(K.l2)
                            .lineLimit(2).fixedSize(horizontal: false, vertical: true)
                    }
                    Spacer()
                    Button("Start") { Task { await store.daemon("start") } }
                        .buttonStyle(.k(.secondary, height: 24)).disabled(store.running || store.daemonBusy != nil)
                    Button("Stop") { Task { await store.daemon("stop") } }
                        .buttonStyle(.k(.secondary, height: 24)).disabled(!store.running || store.daemonBusy != nil)
                    Button("Restart") { Task { await store.daemon("restart") } }
                        .buttonStyle(.k(.secondary, height: 24)).disabled(!store.running || store.daemonBusy != nil)
                }
                .padding(.horizontal, 14).padding(.vertical, 12)
                .overlay(alignment: .bottom) { Divider05() }
                SettingsRow(title: "Run in background at login", note: "Installs a LaunchAgent at ~/Library/LaunchAgents/dev.kith.daemon.plist, so peers reach this Mac even when the app is closed.", divider: false) {
                    Toggle("", isOn: $agent).toggleStyle(.switch).tint(K.acc).labelsHidden()
                        .onChange(of: agent) { _, on in
                            guard on != store.serviceInstalled else { return }
                            Task { await store.daemon(on ? "install" : "uninstall") }
                        }
                }
            }
            SettingsCard {
                SettingsRow(title: "TCP port") {
                    TextField("7420", text: $port).textFieldStyle(.roundedBorder).font(K.mono(12)).multilineTextAlignment(.trailing)
                        .frame(width: 80)
                        .onSubmit { Task { await store.setConfig("port", port) } }
                }
                SettingsRow(title: "Bind address") {
                    Picker("", selection: $bind) {
                        Text("0.0.0.0  all interfaces").tag("0.0.0.0")
                        if let ip = store.localAddress { Text("\(ip)  this network only").tag(ip) }
                        if !["0.0.0.0", store.localAddress ?? ""].contains(bind) { Text(bind).tag(bind) }
                    }
                    .labelsHidden().fixedSize()
                    .onChange(of: bind) { _, b in Task { await store.setConfig("bind", b) } }
                }
                SettingsRow(title: "Discover peers with mDNS", note: "Advertises _kith._tcp on the local network.") {
                    Toggle("", isOn: $mdns).toggleStyle(.switch).tint(K.acc).labelsHidden()
                        .onChange(of: mdns) { _, on in Task { await store.setConfig("mdns", on ? "true" : "false") } }
                }
                SettingsRow(title: "Log level", divider: false) {
                    Picker("", selection: $logLevel) {
                        ForEach(["debug", "info", "warn", "error"], id: \.self) { Text($0).tag($0) }
                    }
                    .labelsHidden().fixedSize()
                    .onChange(of: logLevel) { _, l in Task { await store.setConfig("log_level", l) } }
                    Button("Open logs") {
                        NSWorkspace.shared.open(URL(fileURLWithPath: KithHome.path).appendingPathComponent("daemon.log"))
                    }
                    .buttonStyle(.k(.secondary, height: 24))
                }
            }
            if store.configDirty {
                HStack {
                    Text("Restart the daemon to apply changes.").font(K.ui(12)).foregroundStyle(K.l2)
                    Spacer()
                    Button("Restart now") { Task { await store.daemon("restart") } }.buttonStyle(.k(.primary, height: 24))
                }
            }
        }
        .onAppear { sync() }
        .onChange(of: store.config) { _, _ in sync() }
        .onChange(of: store.service) { _, _ in agent = store.serviceInstalled }
        .task { await store.loadConfig(); await store.refreshService(); agent = store.serviceInstalled }
    }

    private func sync() {
        port = store.config["port"] ?? "7420"
        mdns = store.config["mdns"] != "false"
        logLevel = store.config["log_level"] ?? "info"
        bind = store.config["bind"] ?? "0.0.0.0"
    }

    private var statusText: String {
        if let b = store.daemonBusy { return b }
        return store.running ? "Running" : "Stopped"
    }

    private var dotColor: Color { store.daemonBusy != nil ? K.orangeDot : store.running ? K.greenDot : K.l3 }

    private var detail: String {
        if let s = store.status { return "kithd \(s.version) · pid \(s.pid) · \(store.config["bind"] ?? "0.0.0.0"):\(s.port)" }
        return "peers can't reach this Mac"
    }
}

// MARK: - Command line

private struct CLITab: View {
    @Environment(KithStore.self) private var store
    @State private var onPath: String?
    @State private var checked = false
    @State private var copied = false

    private let snippet = """
    {
      "mcpServers": {
        "kith": {
          "command": "kith",
          "args": ["mcp"]
        }
      }
    }
    """

    var body: some View {
        SettingsPage {
            SettingsCard {
                HStack(spacing: 12) {
                    Image(systemName: onPath != nil ? "checkmark.circle.fill" : "exclamationmark.circle.fill")
                        .font(.system(size: 20)).foregroundStyle(onPath != nil ? K.green : K.orange)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(onPath != nil ? "kith is on your PATH" : checked ? "kith isn't on your PATH" : "Checking…").font(K.ui(13, .medium))
                        Text(onPath ?? "Links Kith's bundled CLI into ~/.local/bin and adds that folder to PATH in ~/.zshrc. No password needed.")
                            .font(onPath != nil ? K.mono(11.5) : K.ui(11.5)).foregroundStyle(K.l2).fixedSize(horizontal: false, vertical: true)
                    }
                    Spacer()
                    if onPath == nil && checked {
                        Button("Install kith command") { install() }.buttonStyle(.k(.primary, height: 26))
                    }
                }
                .padding(.horizontal, 14).padding(.vertical, 12)
            }
            VStack(alignment: .leading, spacing: 6) {
                Text("Use Kith from an AI agent").font(K.ui(13, .medium))
                Text("Add this to your agent's MCP config. The agent can then list peers, send tasks and read logs. Tasks it sends still follow each peer's Ask or Auto policy.")
                    .font(K.ui(11.5)).foregroundStyle(K.l2).fixedSize(horizontal: false, vertical: true)
                ZStack(alignment: .topTrailing) {
                    Text(snippet).font(K.mono(11.5)).lineSpacing(3).textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 14).padding(.vertical, 12)
                        .card(radius: 10, fill: K.con)
                    Button {
                        store.copy(snippet, label: "MCP config")
                        withAnimation(K.snappy) { copied = true }
                        Task { try? await Task.sleep(for: .seconds(1.4)); withAnimation(K.smooth) { copied = false } }
                    } label: {
                        Label(copied ? "Copied" : "Copy", systemImage: copied ? "checkmark" : "doc.on.doc").contentTransition(.symbolEffect(.replace))
                    }
                    .buttonStyle(.k(.secondary, height: 24)).padding(8)
                }
                .padding(.top, 2)
            }
        }
        .task { onPath = await KithCLI.onPath(); checked = true }
    }

    private func install() {
        guard let bin = KithCLI.bundled ?? KithCLI.binary else {
            store.flash("The bundled kith binary is missing; reinstall Kith.", error: true)
            return
        }
        let home = FileManager.default.homeDirectoryForCurrentUser
        let dir = home.appendingPathComponent(".local/bin")
        let link = dir.appendingPathComponent("kith")
        do {
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            try? FileManager.default.removeItem(at: link)
            try FileManager.default.createSymbolicLink(atPath: link.path, withDestinationPath: bin)
            if !KithCLI.loginPath.split(separator: ":").contains(where: { $0 == dir.path || $0 == "$HOME/.local/bin" }) {
                let rc = home.appendingPathComponent(".zshrc")
                let existing = (try? String(contentsOf: rc, encoding: .utf8)) ?? ""
                if !existing.contains("# >>> kith >>>") {
                    let block = "\n# >>> kith >>>\nexport PATH=\"$HOME/.local/bin:$PATH\"\n# <<< kith <<<\n"
                    try (existing + block).write(to: rc, atomically: true, encoding: .utf8)
                }
            }
            onPath = link.path
            store.flash("Installed kith. Open a new terminal window to use it.")
        } catch {
            store.fail(error)
        }
    }
}

// MARK: - About

private struct AboutTab: View {
    @Environment(KithStore.self) private var store

    var body: some View {
        SettingsPage {
            VStack(spacing: 6) {
                RoundedRectangle(cornerRadius: 15, style: .continuous).fill(K.acc)
                    .frame(width: 64, height: 64)
                    .overlay(KithMark(color: .white, width: 38))
                    .shadow(color: .black.opacity(0.15), radius: 7, y: 4)
                Text("Kith").font(K.ui(15, .semibold)).padding(.top, 4)
                Text("Version \(appVersion)").font(K.ui(12)).foregroundStyle(K.l2)
            }
            .frame(maxWidth: .infinity).padding(.top, 8)
            SettingsCard {
                SettingsRow(title: "This Mac's fingerprint") {
                    if let s = store.status { CopyableMono(text: s.display, label: "fingerprint", size: 12) }
                    else { Text("start the daemon to show it").font(K.ui(12)).foregroundStyle(K.l2) }
                }
                SettingsRow(title: "Data folder", divider: false) {
                    Text(abbreviate(KithHome.path)).font(K.mono(11.5)).foregroundStyle(K.l2)
                    Button("Reveal") { NSWorkspace.shared.open(URL(fileURLWithPath: KithHome.path)) }
                        .buttonStyle(.k(.secondary, height: 22))
                }
            }
            Text("Kith runs tasks from paired machines as you. It verifies requirements, not intent; there is no sandbox.")
                .font(K.ui(11.5)).foregroundStyle(K.l2)
        }
    }

    private var appVersion: String {
        let v = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String
        let daemon = store.status?.version
        return [v ?? daemon ?? "dev", daemon.flatMap { $0 != v ? "kithd \($0)" : nil }].compactMap { $0 }.joined(separator: " · ")
    }
}
