import AppKit

/// What the window shows before the launcher is loaded, and instead of it when
/// the daemon could not be started. A blank window that never fills in tells a
/// person nothing; this says what is happening, or what went wrong and what
/// the daemon said about it.
final class StatusView: NSView {
    private let spinner = NSProgressIndicator()
    private let title = NSTextField(labelWithString: "")
    private let detail = NSTextView()
    private let detailScroll = NSScrollView()
    private let retry = NSButton()

    var onRetry: (() -> Void)?

    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
        title.font = .systemFont(ofSize: 15, weight: .medium)
        title.alignment = .center
        title.lineBreakMode = .byWordWrapping
        title.maximumNumberOfLines = 0

        spinner.style = .spinning
        spinner.controlSize = .small
        spinner.isDisplayedWhenStopped = false

        detail.isEditable = false
        detail.drawsBackground = false
        detail.font = .monospacedSystemFont(ofSize: 11, weight: .regular)
        detail.textContainerInset = NSSize(width: 8, height: 8)
        detailScroll.documentView = detail
        detailScroll.hasVerticalScroller = true
        detailScroll.drawsBackground = false
        detailScroll.borderType = .lineBorder
        detailScroll.isHidden = true

        retry.title = "Try again"
        retry.bezelStyle = .rounded
        retry.target = self
        retry.action = #selector(retryTapped)
        retry.isHidden = true

        for v in [spinner, title, detailScroll, retry] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            addSubview(v)
        }
        NSLayoutConstraint.activate([
            title.centerXAnchor.constraint(equalTo: centerXAnchor),
            title.centerYAnchor.constraint(equalTo: centerYAnchor, constant: -90),
            title.leadingAnchor.constraint(greaterThanOrEqualTo: leadingAnchor, constant: 32),
            title.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -32),
            spinner.centerXAnchor.constraint(equalTo: centerXAnchor),
            spinner.bottomAnchor.constraint(equalTo: title.topAnchor, constant: -16),
            detailScroll.topAnchor.constraint(equalTo: title.bottomAnchor, constant: 16),
            detailScroll.centerXAnchor.constraint(equalTo: centerXAnchor),
            detailScroll.widthAnchor.constraint(equalTo: widthAnchor, multiplier: 0.8),
            detailScroll.heightAnchor.constraint(equalToConstant: 160),
            retry.topAnchor.constraint(equalTo: detailScroll.bottomAnchor, constant: 16),
            retry.centerXAnchor.constraint(equalTo: centerXAnchor),
        ])
    }

    required init?(coder: NSCoder) { fatalError("not loaded from a nib") }

    /// The launcher's own page ground, so the window is the right colour
    /// before the launcher has painted anything and there is no white frame
    /// between the two.
    override func updateLayer() {
        layer?.backgroundColor = Ground.color.cgColor
    }

    override var wantsUpdateLayer: Bool { true }

    @objc private func retryTapped() { onRetry?() }

    func working(_ message: String) {
        title.stringValue = message
        spinner.startAnimation(nil)
        detailScroll.isHidden = true
        retry.isHidden = true
    }

    func failed(_ message: String, log: String) {
        title.stringValue = message
        spinner.stopAnimation(nil)
        detail.string = log
        detailScroll.isHidden = log.isEmpty
        retry.isHidden = false
    }
}
