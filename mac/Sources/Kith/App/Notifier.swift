import Foundation
import UserNotifications

/// Native notifications. Approval requests are actionable (Approve / Deny);
/// finished tasks offer Show; peer changes are reported only after 30 s of
/// stability (KithStore.notices).
@MainActor
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    static let shared = Notifier()

    /// UNUserNotificationCenter needs a real app bundle (not `swift run`).
    private var available: Bool { Bundle.main.bundleURL.pathExtension == "app" }

    func setup() {
        guard available else { return }
        let c = UNUserNotificationCenter.current()
        c.delegate = self
        let approve = UNNotificationAction(identifier: "approve", title: "Approve", options: [])
        let deny = UNNotificationAction(identifier: "deny", title: "Deny", options: [.destructive])
        let show = UNNotificationAction(identifier: "show", title: "Show", options: [.foreground])
        c.setNotificationCategories([
            UNNotificationCategory(identifier: "approval", actions: [deny, approve], intentIdentifiers: []),
            UNNotificationCategory(identifier: "task", actions: [show], intentIdentifiers: []),
        ])
        c.requestAuthorization(options: [.alert, .sound, .badge]) { _, _ in }
    }

    func approval(_ t: KTask) {
        Task {
            let full: KTask? = try? await IPC.call("task", ["id": t.id])
            let argv = full?.argv.map(Fmt.argv) ?? t.name
            post(id: "approval-\(t.id)", title: "\(t.peerLabel) wants to run \(t.name)",
                 body: "\(argv). Runs as you, without a sandbox.", category: "approval", task: t.id, sound: true)
        }
    }

    func finished(_ t: KTask) {
        let peer = t.peerLabel
        let (title, body): (String, String) = switch t.state {
        case TaskState.completed: ("\(t.name) completed on \(peer)", t.detail ?? "Finished.")
        case TaskState.failed: ("\(t.name) failed on \(peer)", [t.code, t.detail].compactMap { $0 }.joined(separator: " · "))
        case TaskState.rejected: ("\(peer) rejected \(t.name)", [t.code, t.detail].compactMap { $0 }.joined(separator: " · "))
        default: ("\(t.name) was cancelled", t.detail ?? "")
        }
        post(id: "done-\(t.id)", title: title, body: body, category: "task", task: t.id, sound: false)
    }

    func peer(_ p: Peer, online: Bool) {
        let addr = p.lastAddr.map { " at \($0.split(separator: ":").first ?? "")" } ?? ""
        post(id: "peer-\(p.fp)-\(Date().timeIntervalSince1970)",
             title: online ? "\(p.name) is back online" : "\(p.name) went offline",
             body: online ? "Reachable for 30 s\(addr)." : "Unreachable for 30 s. Running tasks resume when it returns.",
             category: nil, task: nil, sound: false)
    }

    private func post(id: String, title: String, body: String, category: String?, task: String?, sound: Bool) {
        guard available else { return }
        let c = UNMutableNotificationContent()
        c.title = title
        c.body = body
        if let category { c.categoryIdentifier = category }
        if let task { c.userInfo = ["task": task] }
        if sound { c.sound = .default }
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: id, content: c, trigger: nil))
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        [.banner, .sound, .list]
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        let action = response.actionIdentifier
        guard let id = response.notification.request.content.userInfo["task"] as? String else { return }
        await MainActor.run {
            let store = KithStore.shared
            Task {
                switch action {
                case "approve", "deny":
                    guard let t: KTask = try? await IPC.call("task", ["id": id]) else { return }
                    if action == "approve" { await store.approve(t) } else { await store.deny(t) }
                default:
                    if let t = store.task(id), t.needsApproval { store.open(.approvals) } else { store.openTask(id) }
                }
            }
        }
    }
}
