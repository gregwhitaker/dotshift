import AppKit

enum MenuBarIcon {
    static let size = NSSize(width: 20, height: 20)
    private static let centers: [(CGFloat, CGFloat)] = [(13, 31), (33, 31), (13, 51), (33, 51), (49, 15)]
    static let offsetRect = dotRect(x: 49, y: 15)

    private static func dotRect(x: CGFloat, y: CGFloat) -> NSRect {
        // Preserve the Offset mark's geometry, converting SVG to AppKit coordinates.
        NSRect(x: 1 + (x - 9) * 0.28, y: 1 + (64 - y - 9) * 0.28, width: 5.04, height: 5.04)
    }

    static func image(attention: Bool) -> NSImage {
        let image = NSImage(size: size, flipped: false) { _ in
            NSColor.black.setFill()
            for (x, y) in centers.prefix(attention ? 4 : 5) {
                NSBezierPath(ovalIn: dotRect(x: x, y: y)).fill()
            }
            return true
        }
        // Keep the monochrome dots native in normal, dark, and selected menu bars.
        // AttentionDotView supplies the fifth dot separately so template tinting
        // doesn't strip its brand color.
        image.isTemplate = true
        return image
    }

    static func attentionColor(for appearance: NSAppearance) -> NSColor {
        if appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua {
            return NSColor(srgbRed: 124.0 / 255, green: 152.0 / 255, blue: 1, alpha: 1) // #7C98FF
        }
        return NSColor(srgbRed: 47.0 / 255, green: 84.0 / 255, blue: 235.0 / 255, alpha: 1) // #2F54EB
    }
}

final class AttentionDotView: NSView {
    override var isFlipped: Bool { false }

    // All clicks, including directly on the colored dot, belong to the menu button.
    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        needsDisplay = true
    }

    override func draw(_ dirtyRect: NSRect) {
        guard let button = superview as? NSButton, let cell = button.cell else { return }
        let imageRect = convert(cell.imageRect(forBounds: button.bounds), from: button)
        let dot = MenuBarIcon.offsetRect
        let scaleX = imageRect.width / MenuBarIcon.size.width
        let scaleY = imageRect.height / MenuBarIcon.size.height
        let rect = NSRect(x: imageRect.minX + dot.minX * scaleX,
                          y: imageRect.minY + dot.minY * scaleY,
                          width: dot.width * scaleX, height: dot.height * scaleY)
        MenuBarIcon.attentionColor(for: effectiveAppearance).setFill()
        NSBezierPath(ovalIn: rect).fill()
    }
}
