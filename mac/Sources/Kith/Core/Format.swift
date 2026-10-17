import Foundation

/// Formatting shared by every screen: sizes, durations, relative times.
enum Fmt {
    private static let isoFrac: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()
    private static let iso = ISO8601DateFormatter()

    static func date(_ s: String) -> Date? {
        if s.isEmpty { return nil }
        return isoFrac.date(from: s) ?? iso.date(from: s)
    }

    static func bytes(_ n: Int64) -> String {
        let d = Double(n)
        switch n {
        case 1 << 30 ..< Int64.max: return String(format: "%.1f GB", d / Double(1 << 30))
        case 1 << 20 ..< 1 << 30: return String(format: "%.1f MB", d / Double(1 << 20))
        case 1 << 10 ..< 1 << 20: return String(format: "%.0f KB", d / Double(1 << 10))
        default: return "\(n) B"
        }
    }

    static func gb(_ v: Double) -> String {
        v >= 10 || v.rounded() == v ? String(format: "%.0f GB", v) : String(format: "%.1f GB", v)
    }

    /// 0:42, 9:40, 1:02:03
    static func clock(_ seconds: TimeInterval) -> String {
        let s = max(0, Int(seconds))
        if s >= 3600 { return String(format: "%d:%02d:%02d", s / 3600, s / 60 % 60, s % 60) }
        return String(format: "%d:%02d", s / 60, s % 60)
    }

    /// "now", "14 s ago", "3 min ago", "2 h ago", "yesterday", "Oct 12"
    static func ago(_ d: Date?, now: Date = .now) -> String {
        guard let d else { return "" }
        let s = now.timeIntervalSince(d)
        switch s {
        case ..<5: return "now"
        case ..<60: return "\(Int(s)) s ago"
        case ..<3600: return "\(Int(s / 60)) min ago"
        case ..<86400: return "\(Int(s / 3600)) h ago"
        default:
            if Calendar.current.isDateInYesterday(d) { return "yesterday" }
            return d.formatted(.dateTime.month(.abbreviated).day())
        }
    }

    /// Compact form for tight columns: "38 m", "1 h", "2 d".
    static func agoShort(_ d: Date?, now: Date = .now) -> String {
        guard let d else { return "" }
        let s = now.timeIntervalSince(d)
        switch s {
        case ..<60: return "now"
        case ..<3600: return "\(Int(s / 60)) m"
        case ..<86400: return "\(Int(s / 3600)) h"
        default: return "\(Int(s / 86400)) d"
        }
    }

    private static let hms: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "HH:mm:ss"
        return f
    }()

    /// 24-hour wall clock, as the timeline and activity columns show it.
    static func time(_ d: Date?) -> String {
        guard let d else { return "" }
        return hms.string(from: d)
    }

    static func osName(_ os: String) -> String {
        let o = os.lowercased()
        if o.hasPrefix("windows") { return "Windows" }
        if o.hasPrefix("darwin") || o.hasPrefix("mac") { return "macOS" }
        if o.hasPrefix("linux") { return "Linux" }
        return os
    }

    /// "windows-11" → "Windows 11"; "darwin-26.1" → "macOS 26.1"
    static func osLong(_ os: String) -> String {
        let name = osName(os)
        if let dash = os.firstIndex(of: "-") {
            return name + " " + os[os.index(after: dash)...]
        }
        return name
    }

    /// "NVIDIA GeForce RTX 3050 Laptop GPU" → "RTX 3050"
    static func gpuShort(_ name: String) -> String {
        var n = name.replacingOccurrences(of: "NVIDIA ", with: "").replacingOccurrences(of: "GeForce ", with: "")
        n = n.replacingOccurrences(of: " Laptop GPU", with: "")
        return n
    }

    static func sha(_ s: String) -> String {
        s.count > 12 ? "\(s.prefix(6))…\(s.suffix(4))" : s
    }

    static func argv(_ a: [String]) -> String {
        a.map { $0.contains(" ") ? "\"\($0)\"" : $0 }.joined(separator: " ")
    }

    /// "347X0F" → "347 X0F"
    static func pairCode(_ c: String) -> String {
        let s = c.replacingOccurrences(of: " ", with: "").replacingOccurrences(of: "-", with: "")
        guard s.count == 6 else { return c }
        return "\(s.prefix(3)) \(s.suffix(3))"
    }
}
