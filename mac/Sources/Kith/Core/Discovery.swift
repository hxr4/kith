import Foundation
import Network

/// Kith daemons advertising _kith._tcp on the local network (TXT: v, fp,
/// name, mode). Each result is resolved to an IPv4 address for `pair`.
@MainActor @Observable
final class Discovery {
    struct Machine: Identifiable, Equatable {
        var id: String          // service instance name
        var name: String
        var display: String     // KITH-XXXX-…
        var mode: String
        var address: String?    // ip:port once resolved
    }

    var machines: [Machine] = []
    var failed: String?
    private var browser: NWBrowser?
    private var resolving: [String: NWConnection] = [:]

    func start(excluding ownDisplay: String?) {
        stop()
        let b = NWBrowser(for: .bonjourWithTXTRecord(type: "_kith._tcp", domain: "local."), using: .tcp)
        b.browseResultsChangedHandler = { [weak self] results, _ in
            Task { @MainActor in self?.update(results, own: ownDisplay) }
        }
        b.stateUpdateHandler = { [weak self] st in
            if case .failed(let e) = st { Task { @MainActor in self?.failed = e.localizedDescription } }
        }
        b.start(queue: .main)
        browser = b
    }

    func stop() {
        browser?.cancel()
        browser = nil
        resolving.values.forEach { $0.cancel() }
        resolving = [:]
    }

    private func update(_ results: Set<NWBrowser.Result>, own: String?) {
        var next: [Machine] = []
        for r in results {
            guard case let .service(name, _, _, _) = r.endpoint else { continue }
            var txt: [String: String] = [:]
            if case let .bonjour(rec) = r.metadata { txt = rec.dictionary }
            guard txt["v"] == "1", let fp = txt["fp"], fp != own else { continue }
            let old = machines.first { $0.id == name }
            next.append(Machine(id: name, name: txt["name"] ?? name, display: fp, mode: txt["mode"] ?? "", address: old?.address))
            if old?.address == nil && resolving[name] == nil { resolve(r.endpoint, id: name) }
        }
        machines = next.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
    }

    /// Opens (and immediately closes) a TCP connection to learn the address.
    private func resolve(_ endpoint: NWEndpoint, id: String) {
        let params = NWParameters.tcp
        if let ip = params.defaultProtocolStack.internetProtocol as? NWProtocolIP.Options { ip.version = .v4 }
        let c = NWConnection(to: endpoint, using: params)
        resolving[id] = c
        c.stateUpdateHandler = { [weak self] st in
            switch st {
            case .ready:
                var addr: String?
                if case let .hostPort(host, port)? = c.currentPath?.remoteEndpoint {
                    var h = "\(host)"
                    if let pct = h.firstIndex(of: "%") { h = String(h[..<pct]) }
                    addr = "\(h):\(port.rawValue)"
                }
                c.cancel()
                Task { @MainActor in
                    guard let self else { return }
                    self.resolving[id] = nil
                    if let i = self.machines.firstIndex(where: { $0.id == id }) { self.machines[i].address = addr }
                }
            case .failed, .cancelled:
                Task { @MainActor in self?.resolving[id] = nil }
            default: break
            }
        }
        c.start(queue: .main)
    }
}
