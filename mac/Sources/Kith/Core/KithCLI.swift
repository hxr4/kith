import Foundation

/// Runs the bundled `kith` CLI for what isn't on the IPC socket (daemon
/// control, config, doctor, check, submit). Apps launched from Finder get a
/// minimal PATH, so commands run with the login shell's PATH: `kith daemon
/// install` snapshots it into the LaunchAgent, and tasks need python there.
enum KithCLI {
    /// Kith.app/Contents/Helpers/kith (not MacOS/: "Kith" and "kith" would
    /// collide on a case-insensitive volume).
    static var bundled: String? {
        let p = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/kith").path
        return FileManager.default.isExecutableFile(atPath: p) ? p : nil
    }

    static var binary: String? {
        var candidates: [String] = []
        if let b = bundled { candidates.append(b) }
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        candidates += ["\(home)/.local/bin/kith", "/usr/local/bin/kith", "/opt/homebrew/bin/kith"]
        return candidates.first { FileManager.default.isExecutableFile(atPath: $0) }
    }

    /// The login shell's PATH, resolved once.
    static let loginPath: String = {
        let shell = ProcessInfo.processInfo.environment["SHELL"] ?? "/bin/zsh"
        let p = Process()
        p.executableURL = URL(fileURLWithPath: shell)
        p.arguments = ["-l", "-c", "printf %s \"$PATH\""]
        let out = Pipe()
        p.standardOutput = out
        p.standardError = FileHandle.nullDevice
        p.standardInput = FileHandle.nullDevice
        do {
            try p.run()
            let deadline = Date().addingTimeInterval(5)
            while p.isRunning && Date() < deadline { usleep(20_000) }
            if p.isRunning { p.terminate() }
            let s = String(data: out.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
            if !s.isEmpty { return s }
        } catch {}
        return "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
    }()

    struct Output {
        var text: String
        var status: Int32
    }

    static func run(_ args: [String], timeout: TimeInterval = 60) async throws -> Output {
        guard let bin = binary else {
            throw KithError(code: "NOT_FOUND", message: "The kith command isn't installed.",
                            remediation: "Reinstall Kith, or install the CLI from Settings › Command line.")
        }
        return try await Task.detached(priority: .userInitiated) {
            let p = Process()
            p.executableURL = URL(fileURLWithPath: bin)
            p.arguments = args
            var env = ProcessInfo.processInfo.environment
            env["PATH"] = loginPath
            env["NO_COLOR"] = "1"
            p.environment = env
            let pipe = Pipe()
            p.standardOutput = pipe
            p.standardError = pipe
            p.standardInput = FileHandle.nullDevice
            try p.run()
            // Drain while running so a chatty command can't fill the pipe.
            var data = Data()
            let reader = Task.detached { pipe.fileHandleForReading.readDataToEndOfFile() }
            let deadline = Date().addingTimeInterval(timeout)
            while p.isRunning && Date() < deadline { try? await Task.sleep(for: .milliseconds(50)) }
            if p.isRunning {
                p.terminate()
                throw KithError(code: "TIMEOUT", message: "kith \(args.first ?? "") timed out")
            }
            data = await reader.value
            return Output(text: String(data: data, encoding: .utf8) ?? "", status: p.terminationStatus)
        }.value
    }

    /// Commands that report failure through the exit code still print JSON.
    static func json<T: Decodable>(_ args: [String], as: T.Type = T.self, timeout: TimeInterval = 60) async throws -> T {
        let out = try await run(args + ["--json"], timeout: timeout)
        if let i = out.text.firstIndex(where: { $0 == "{" || $0 == "[" }),
           let v = try? IPC.decoder.decode(T.self, from: Data(out.text[i...].utf8)) {
            return v
        }
        let msg = out.text.trimmingCharacters(in: .whitespacesAndNewlines)
        throw parseCLIError(msg.isEmpty ? "kith \(args.joined(separator: " ")) failed" : msg)
    }

    /// Plain commands; a non-zero exit becomes an error with the CLI's text.
    @discardableResult
    static func plain(_ args: [String], timeout: TimeInterval = 60) async throws -> String {
        let out = try await run(args, timeout: timeout)
        let text = out.text.trimmingCharacters(in: .whitespacesAndNewlines)
        if out.status != 0 { throw parseCLIError(text.isEmpty ? "kith \(args.joined(separator: " ")) failed" : text) }
        return text
    }

    /// "CODE: message\nFix: remediation" → KithError
    static func parseCLIError(_ text: String) -> KithError {
        var lines = text.split(separator: "\n").map(String.init)
        var fix: String?
        if let i = lines.firstIndex(where: { $0.hasPrefix("Fix: ") }) {
            fix = String(lines[i].dropFirst(5))
            lines.remove(at: i)
        }
        let first = lines.joined(separator: "\n")
        if let colon = first.firstIndex(of: ":"), first[..<colon].allSatisfy({ $0.isUppercase || $0 == "_" }) {
            return KithError(code: String(first[..<colon]), message: first[first.index(after: colon)...].trimmingCharacters(in: .whitespaces), remediation: fix)
        }
        return KithError(code: "CLI", message: first, remediation: fix)
    }

    /// Whether `kith` resolves on the login shell's PATH, and where.
    static func onPath() async -> String? {
        await Task.detached {
            for dir in loginPath.split(separator: ":") {
                let p = "\(dir)/kith"
                if FileManager.default.isExecutableFile(atPath: p) { return p }
            }
            return nil
        }.value
    }
}
