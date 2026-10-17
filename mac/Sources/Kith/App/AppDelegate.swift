import AppKit
import SwiftUI

/// Kith lives in the menu bar (LSUIElement). The main window is an AppKit
/// window hosting SwiftUI so any part of the app (notifications, the
/// popover, the Dock) can bring it forward; while it is open Kith shows in
/// the Dock and the app switcher.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    static var shared: AppDelegate?
    private var window: NSWindow?

    func applicationWillFinishLaunching(_ notification: Notification) {
        AppDelegate.shared = self
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        Notifier.shared.setup()
        KithStore.shared.start()
        if !launchedAtLogin && !CommandLine.arguments.contains("--hidden") {
            showMain()
        }
    }

    /// Login items open quietly in the menu bar.
    private var launchedAtLogin: Bool {
        guard let ev = NSAppleEventManager.shared().currentAppleEvent else { return false }
        return ev.eventID == kAEOpenApplication &&
            ev.paramDescriptor(forKeyword: keyAEPropData)?.enumCodeValue == keyAELaunchedAsLogInItem
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showMain()
        return true
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    /// Dropping a task file on the Dock icon opens New task.
    func application(_ application: NSApplication, open urls: [URL]) {
        if let u = urls.first(where: { $0.pathExtension == "toml" || $0.hasDirectoryPath }) {
            KithStore.shared.newTask(TaskFile.resolve(u))
        }
    }

    func showMain() {
        if window == nil {
            let host = NSHostingController(rootView: MainWindow().environment(KithStore.shared))
            host.sizingOptions = []
            let w = NSWindow(contentViewController: host)
            w.title = "Kith"
            w.styleMask = [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView]
            w.titlebarAppearsTransparent = true
            w.titleVisibility = .hidden
            w.isReleasedWhenClosed = false
            w.minSize = NSSize(width: 900, height: 580)
            w.delegate = self
            w.setContentSize(NSSize(width: 1120, height: 740))
            if !w.setFrameUsingName("KithMain") { w.center() }
            w.setFrameAutosaveName("KithMain")
            window = w
        }
        NSApp.setActivationPolicy(.regular)
        NSApp.activate()
        window?.makeKeyAndOrderFront(nil)
        // Don't start with the cursor in the search field.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.05) { [weak self] in self?.window?.makeFirstResponder(nil) }
        KithStore.shared.interactive = true
    }

    func windowWillClose(_ notification: Notification) {
        KithStore.shared.interactive = false
        NSApp.setActivationPolicy(.accessory)
    }
}

/// Task files: a kith.toml / task.toml, or a folder that contains one.
enum TaskFile {
    static func resolve(_ url: URL) -> URL? {
        var isDir: ObjCBool = false
        guard FileManager.default.fileExists(atPath: url.path, isDirectory: &isDir) else { return nil }
        if !isDir.boolValue { return url.pathExtension == "toml" ? url : nil }
        for name in ["kith.toml", "task.toml"] {
            let u = url.appendingPathComponent(name)
            if FileManager.default.fileExists(atPath: u.path) { return u }
        }
        let tomls = (try? FileManager.default.contentsOfDirectory(at: url, includingPropertiesForKeys: nil))?
            .filter { $0.pathExtension == "toml" } ?? []
        return tomls.count == 1 ? tomls[0] : nil
    }
}
