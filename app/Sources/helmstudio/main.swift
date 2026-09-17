import AppKit

// helmstudio.app is a window plus native chrome around the launcher the daemon
// already serves (docs/design/01-prd.md §12). Everything inside the window is
// the daemon's own UI, so a bug fixed in the browser path is fixed here too,
// and no feature is reachable only from the app (R73).
let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
