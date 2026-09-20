import AppKit
import WebKit

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private var window: NSWindow!
    private var webView: WKWebView!
    private var status: StatusView!
    private let daemon = Daemon()
    /// The origin the daemon serves. Set once the handshake is in; until then
    /// there is nothing the window is allowed to navigate to.
    private var origin: URL?
    /// Where each running download is being written, so the one that finishes
    /// can tell the Dock about the file it made.
    fileprivate var destinations: [ObjectIdentifier: URL] = [:]

    func applicationDidFinishLaunching(_ notification: Notification) {
        buildMenu()
        buildWindow()
        NSApp.activate(ignoringOtherApps: true)
        Task { await connect() }
    }

    // MARK: - the daemon

    private func connect() async {
        status.working("Starting helmstudio…")
        showStatus()
        do {
            let shake = try await daemon.resolve()
            guard let url = URL(string: shake.url) else {
                status.failed("The daemon reported an address this app cannot use: \(shake.url)",
                              log: daemon.log.tail)
                return
            }
            origin = url
            window.title = daemon.adopted ? "helmstudio — adopted" : "helmstudio"
            status.working("Opening the launcher…")
            webView.load(URLRequest(url: url))
        } catch {
            status.failed(error.localizedDescription, log: daemon.log.tail)
            showStatus()
        }
    }

    // MARK: - the window

    private func buildWindow() {
        let config = WKWebViewConfiguration()
        // A studio opens in a frame that carries allow="fullscreen", and
        // helmstudio asks on its behalf from a click (03 §7a, web/embed.js).
        // A WKWebView refuses that unless this is on, where Electron allowed
        // it by default.
        config.preferences.isElementFullscreenEnabled = true
        // Everything below is about the first second. A WKWebView is white
        // until its first frame lands, so without a ground of its own the app
        // opens white and snaps to the launcher's near-black.
        // Nothing is registered on this configuration: no script message
        // handler, no URL scheme handler. A web view with no bridge into the
        // app has nothing to leak through, which is the property M9's review
        // asks about — and it is why the daemon's cookie, not a header the
        // shell injects, is what authenticates the page.
        webView = WKWebView(frame: .zero, configuration: config)
        webView.navigationDelegate = self
        webView.uiDelegate = self
        // The Web Inspector, which Electron gave away in devtools. Without it
        // the launcher cannot be debugged inside the app at all.
        webView.isInspectable = true
        webView.underPageBackgroundColor = Ground.color
        webView.wantsLayer = true
        webView.alphaValue = 0
        webView.isHidden = true
        webView.translatesAutoresizingMaskIntoConstraints = false

        status = StatusView(frame: .zero)
        status.translatesAutoresizingMaskIntoConstraints = false
        status.onRetry = { [weak self] in Task { await self?.connect() } }

        let content = NSView()
        content.wantsLayer = true
        content.layer?.backgroundColor = Ground.color.cgColor
        for v in [webView, status] as [NSView] {
            content.addSubview(v)
            NSLayoutConstraint.activate([
                v.topAnchor.constraint(equalTo: content.topAnchor),
                v.bottomAnchor.constraint(equalTo: content.bottomAnchor),
                v.leadingAnchor.constraint(equalTo: content.leadingAnchor),
                v.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            ])
        }

        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1280, height: 860),
                          styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                          backing: .buffered, defer: false)
        window.title = "helmstudio"
        window.backgroundColor = Ground.color
        window.contentView = content
        window.minSize = NSSize(width: 960, height: 600)
        window.center()
        // 02 §11: the window's bounds are the app's own, kept in its user
        // data and never in helm.db.
        window.setFrameAutosaveName(Defaults.windowFrame)
        window.makeKeyAndOrderFront(nil)
    }

    private func showStatus() {
        status.isHidden = false
        status.alphaValue = 1
        webView.isHidden = true
        webView.alphaValue = 0
    }

    /// Reveal the launcher once it has actually painted, and cross-fade
    /// rather than cut: the two grounds are the same colour, so what a person
    /// sees is the spinner dissolving into the page instead of a swap.
    private func revealLauncher() {
        guard webView.alphaValue < 1 else { return }
        webView.isHidden = false
        NSAnimationContext.runAnimationGroup({ ctx in
            ctx.duration = 0.2
            webView.animator().alphaValue = 1
            status.animator().alphaValue = 0
        }, completionHandler: { [weak self] in
            self?.status.isHidden = true
        })
    }

    // MARK: - quitting

    /// R69: on quit the shell shuts the daemon down cleanly, or leaves it
    /// running when the user chose to keep studios alive.
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard daemon.shouldStopOnQuit else { return .terminateNow }
        status.working("Stopping studios…")
        showStatus()
        Task {
            await daemon.stop()
            NSApp.reply(toApplicationShouldTerminate: true)
        }
        return .terminateLater
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }

    // MARK: - the menu

    /// An app with no menu has no Cmd-Q, and a web view in an app with no Edit
    /// menu has no copy and paste: the responder chain is where those come
    /// from on macOS, not the web view.
    private func buildMenu() {
        let main = NSMenu()

        let appItem = NSMenuItem()
        let appMenu = NSMenu()
        appMenu.addItem(withTitle: "About helmstudio", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: "")
        appMenu.addItem(.separator())
        let keep = NSMenuItem(title: "Keep studios running after quit", action: #selector(toggleKeepRunning), keyEquivalent: "")
        keep.target = self
        keep.state = UserDefaults.standard.bool(forKey: Defaults.keepStudiosRunning) ? .on : .off
        appMenu.addItem(keep)
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "Hide helmstudio", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        appMenu.addItem(withTitle: "Quit helmstudio", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appItem.submenu = appMenu
        main.addItem(appItem)

        let editItem = NSMenuItem()
        let edit = NSMenu(title: "Edit")
        edit.addItem(withTitle: "Undo", action: Selector(("undo:")), keyEquivalent: "z")
        edit.addItem(withTitle: "Redo", action: Selector(("redo:")), keyEquivalent: "Z")
        edit.addItem(.separator())
        edit.addItem(withTitle: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        edit.addItem(withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        edit.addItem(withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        edit.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        editItem.submenu = edit
        main.addItem(editItem)

        let viewItem = NSMenuItem()
        let view = NSMenu(title: "View")
        let reload = NSMenuItem(title: "Reload", action: #selector(reloadLauncher), keyEquivalent: "r")
        reload.target = self
        view.addItem(reload)
        view.addItem(.separator())
        view.addItem(withTitle: "Enter Full Screen", action: #selector(NSWindow.toggleFullScreen(_:)), keyEquivalent: "f")
        viewItem.submenu = view
        main.addItem(viewItem)

        NSApp.mainMenu = main
    }

    @objc private func reloadLauncher() {
        guard let origin else { return }
        // Reload the origin rather than the current page: the launcher keeps
        // its place in the address, and reloading a page the frame navigated
        // to is not what a person means by reloading helmstudio.
        webView.load(URLRequest(url: origin))
    }

    @objc private func toggleKeepRunning(_ sender: NSMenuItem) {
        let on = !UserDefaults.standard.bool(forKey: Defaults.keepStudiosRunning)
        UserDefaults.standard.set(on, forKey: Defaults.keepStudiosRunning)
        sender.state = on ? .on : .off
    }
}

// Every completion handler here is `@MainActor`, or none of this is here.
//
// WKUIDelegate is an Objective-C protocol and WebKit finds these by selector.
// A method that does not satisfy the requirement is not exported at all, and
// an optional method WebKit cannot find means WebKit answers the page itself
// — by doing nothing. WebKit declares these handlers WK_SWIFT_UI_ACTOR, so
// the requirement is `@escaping @MainActor (…) -> Void`; written without it
// each one only "nearly matches" (the compiler says exactly that, in a
// warning), and the app silently answered no dialog at all: browse opened
// nothing, alert() returned in a millisecond, confirm() was false, and the
// studio inside the frame looked broken. `createWebViewWith` takes no
// handler, so it matched and Open in a tab went on working — which is what
// made this look like a file-picker bug rather than a delegate WebKit could
// not see.
extension AppDelegate: WKUIDelegate {
    /// "Open in a tab" (03 §7a) asks for a new window, and a WKWebView makes
    /// none unless it is told how. Without this the button did nothing at all
    /// in the app, which is worse than either outcome.
    ///
    /// It opens in the browser the person chose, which is what "a tab" means
    /// on a Mac, so the button keeps its promise rather than being hidden
    /// here and present in the browser. Returning nil says no web view was
    /// made, which is true — we handed it to somebody else.
    func webView(_ webView: WKWebView,
                 createWebViewWith configuration: WKWebViewConfiguration,
                 for navigationAction: WKNavigationAction,
                 windowFeatures: WKWindowFeatures) -> WKWebView? {
        if let url = navigationAction.request.url {
            NSWorkspace.shared.open(url)
        }
        return nil
    }

    /// A studio asking for the microphone — a speech studio recording its
    /// reference clip. Unanswered, WebKit denies it and the page is given no
    /// `navigator.mediaDevices` at all, so recording looks broken rather than
    /// refused. Granting here is not the real decision: macOS still asks for
    /// the microphone once, for helmstudio, and what is being granted is
    /// loopback-local code the person installed and started themselves.
    func webView(_ webView: WKWebView,
                 requestMediaCapturePermissionFor origin: WKSecurityOrigin,
                 initiatedByFrame frame: WKFrameInfo,
                 type: WKMediaCaptureType,
                 decisionHandler: @escaping @MainActor (WKPermissionDecision) -> Void) {
        decisionHandler(type == .microphone ? .grant : .deny)
    }

    // The dialogs a page expects to exist.
    //
    // A WKWebView answers none of these on its own, and the failure is silent
    // rather than loud: a file input opens nothing, confirm() returns false,
    // prompt() returns nil. A page written for a browser then looks broken in
    // ways it never reports — a Delete that does nothing, because the confirm
    // it asked for was answered "no" by an empty room. The browser at
    // 127.0.0.1:8700 has all of this for free, and R73 says no feature may be
    // the browser's alone.
    //
    // Each owes WebKit exactly one call to its completion handler. A sheet
    // dismissed without one leaves the page waiting forever.

    /// The window a sheet has to go on: the one the web view is in *now*.
    ///
    /// Full screen (03 §7a) is element fullscreen, and WebKit answers it by
    /// moving the web view into a fullscreen window of its own. The app's own
    /// window is still there, still visible to AppKit, and completely covered.
    /// A sheet put on it opens behind the studio, where nobody can see or
    /// answer it — and every one of these dialogs is something the page is
    /// waiting on, so browse looked broken in full screen and stayed broken
    /// until the frame left it.
    private var sheetHost: NSWindow? { webView?.window ?? window }

    /// `<input type="file">`, which is how a studio is given an image, a clip
    /// or audio. Without this, browse does nothing at all.
    func webView(_ webView: WKWebView,
                 runOpenPanelWith parameters: WKOpenPanelParameters,
                 initiatedByFrame frame: WKFrameInfo,
                 completionHandler: @escaping @MainActor ([URL]?) -> Void) {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = parameters.allowsDirectories
        panel.allowsMultipleSelection = parameters.allowsMultipleSelection
        panel.resolvesAliases = true
        guard let host = sheetHost else {
            completionHandler(panel.runModal() == .OK ? panel.urls : nil)
            return
        }
        panel.beginSheetModal(for: host) { response in
            completionHandler(response == .OK ? panel.urls : nil)
        }
    }

    /// alert(): one button, and the page waits for it.
    func webView(_ webView: WKWebView,
                 runJavaScriptAlertPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo,
                 completionHandler: @escaping @MainActor () -> Void) {
        let alert = NSAlert()
        alert.messageText = message
        alert.addButton(withTitle: "OK")
        runSheet(alert) { _ in completionHandler() }
    }

    /// confirm(): OK is true and anything else is false. Unanswered it is
    /// false, so a page's destructive actions quietly stop happening.
    func webView(_ webView: WKWebView,
                 runJavaScriptConfirmPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo,
                 completionHandler: @escaping @MainActor (Bool) -> Void) {
        let alert = NSAlert()
        alert.messageText = message
        alert.addButton(withTitle: "OK")
        alert.addButton(withTitle: "Cancel")
        runSheet(alert) { completionHandler($0 == .alertFirstButtonReturn) }
    }

    /// prompt(): the text typed, or nil when cancelled.
    func webView(_ webView: WKWebView,
                 runJavaScriptTextInputPanelWithPrompt prompt: String,
                 defaultText: String?,
                 initiatedByFrame frame: WKFrameInfo,
                 completionHandler: @escaping @MainActor (String?) -> Void) {
        let alert = NSAlert()
        alert.messageText = prompt
        alert.addButton(withTitle: "OK")
        alert.addButton(withTitle: "Cancel")
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 280, height: 24))
        field.stringValue = defaultText ?? ""
        alert.accessoryView = field
        runSheet(alert) { completionHandler($0 == .alertFirstButtonReturn ? field.stringValue : nil) }
    }

    /// A sheet on the window the web view is in, a modal when there is none.
    private func runSheet(_ alert: NSAlert, done: @escaping (NSApplication.ModalResponse) -> Void) {
        guard let host = sheetHost else {
            done(alert.runModal())
            return
        }
        alert.beginSheetModal(for: host, completionHandler: done)
    }
}

extension AppDelegate: WKNavigationDelegate {
    /// The window is the launcher's and nothing else's.
    ///
    /// A studio's page runs in a frame on its own origin (03 §7a) and is
    /// someone else's code; a link in it that navigated the whole window would
    /// take helmstudio somewhere it has no business being. Sub-frames are left
    /// alone — that is the studio navigating itself — and anything else opens
    /// in the browser the person actually chose.
    func webView(_ webView: WKWebView,
                 decidePolicyFor navigationAction: WKNavigationAction,
                 decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
        // Save, and the context menu's Download Image. It has to be answered
        // before anything else: a thumbnail is a `blob:` URL and a take is
        // served by the daemon, and the origin check below cancelled both, so
        // the download was refused before it could become a `WKDownload` and
        // the delegate that would have run it was never asked.
        if navigationAction.shouldPerformDownload {
            NSLog("helmstudio: download requested for %@", navigationAction.request.url?.absoluteString ?? "(no url)")
            decisionHandler(.download)
            return
        }
        guard navigationAction.targetFrame?.isMainFrame ?? true else {
            decisionHandler(.allow)
            return
        }
        guard let url = navigationAction.request.url else {
            decisionHandler(.cancel)
            return
        }
        if let origin, url.host == origin.host, url.port == origin.port, url.scheme == origin.scheme {
            decisionHandler(.allow)
            return
        }
        decisionHandler(.cancel)
        if navigationAction.navigationType == .linkActivated {
            NSWorkspace.shared.open(url)
        }
    }

    /// A download: the gallery's Save, or the context menu's Download Image.
    ///
    /// WebKit turns the navigation into a `WKDownload` and asks who will run
    /// it. Unanswered, the download is dropped and the page is told nothing —
    /// in the app that looked like a menu item that did nothing at all.
    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        download.delegate = self
    }

    /// The same, for a response that turns out to be an attachment.
    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        download.delegate = self
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        revealLauncher()
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        status.failed("The launcher did not load: \(error.localizedDescription)", log: daemon.log.tail)
        showStatus()
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        status.failed("The launcher did not load: \(error.localizedDescription)", log: daemon.log.tail)
        showStatus()
    }
}

// Downloads land in ~/Downloads, where a browser would put them.
//
// The app is not sandboxed, so this needs no panel and no permission; a Save
// panel per take would be in the way of the one thing the gallery's download
// is for, which is keeping a render without hunting for it in the store.
extension AppDelegate: WKDownloadDelegate {
    func download(_ download: WKDownload,
                  decideDestinationUsing response: URLResponse,
                  suggestedFilename: String,
                  completionHandler: @escaping @MainActor (URL?) -> Void) {
        let fm = FileManager.default
        let dir = fm.urls(for: .downloadsDirectory, in: .userDomainMask).first
            ?? URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent("Downloads")
        // WKDownload refuses a destination that exists, so the second copy of
        // a take becomes "take-2.png" rather than a download that fails with
        // nothing on screen to say why.
        let name = suggestedFilename.isEmpty ? "download" : suggestedFilename
        var url = dir.appendingPathComponent(name)
        let ext = url.pathExtension, stem = url.deletingPathExtension().lastPathComponent
        var n = 2
        while fm.fileExists(atPath: url.path) {
            url = dir.appendingPathComponent(ext.isEmpty ? "\(stem)-\(n)" : "\(stem)-\(n).\(ext)")
            n += 1
        }
        destinations[ObjectIdentifier(download)] = url
        completionHandler(url)
    }

    func downloadDidFinish(_ download: WKDownload) {
        guard let url = destinations.removeValue(forKey: ObjectIdentifier(download)) else { return }
        // What tells the Dock's Downloads stack to bounce and show the file.
        // Without it a download that worked looks exactly like one that did
        // not, which is the bug this whole extension is here to fix.
        DistributedNotificationCenter.default().postNotificationName(
            .init("com.apple.DownloadFileFinished"), object: url.path, userInfo: nil, deliverImmediately: true)
    }

    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        destinations.removeValue(forKey: ObjectIdentifier(download))
        NSLog("helmstudio: download failed: %@", error.localizedDescription)
    }
}
