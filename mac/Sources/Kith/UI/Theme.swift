import SwiftUI
import AppKit

/// Design tokens from the "Quiet instrument" design (kith-shared.js), each
/// with its light and dark value.
enum K {
    static let win = dyn(0xECECEE, 0x1E1E20)
    static let side = dyn(0xEEEEF0, 0x252527)
    static let bg = dyn(0xFFFFFF, 0x1C1C1E)
    static let bg2 = dyn(0xF6F6F7, 0x232325)
    static let card = dyn(0xFFFFFF, 0x2A2A2C)
    static let l1 = dynA((0, 0, 0, 0.86), (255, 255, 255, 0.88))
    static let l2 = dynA((0, 0, 0, 0.56), (255, 255, 255, 0.56))
    static let l3 = dynA((0, 0, 0, 0.34), (255, 255, 255, 0.32))
    static let sep = dynA((0, 0, 0, 0.10), (255, 255, 255, 0.10))
    static let fill = dynA((0, 0, 0, 0.045), (255, 255, 255, 0.05))
    static let fill2 = dynA((0, 0, 0, 0.08), (255, 255, 255, 0.09))
    static let acc = dyn(0x0F8C8C, 0x1E9E9E)
    static let accText = dyn(0x0B7272, 0x5CCFCF)
    static let accSoft = dynA((15, 140, 140, 0.12), (34, 163, 163, 0.20))
    static let green = dyn(0x1F8A3B, 0x32D74B)
    static let greenDot = dyn(0x34C759, 0x30D158)
    static let greenSoft = dynA((52, 199, 89, 0.15), (50, 215, 75, 0.15))
    static let orange = dyn(0xB35000, 0xFFB340)
    static let orangeDot = dyn(0xFF9500, 0xFF9F0A)
    static let orangeSoft = dynA((255, 149, 0, 0.15), (255, 159, 10, 0.16))
    static let red = dyn(0xC8102E, 0xFF6B61)
    static let redDot = dyn(0xFF3B30, 0xFF453A)
    static let redSoft = dynA((255, 59, 48, 0.10), (255, 69, 58, 0.15))
    static let con = dyn(0xFAFAFA, 0x161617)
    static let btn = dyn(0xFFFFFF, 0x3A3A3C)
    static let btnEdge = dynA((0, 0, 0, 0.16), (255, 255, 255, 0.10))

    static func mono(_ size: CGFloat, _ weight: Font.Weight = .regular) -> Font {
        .system(size: size, weight: weight, design: .monospaced)
    }
    static func ui(_ size: CGFloat, _ weight: Font.Weight = .regular) -> Font {
        .system(size: size, weight: weight)
    }

    // Motion (the design's motion spec).
    static let layout = Animation.spring(response: 0.35, dampingFraction: 0.85)
    static let snappy = Animation.snappy(duration: 0.25)
    static let smooth = Animation.smooth(duration: 0.35)
    static let pill = Animation.spring(response: 0.3, dampingFraction: 0.7)

    private static func dyn(_ light: UInt32, _ dark: UInt32) -> Color {
        Color(nsColor: NSColor(name: nil) { ap in
            let hex = ap.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua ? dark : light
            return NSColor(srgbRed: CGFloat(hex >> 16 & 0xFF) / 255, green: CGFloat(hex >> 8 & 0xFF) / 255,
                           blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
        })
    }

    private static func dynA(_ light: (Int, Int, Int, Double), _ dark: (Int, Int, Int, Double)) -> Color {
        Color(nsColor: NSColor(name: nil) { ap in
            let c = ap.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua ? dark : light
            return NSColor(srgbRed: CGFloat(c.0) / 255, green: CGFloat(c.1) / 255, blue: CGFloat(c.2) / 255, alpha: c.3)
        })
    }
}

// MARK: - Buttons

enum KButtonKind { case primary, secondary, destructive, destructiveFilled, disabled }

/// Push buttons from the component sheet. Pressed: scale .97, answered in 80 ms.
struct KButtonStyle: ButtonStyle {
    var kind: KButtonKind = .secondary
    var height: CGFloat = 28
    var fullWidth = false
    @Environment(\.isEnabled) private var enabled

    func makeBody(configuration: Configuration) -> some View {
        let k = enabled ? kind : .disabled
        configuration.label
            .font(K.ui(height < 26 ? 12 : 13, .medium))
            .lineLimit(1)
            .padding(.horizontal, height < 26 ? 10 : 14)
            .frame(height: height)
            .frame(maxWidth: fullWidth ? .infinity : nil)
            .foregroundStyle(fg(k))
            .background(RoundedRectangle(cornerRadius: height < 26 ? 6 : 7, style: .continuous).fill(bg(k)))
            .overlay {
                if k == .secondary || k == .destructive {
                    RoundedRectangle(cornerRadius: height < 26 ? 6 : 7, style: .continuous).strokeBorder(K.btnEdge, lineWidth: 0.5)
                }
            }
            .shadow(color: .black.opacity(k == .secondary || k == .destructive ? 0.06 : 0), radius: 0.75, y: 0.5)
            .contentShape(Rectangle())
            .scaleEffect(configuration.isPressed ? 0.97 : 1)
            .animation(.easeOut(duration: 0.08), value: configuration.isPressed)
    }

    private func fg(_ k: KButtonKind) -> Color {
        switch k {
        case .primary, .destructiveFilled: .white
        case .secondary: K.l1
        case .destructive: K.red
        case .disabled: K.l3
        }
    }

    private func bg(_ k: KButtonKind) -> Color {
        switch k {
        case .primary: K.acc
        case .destructiveFilled: K.redDot
        case .secondary, .destructive: K.btn
        case .disabled: K.fill2
        }
    }
}

extension ButtonStyle where Self == KButtonStyle {
    static var kPrimary: KButtonStyle { KButtonStyle(kind: .primary) }
    static var kSecondary: KButtonStyle { KButtonStyle(kind: .secondary) }
    static var kDestructive: KButtonStyle { KButtonStyle(kind: .destructive) }
    static func k(_ kind: KButtonKind, height: CGFloat = 28, fullWidth: Bool = false) -> KButtonStyle {
        KButtonStyle(kind: kind, height: height, fullWidth: fullWidth)
    }
}

/// Borderless icon button with a hover fill (log toolbar, copy glyphs).
struct IconButtonStyle: ButtonStyle {
    var on = false
    @State private var hover = false

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 12))
            .foregroundStyle(on ? K.accText : K.l2)
            .frame(width: 26, height: 22)
            .background(RoundedRectangle(cornerRadius: 6).fill(on ? K.accSoft : hover ? K.fill : .clear))
            .contentShape(Rectangle())
            .onHover { h in withAnimation(.easeOut(duration: 0.08)) { hover = h } }
            .scaleEffect(configuration.isPressed ? 0.94 : 1)
    }
}

// MARK: - Surfaces

struct CardBackground: ViewModifier {
    var radius: CGFloat = 12
    var fill: Color = K.card
    var shadow = false

    func body(content: Content) -> some View {
        content
            .background(RoundedRectangle(cornerRadius: radius, style: .continuous).fill(fill))
            .overlay(RoundedRectangle(cornerRadius: radius, style: .continuous).strokeBorder(K.sep, lineWidth: 0.5))
            .clipShape(RoundedRectangle(cornerRadius: radius, style: .continuous))
            .shadow(color: .black.opacity(shadow ? 0.06 : 0), radius: 1.5, y: 1)
    }
}

extension View {
    func card(radius: CGFloat = 12, fill: Color = K.card, shadow: Bool = false) -> some View {
        modifier(CardBackground(radius: radius, fill: fill, shadow: shadow))
    }

    /// Row hover fill (80 ms).
    func hoverFill(radius: CGFloat = 7, selected: Bool = false) -> some View {
        modifier(HoverFill(radius: radius, selected: selected))
    }

    func sectionLabel() -> some View {
        font(K.ui(11, .semibold)).foregroundStyle(K.l2)
    }
}

struct HoverFill: ViewModifier {
    var radius: CGFloat
    var selected: Bool
    @State private var hover = false

    func body(content: Content) -> some View {
        content
            .background(RoundedRectangle(cornerRadius: radius, style: .continuous).fill(selected ? K.fill2 : hover ? K.fill : .clear))
            .contentShape(Rectangle())
            .onHover { h in withAnimation(.easeOut(duration: 0.08)) { hover = h } }
    }
}

struct Divider05: View {
    var body: some View { Rectangle().fill(K.sep).frame(height: 0.5) }
}
