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
