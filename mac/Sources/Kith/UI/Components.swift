import SwiftUI

// MARK: - State chip

/// The 11 task states. In progress: a leading dot (live states pulse).
/// Ended: a leading glyph, static, settling with a 1.04 pop.
struct StateChip: View {
    var state: String
    var big = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var pop = false

    private struct Style { var fg: Color; var bg: Color = .clear; var border: Color? = nil; var ring = false; var live = false; var icon: String? = nil }

    private var style: Style {
        switch state {
        case TaskState.submitted: Style(fg: K.l2, border: K.l3, ring: true)
        case TaskState.accepted: Style(fg: K.accText, border: K.acc, ring: true)
        case TaskState.transferring: Style(fg: K.accText, bg: K.accSoft, live: true)
        case TaskState.running: Style(fg: .white, bg: K.acc, live: true)
        case TaskState.finalizing: Style(fg: K.accText, bg: K.accSoft)
        case TaskState.cancelling: Style(fg: K.orange, bg: K.orangeSoft, live: true)
        case TaskState.interrupted: Style(fg: K.orange, border: K.orange)
        case TaskState.completed: Style(fg: K.green, bg: K.greenSoft, icon: "checkmark")
        case TaskState.failed: Style(fg: K.red, bg: K.redSoft, icon: "xmark")
        case TaskState.cancelled: Style(fg: K.l2, bg: K.fill2, icon: "minus")
        case TaskState.rejected: Style(fg: K.red, border: K.red, icon: "nosign")
        default: Style(fg: K.l2, border: K.l3, ring: true)
        }
    }

    var body: some View {
        let s = style
        HStack(spacing: 5) {
            if let icon = s.icon {
                Image(systemName: icon).font(.system(size: big ? 9.5 : 8, weight: .bold))
                    .transition(.scale.combined(with: .opacity))
            } else {
                LiveDot(color: s.fg, ring: s.ring, live: s.live && !reduceMotion)
                    .transition(.scale.combined(with: .opacity))
            }
            Text(state).contentTransition(.interpolate)
        }
        .font(K.mono(big ? 11 : 10, .semibold))
        .tracking(0.4)
        .foregroundStyle(s.fg)
        .padding(.leading, big ? 8 : 6)
        .padding(.trailing, big ? 9 : 7)
        .frame(height: big ? 22 : 18)
        .background(RoundedRectangle(cornerRadius: 5, style: .continuous).fill(s.bg))
        .overlay { if let b = s.border { RoundedRectangle(cornerRadius: 5, style: .continuous).strokeBorder(b, lineWidth: 1) } }
        .fixedSize()
        .scaleEffect(pop ? 1.04 : 1)
        .animation(K.layout, value: state)
        .onChange(of: state) { _, new in
            guard TaskState.isEnded(new), !reduceMotion else { return }
            withAnimation(.snappy(duration: 0.12)) { pop = true } completion: {
                withAnimation(.spring(response: 0.3, dampingFraction: 0.7)) { pop = false }
            }
        }
    }
}

struct LiveDot: View {
    var color: Color
    var ring = false
    var live = false
    var size: CGFloat = 6
    @State private var on = false

    var body: some View {
        Group {
            if ring { Circle().strokeBorder(color, lineWidth: 1.5) } else { Circle().fill(color) }
        }
        .frame(width: size, height: size)
        .opacity(live && on ? 0.35 : 1)
        .scaleEffect(live && on ? 0.7 : 1)
        .onAppear { if live { withAnimation(.easeInOut(duration: 0.8).repeatForever(autoreverses: true)) { on = true } } }
        .onChange(of: live) { _, l in
            if l { withAnimation(.easeInOut(duration: 0.8).repeatForever(autoreverses: true)) { on = true } } else { on = false }
        }
    }
}

struct StatusDot: View {
    var on: Bool
    var size: CGFloat = 7
    var body: some View {
        Group {
            if on { Circle().fill(K.greenDot) } else { Circle().strokeBorder(K.l3, lineWidth: 1.5) }
        }
        .frame(width: size, height: size)
    }
}

// MARK: - The link

enum LinkMode: Equatable { case off, idle, flow, flowBack }

/// The signature element: two machines joined by a line. Solid when
/// reachable, dashed when offline; pulses travel along it in the direction
/// of data while files transfer (Mac → peer) or logs stream (peer → Mac).
struct LinkLine: View {
    var mode: LinkMode
    var lineWidth: CGFloat = 1.5
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let flowing = (mode == .flow || mode == .flowBack) && !reduceMotion
        TimelineView(.animation(paused: !flowing)) { tl in
            Canvas { ctx, size in
                let y = size.height / 2
                var p = Path()
                p.move(to: CGPoint(x: 0, y: y))
                p.addLine(to: CGPoint(x: size.width, y: y))
                if mode == .off {
                    ctx.stroke(p, with: .color(K.l3), style: StrokeStyle(lineWidth: lineWidth, dash: [3, 3]))
                    return
                }
                ctx.stroke(p, with: .color(K.acc), style: StrokeStyle(lineWidth: lineWidth, lineCap: .round))
                guard flowing else { return }
                let t = tl.date.timeIntervalSinceReferenceDate
                for i in 0..<3 {
                    var f = ((t + Double(i) * 0.53) / 1.6).truncatingRemainder(dividingBy: 1)
                    f = f < 0.5 ? 4 * f * f * f : 1 - pow(-2 * f + 2, 3) / 2 // easeInOut
                    if mode == .flowBack { f = 1 - f }
                    let x = 2.5 + (size.width - 5) * f
                    let edge = min(f, 1 - f)
                    let alpha = min(1, edge / 0.14)
                    let r = CGRect(x: x - 2.5, y: y - 2.5, width: 5, height: 5)
                    ctx.fill(Path(ellipseIn: r.insetBy(dx: -2, dy: -2)), with: .color((scheme == .dark ? K.bg : K.bg).opacity(alpha)))
                    ctx.fill(Path(ellipseIn: r), with: .color(K.acc.opacity(alpha)))
                }
            }
        }
        .frame(height: 10)
        .animation(K.smooth, value: mode)
        .accessibilityLabel(mode == .off ? "offline" : mode == .idle ? "connected" : "transferring")
    }
}

// MARK: - Countdown ring

struct CountdownRing: View {
    var fraction: Double
    var warn: Bool
    var size: CGFloat = 20

    var body: some View {
        ZStack {
            Circle().stroke(K.fill2, lineWidth: size * 0.2)
            Circle().trim(from: 0, to: max(0, min(1, fraction)))
                .stroke(warn ? K.orangeDot : K.acc, style: StrokeStyle(lineWidth: size * 0.2, lineCap: .butt))
                .rotationEffect(.degrees(-90))
        }
        .frame(width: size * 0.8, height: size * 0.8)
        .frame(width: size, height: size)
        .animation(.smooth(duration: 0.3), value: warn)
    }
}

/// The approval timeout (daemon ApprovalTTL, 15 min from creation).
struct ApprovalCountdown: View {
    var created: Date?
    var size: CGFloat = 20
    var withLabel = true
    static let ttl: TimeInterval = 15 * 60

    var body: some View {
        TimelineView(.periodic(from: .now, by: 1)) { tl in
            let left = max(0, Self.ttl - tl.date.timeIntervalSince(created ?? tl.date))
            HStack(spacing: 5) {
                if withLabel {
                    Text(Fmt.clock(left)).font(K.mono(11)).foregroundStyle(left <= 60 ? K.orange : K.l2)
                        .contentTransition(.numericText(countsDown: true))
                        .animation(.snappy, value: Int(left))
                }
                CountdownRing(fraction: left / Self.ttl, warn: left <= 60, size: size)
            }
        }
    }
}

// MARK: - Segmented

/// The design's segmented control; the thumb slides between options.
struct Segmented<T: Hashable>: View {
    var options: [(T, String)]
    @Binding var selection: T
    var mono = false
    var counts: [T: Int] = [:]
    @Namespace private var ns

    var body: some View {
        HStack(spacing: 2) {
            ForEach(options, id: \.0) { value, label in
                let on = value == selection
                HStack(spacing: 4) {
                    Text(label).font(mono ? K.mono(11) : K.ui(12, .medium))
                    if let c = counts[value] { Text("\(c)").font(K.ui(12)).foregroundStyle(K.l3).contentTransition(.numericText()) }
                }
                .foregroundStyle(on ? K.l1 : K.l2)
                .padding(.horizontal, 10)
                .frame(height: 22)
                .background {
                    if on {
                        RoundedRectangle(cornerRadius: 6, style: .continuous).fill(K.btn)
                            .shadow(color: .black.opacity(0.12), radius: 0.5, y: 0.5)
                            .matchedGeometryEffect(id: "thumb", in: ns)
                    }
                }
                .contentShape(Rectangle())
                .onTapGesture { withAnimation(K.snappy) { selection = value } }
            }
        }
        .padding(2)
        .background(RoundedRectangle(cornerRadius: 8, style: .continuous).fill(K.fill2))
    }
}

// MARK: - Device glyph

struct DeviceGlyph: View {
    var symbol: String
    var size: CGFloat = 48
    var dim = false

    var body: some View {
        Image(systemName: symbol)
            .font(.system(size: size * 0.46, weight: .regular))
            .symbolRenderingMode(.hierarchical)
            .foregroundStyle(dim ? K.l3 : K.l1)
            .frame(width: size, height: size)
            .background(RoundedRectangle(cornerRadius: size * 0.25, style: .continuous).fill(dim ? .clear : K.bg2))
            .overlay(RoundedRectangle(cornerRadius: size * 0.25, style: .continuous)
                .strokeBorder(dim ? K.l3 : K.sep, lineWidth: dim ? 1 : 0.5))
    }
}

/// Kith's mark (branding/geometry.json): two offset half-rings, the two
/// machines, passing a dot, the task, between them. Units are a 256 box.
struct MarkGeometry {
    var dx, dy, r, w, rd, s: CGFloat
    static let main = MarkGeometry(dx: 34, dy: 20, r: 54, w: 36, rd: 20, s: 0.97)
    /// Heavier cut for 24 pt and below, where the gaps would close.
    static let small = MarkGeometry(dx: 40, dy: 16, r: 50, w: 44, rd: 21, s: 0.9)

    /// The mark's bounds in its 256 box (it is symmetric about the centre).
    var bounds: CGRect {
        let x0 = 128 - (dx + r + w / 2) * s, y0 = 128 - (dy + r + w / 2) * s
        return CGRect(x: x0, y: y0, width: 2 * (128 - x0), height: 2 * (128 - y0))
    }

    /// The halves and the dot fitted into `rect` (y down). `travel` slides the
    /// dot from the left half's centre (-1) to the right half's (1); the
    /// halves open toward each other, and the dot narrows by up to 30% on the
    /// way through so it keeps clear of both.
    func paths(in rect: CGRect, travel: CGFloat = 0) -> (halves: Path, dot: Path) {
        let b = bounds
        let k = min(rect.width / b.width, rect.height / b.height)
        let o = CGPoint(x: rect.midX - 128 * k, y: rect.midY - 128 * k)
        func pt(_ x: CGFloat, _ y: CGFloat) -> CGPoint { CGPoint(x: o.x + x * k, y: o.y + y * k) }
        let band = StrokeStyle(lineWidth: w * s * k, lineCap: .round)
        var halves = Path()
        for side: CGFloat in [-1, 1] {
            var arc = Path()
            // Angles grow from +x toward +y (down): 90°→270° is the left half.
            arc.addRelativeArc(center: pt(128 + side * dx * s, 128 + side * dy * s), radius: r * s * k,
                               startAngle: .degrees(side < 0 ? 90 : 270), delta: .degrees(180))
            halves.addPath(arc.strokedPath(band))
        }
        let squeeze = 1 - 0.3 * sin(.pi * min(1, abs(travel)))
        let c = pt(128 + travel * dx * s, 128 + travel * dy * s), d = rd * s * k * squeeze
        return (halves, Path(ellipseIn: CGRect(x: c.x - d, y: c.y - d, width: 2 * d, height: 2 * d)))
    }
}

/// The Kith mark. `dot` defaults to the halves' colour.
struct KithMark: View {
    var color: Color = .white
    var dot: Color? = nil
    var width: CGFloat = 22

    var body: some View {
        let g: MarkGeometry = width <= 24 ? .small : .main
        Canvas { ctx, size in
            let m = g.paths(in: CGRect(origin: .zero, size: size))
            ctx.fill(m.halves, with: .color(color))
            ctx.fill(m.dot, with: .color(dot ?? color))
        }
        .frame(width: width, height: width * g.bounds.height / g.bounds.width)
        .accessibilityHidden(true)
    }
}

// MARK: - Toast

struct ToastView: View {
    var toast: Toast
    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: toast.isError ? "exclamationmark.triangle.fill" : "checkmark")
                .font(.system(size: 11, weight: .bold))
                .foregroundStyle(toast.isError ? K.orange : K.green)
            Text(toast.text).font(K.ui(12, .medium)).lineLimit(2)
        }
        .padding(.leading, 10).padding(.trailing, 12)
        .frame(minHeight: 28)
        .background(Capsule().fill(.regularMaterial))
        .overlay(Capsule().strokeBorder(K.sep, lineWidth: 0.5))
        .shadow(color: .black.opacity(0.18), radius: 14, y: 6)
        .frame(maxWidth: 480)
    }
}

struct Toast: Equatable, Identifiable {
    var id = UUID()
    var text: String
    var isError = false
}

// MARK: - Small pieces

/// A value that copies itself (fingerprints, hashes) with a "Copied" toast.
struct CopyableMono: View {
    var text: String
    var shown: String? = nil
    var label: String
    var size: CGFloat = 11.5
    @State private var hover = false

    var body: some View {
        HStack(spacing: 5) {
            Text(shown ?? text).font(K.mono(size)).lineLimit(1).truncationMode(.middle).textSelection(.enabled)
            Image(systemName: "doc.on.doc").font(.system(size: 10)).foregroundStyle(hover ? K.acc : K.l3)
        }
        .contentShape(Rectangle())
        .onHover { hover = $0 }
        .onTapGesture { KithStore.shared.copy(text, label: label) }
        .help("Copy \(label)")
    }
}

struct KSpinner: View {
    var size: CGFloat = 12
    var body: some View { ProgressView().controlSize(.small).scaleEffect(size / 16).frame(width: size, height: size) }
}

/// Fixed-width key/value row used in manifests and info panes.
struct KVRow<V: View>: View {
    var key: String
    var keyWidth: CGFloat = 84
    @ViewBuilder var value: V
    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(key).font(K.ui(11.5)).foregroundStyle(K.l2).frame(width: keyWidth, alignment: .leading)
            value.frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.horizontal, 14).padding(.vertical, 6)
    }
}
