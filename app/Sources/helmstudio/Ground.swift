import AppKit

/// The launcher's page ground, as helm-css defines it: #0d0c0a dark,
/// #faf8f3 light (packages/helm-css/helm.css, --helm-ground-page).
///
/// The shell needs this before the launcher has painted. A WKWebView is white
/// until its first frame, and a window is the system's grey until something
/// fills it, so without this the app opens white, flashes to the launcher's
/// near-black, and looks broken on the one screen everyone sees first.
///
/// The OS preference is used rather than the stored theme, because the stored
/// one arrives with the first request — which is the thing being waited for.
/// index.html makes the same trade for the same reason.
enum Ground {
    static let dark = NSColor(srgbRed: 0x0d/255, green: 0x0c/255, blue: 0x0a/255, alpha: 1)
    static let light = NSColor(srgbRed: 0xfa/255, green: 0xf8/255, blue: 0xf3/255, alpha: 1)

    static var color: NSColor {
        let match = NSApp.effectiveAppearance.bestMatch(from: [.darkAqua, .aqua])
        return match == .darkAqua ? dark : light
    }
}
