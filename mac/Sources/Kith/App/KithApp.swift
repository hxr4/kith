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

/// Steps the menu bar icon's moving dot while a task is live (8 fps).
@MainActor @Observable
final class MenuIconAnimator {
    private(set) var tick = 0
    @ObservationIgnored private var timer: Timer?

    func frame(live: Bool) -> Int? {
        if live && timer == nil {
            timer = Timer.scheduledTimer(withTimeInterval: 0.125, repeats: true) { [weak self] _ in
                MainActor.assumeIsolated { self?.tick += 1 }
            }
        } else if !live, let t = timer {
            t.invalidate()
            timer = nil
        }
        return live ? tick % MenuIcon.frames : nil
    }
}

/// The menu bar mark (small cut). While a task runs the dot glides from one
/// half to the other and back; with the daemon off the halves dim and the dot
/// goes; an orange badge, cut out of the mark, flags a waiting approval.
enum MenuIcon {
    static let frames = 16

    static func image(frame: Int?, badge: Bool, off: Bool) -> NSImage {
        let size = NSSize(width: 20, height: 16)
        let img = NSImage(size: size, flipped: true) { rect in
            guard let cg = NSGraphicsContext.current?.cgContext else { return false }
            let travel = frame.map { sin(CGFloat($0) / CGFloat(frames) * 2 * .pi) } ?? 0
            let m = MarkGeometry.small.paths(in: rect.insetBy(dx: 0.25, dy: 0.25), travel: travel)
            // A template's colour is ignored (only alpha counts); with the
            // coloured badge the image isn't a template, so use the label colour.
            let ink = badge ? NSColor.labelColor : NSColor.black
            cg.setFillColor(ink.withAlphaComponent(off ? 0.4 : 1).cgColor)
            cg.addPath(m.halves.cgPath)
            if !off { cg.addPath(m.dot.cgPath) }
            cg.fillPath()
            if badge {
                let b = CGRect(x: size.width - 6, y: 0, width: 6, height: 6)
                cg.setBlendMode(.clear)
                cg.fillEllipse(in: b.insetBy(dx: -1.25, dy: -1.25))
                cg.setBlendMode(.normal)
                cg.setFillColor(NSColor.systemOrange.cgColor)
                cg.fillEllipse(in: b)
            }
            return true
        }
        img.isTemplate = !badge
        return img
    }
}
