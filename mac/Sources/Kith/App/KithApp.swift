import SwiftUI
import AppKit

@main
struct KithApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @State private var store = KithStore.shared
    @State private var icon = MenuIconAnimator()

    var body: some Scene {
        MenuBarExtra {
            PopoverView()
                .environment(store)
        } label: {
            Image(nsImage: MenuIcon.image(frame: icon.frame(live: !store.active.filter { TaskState.isLive($0.state) }.isEmpty),
                                          badge: !store.pending.isEmpty, off: store.loaded && !store.running))
        }
        .menuBarExtraStyle(.window)

        Settings {
            SettingsView()
                .environment(store)
        }
        .commands {
            CommandGroup(replacing: .newItem) {
                Button("New Task…") { store.newTask() }.keyboardShortcut("n")
                Button("Open Kith") { AppDelegate.shared?.showMain() }.keyboardShortcut("o")
                Divider()
                Button("Pair a Machine…") { store.pair() }
            }
            CommandMenu("Go") {
                ForEach(Array(Screen.allCases.enumerated()), id: \.element) { i, s in
                    Button(s.title) { store.open(s) }.keyboardShortcut(KeyEquivalent(Character("\(i + 1)")))
                }
            }
        }
    }
}

/// Steps the menu bar icon's moving dot while a task is live (~4 fps).
@MainActor @Observable
final class MenuIconAnimator {
    private(set) var tick = 0
    @ObservationIgnored private var timer: Timer?

    func frame(live: Bool) -> Int? {
        if live && timer == nil {
            timer = Timer.scheduledTimer(withTimeInterval: 0.25, repeats: true) { [weak self] _ in
                MainActor.assumeIsolated { self?.tick += 1 }
            }
        } else if !live, let t = timer {
            t.invalidate()
            timer = nil
        }
        return live ? tick % 6 : nil
    }
}

/// The menu bar mark: two linked nodes; a dot travels along the link while a
/// task runs, and an orange badge marks a waiting approval.
enum MenuIcon {
    static func image(frame: Int?, badge: Bool, off: Bool) -> NSImage {
        let size = NSSize(width: 24, height: 16)
        let img = NSImage(size: size, flipped: false) { _ in
            let ink = NSColor.labelColor.withAlphaComponent(off ? 0.45 : 1)
            ink.setStroke()
            ink.setFill()
            let d: CGFloat = 7.5, y = (size.height - d) / 2, lw: CGFloat = 1.5
            let l = NSBezierPath(ovalIn: NSRect(x: 2 + lw / 2, y: y + lw / 2, width: d - lw, height: d - lw))
            let r = NSBezierPath(ovalIn: NSRect(x: 2 + 20 - d + lw / 2, y: y + lw / 2, width: d - lw, height: d - lw))
            l.lineWidth = lw; r.lineWidth = lw
            l.stroke(); r.stroke()
            let link = NSBezierPath()
            link.move(to: NSPoint(x: 2 + d - 0.5, y: size.height / 2))
            link.line(to: NSPoint(x: 2 + 20 - d + 0.5, y: size.height / 2))
            link.lineWidth = off ? 1 : 1.5
            if off { link.setLineDash([2, 2], count: 2, phase: 0) }
            link.stroke()
            if let f = frame {
                let x = 2 + d - 1 + CGFloat(f) * (20 - 2 * d + 2) / 5
                NSBezierPath(ovalIn: NSRect(x: x - 1.6, y: size.height / 2 - 1.6, width: 3.2, height: 3.2)).fill()
            }
            if badge {
                NSColor.systemOrange.setFill()
                NSBezierPath(ovalIn: NSRect(x: size.width - 6, y: size.height - 6, width: 6, height: 6)).fill()
            }
            return true
        }
        img.isTemplate = !badge
        return img
    }
}
