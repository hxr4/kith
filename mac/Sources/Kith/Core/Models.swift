import Foundation

// Shapes of the daemon's IPC replies (internal/daemon/api.go). Keys arrive in
// snake_case and are converted by IPC.decoder.

struct StatusView: Decodable, Equatable {
    var fp: String
    var display: String
    var name: String
    var port: Int
    var home: String
    var version: String
    var peers: Int
    var online: Int
    var mdns: Bool
    var pid: Int
}

struct GPU: Decodable, Hashable {
    var name: String
    var vramGb: Double
    var freeVramGb: Double
}

struct Manifest: Decodable, Equatable {
    var os: String
    var arch: String
    var python: [String]?
    var packages: [String: String]?
    var gpus: [GPU]?
    var freeDiskGb: Double
    var ramGb: Double
    var mode: String?
    var kithVer: String?

    /// "Windows · RTX 3050 6 GB · 5.1 GB free" (the popover's capability line).
    var summary: String {
        var parts = [Fmt.osName(os)]
        if let g = gpus?.first {
            parts.append("\(Fmt.gpuShort(g.name)) \(Fmt.gb(g.vramGb))")
            parts.append("\(Fmt.gb(g.freeVramGb)) free")
        } else {
            parts.append("\(Fmt.gb(ramGb)) RAM")
        }
        return parts.joined(separator: " · ")
    }
}

struct Peer: Decodable, Identifiable, Equatable {
    var fp: String
    var display: String
    var name: String
    var policy: String?
    var lastAddr: String?
    var lastSeen: String?
    var reachable: Bool
    var caps: String?
    var manifest: Manifest?
    var id: String { fp }

    var isWindows: Bool { manifest?.os.lowercased().hasPrefix("windows") ?? false }
    var isMac: Bool {
        guard let os = manifest?.os.lowercased() else { return false }
        return os.hasPrefix("darwin") || os.hasPrefix("mac")
    }
    var symbol: String { isWindows ? "pc" : manifest == nil || isMac ? "desktopcomputer" : "server.rack" }
}

struct Transition: Decodable, Hashable {
    var seq: Int
    var state: String
    var detail: String?
    var ts: String
}

struct Artifact: Decodable, Identifiable, Hashable {
    var id: String
    var taskId: String
    var path: String
    var sha256: String
    var size: Int64
    var location: String
    var producerFp: String
    var fetched: Bool
    var copyBack: Bool
    var producerOffline: Bool
    var status: String
}

struct KTask: Decodable, Identifiable, Equatable {
    var id: String
    var role: String
    var peerFp: String
    var project: String
    var name: String
    var state: String
    var detail: String?
    var attempt: Int
    var pendingApproval: Bool
    var wsDir: String?
    var createdAt: String
    var updatedAt: String
    var projectDir: String?
    var code: String?
    var remediation: String?
    var startedAt: String?
    var logSize: Int64
    var peerName: String?
    var argv: [String]?
    var timeline: [Transition]?
    var artifacts: [Artifact]?
    var pid: Int?
    var inputs: [FileOffer]?
    var outputs: [String]?
    var requirements: [CheckResult.Requirement]?

    var sent: Bool { role == "requester" }
    var ended: Bool { TaskState.isEnded(state) }
    var needsApproval: Bool { role == "executor" && state == TaskState.submitted && pendingApproval }
    var peerLabel: String { peerName?.isEmpty == false ? peerName! : String(peerFp.prefix(8)) }
    var created: Date? { Fmt.date(createdAt) }
    var updated: Date? { Fmt.date(updatedAt) }
    var started: Date? { startedAt.flatMap(Fmt.date) }
}

struct FileOffer: Decodable, Hashable {
    var path: String
    var size: Int64
}

struct LogsResult: Decodable {
    var taskId: String
    var offset: Int64
    var state: String
    var done: Bool
    var data: String?
}

struct FetchResult: Decodable {
    var taskId: String
    var path: String
    var localPath: String
    var sha256: String
    var size: Int64
}

struct SubmitResult: Decodable {
    var taskId: String
    var state: String
    var pendingApproval: Bool?
    var detail: String?
    var remediation: String?
    var code: String?
}

/// One `kith doctor --json` line.
struct Check: Decodable, Identifiable, Equatable {
    var name: String
    var status: String // ok | warn | fail | info
    var detail: String
    var fix: String?
    var id: String { name }
}

/// `kith check --json`: a parsed task plus a preflight against the peer.
struct CheckResult: Decodable, Equatable {
    struct Input: Decodable, Equatable { var path: String; var size: Int64 }
    struct Requirement: Decodable, Equatable, Identifiable {
        var req: String
        var have: String
        var status: String // pass | fail | unknown
        var id: String { req }
    }
    var file: String
    var projectDir: String
    var name: String
    var project: String
    var command: [String]
    var inputs: [Input]
    var inputBytes: Int64
    var outputs: [String]?
    var timeoutMinutes: Int
    var onInterrupt: String
    var probe: [String]?
    var peer: String?
    var peerName: String?
    var reachable: Bool
    var requirements: [Requirement]
    var problem: String?
    var ok: Bool
}

/// Pairing stream events (internal/daemon/pairing.go pairEvent).
struct PairEvent: Decodable {
    var kind: String?
    var code: String?
    var peer: Peer?
    var addr: String?
    var message: String?
}

enum TaskState {
    static let submitted = "SUBMITTED", accepted = "ACCEPTED", rejected = "REJECTED", transferring = "TRANSFERRING",
               running = "RUNNING", finalizing = "FINALIZING", cancelling = "CANCELLING", interrupted = "INTERRUPTED",
               completed = "COMPLETED", failed = "FAILED", cancelled = "CANCELLED"
    static let all = [submitted, accepted, transferring, running, finalizing, cancelling, interrupted,
                      completed, failed, cancelled, rejected]
    static func isEnded(_ s: String) -> Bool { s == completed || s == failed || s == cancelled || s == rejected }
    static func isLive(_ s: String) -> Bool { s == transferring || s == running || s == cancelling }
}
