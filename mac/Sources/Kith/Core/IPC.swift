import Foundation
import Darwin

/// An error reported by the daemon (protocol.Error) or by the transport.
struct KithError: Error, LocalizedError, Decodable, Equatable {
    var code: String
    var message: String
    var remediation: String?

    var errorDescription: String? { message }

    static let notRunning = KithError(code: "PEER_UNREACHABLE", message: "The Kith daemon isn't running.",
                                      remediation: "Start it from the menu bar or with `kith daemon start`.")
    static func transport(_ m: String) -> KithError { KithError(code: "INTERNAL", message: m) }
}

/// The daemon's home directory (KITH_HOME or ~/.kith), as config.ResolveHome.
enum KithHome {
    static var path: String {
        if let h = ProcessInfo.processInfo.environment["KITH_HOME"], !h.isEmpty { return h }
        return FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".kith").path
    }
    static var socket: String { (path as NSString).appendingPathComponent("ipc.sock") }
}

/// One IPC connection: newline-delimited JSON over the daemon's unix socket.
/// The client sends one request line; the daemon pushes event lines, then a
/// final line carrying the result or error (internal/ipc).
final class IPCConnection: @unchecked Sendable {
    private let fd: Int32
    private var buffer = Data()
    private let lock = NSLock()
    private var closed = false

    init(path: String = KithHome.socket, timeout: Int = 120) throws {
        fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw KithError.transport("socket() failed") }
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8)
        guard bytes.count < MemoryLayout.size(ofValue: addr.sun_path) else {
            Darwin.close(fd)
            throw KithError.transport("socket path too long")
        }
        withUnsafeMutableBytes(of: &addr.sun_path) { $0.copyBytes(from: bytes) }
        let ok = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) == 0
            }
        }
        guard ok else {
            Darwin.close(fd)
            throw KithError.notRunning
        }
        var one: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &one, socklen_t(MemoryLayout<Int32>.size))
        var tv = timeval(tv_sec: timeout, tv_usec: 0)
        setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))
    }

    deinit { close() }

    func send(_ object: Any) throws {
        var data = try JSONSerialization.data(withJSONObject: object)
        data.append(0x0A)
        try data.withUnsafeBytes { raw in
            var off = 0
            while off < raw.count {
                let n = Darwin.write(fd, raw.baseAddress! + off, raw.count - off)
                if n <= 0 { throw KithError.transport("the daemon closed the connection") }
                off += n
            }
        }
    }

    /// Reads one line (blocking).
    func readLine() throws -> Data {
        while true {
            if let i = buffer.firstIndex(of: 0x0A) {
                let line = buffer.subdata(in: buffer.startIndex..<i)
                buffer.removeSubrange(buffer.startIndex...i)
                return line
            }
            var chunk = [UInt8](repeating: 0, count: 64 << 10)
            let n = Darwin.read(fd, &chunk, chunk.count)
            if n <= 0 { throw KithError.transport(n == 0 ? "the daemon closed the connection" : "timed out waiting for the daemon") }
            buffer.append(contentsOf: chunk[0..<n])
        }
    }

    /// Unblocks a pending read from another thread.
    func cancel() { Darwin.shutdown(fd, SHUT_RDWR) }

    func close() {
        lock.lock(); defer { lock.unlock() }
        if !closed { closed = true; Darwin.close(fd) }
    }
}

/// A decoded server line.
enum IPCMessage {
    case event(String, Data)
    case result(Data)
    case failure(KithError)

    init(line: Data) throws {
        guard let obj = try JSONSerialization.jsonObject(with: line) as? [String: Any] else {
            throw KithError.transport("unexpected reply from the daemon")
        }
        if obj["final"] as? Bool == true {
            if let e = obj["error"] {
                let d = try JSONSerialization.data(withJSONObject: e)
                self = .failure(try JSONDecoder().decode(KithError.self, from: d))
            } else if let r = obj["result"] {
                self = .result(try JSONSerialization.data(withJSONObject: r, options: .fragmentsAllowed))
            } else {
                self = .result(Data("null".utf8))
            }
            return
        }
        let data = obj["data"].flatMap { try? JSONSerialization.data(withJSONObject: $0, options: .fragmentsAllowed) } ?? Data("null".utf8)
        self = .event(obj["event"] as? String ?? "", data)
    }
}

enum IPC {
    static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        return d
    }()

    /// One request → its final result, decoded. Runs off the main thread.
    static func call<T: Decodable>(_ method: String, _ params: [String: Any]? = nil, as: T.Type = T.self, timeout: Int = 120) async throws -> T {
        let data = try await raw(method, params, timeout: timeout)
        do {
            return try decoder.decode(T.self, from: data)
        } catch {
            throw KithError.transport("could not read \(method) reply: \(error.localizedDescription)")
        }
    }

    static func raw(_ method: String, _ params: [String: Any]? = nil, timeout: Int = 120) async throws -> Data {
        let box = ParamsBox(params)
        return try await Task.detached(priority: .userInitiated) {
            let c = try IPCConnection(timeout: timeout)
            defer { c.close() }
            var req: [String: Any] = ["method": method]
            if let p = box.value { req["params"] = p }
            try c.send(req)
            while true {
                switch try IPCMessage(line: try c.readLine()) {
                case .event: continue
                case .result(let d): return d
                case .failure(let e): throw e
                }
            }
        }.value
    }

    /// Fire-and-forget calls whose reply we only need to check for errors.
    static func send(_ method: String, _ params: [String: Any]? = nil) async throws {
        _ = try await raw(method, params)
    }
}

/// Moves untyped JSON params across the actor boundary.
struct ParamsBox: @unchecked Sendable {
    let value: [String: Any]?
    init(_ v: [String: Any]?) { value = v }
}
