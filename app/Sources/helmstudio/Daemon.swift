import Foundation

enum DaemonError: LocalizedError {
    case noBundledDaemon(String)
    case dataRootUnavailable(String)
    case exitedBeforeAnnouncing(Int32, String)
    case announceTimedOut(String)
    case unreadableAnnouncement(String, String)

    var errorDescription: String? {
        switch self {
        case .noBundledDaemon(let path):
            return "This copy of helmstudio has no daemon in it: nothing at \(path). The app bundles the daemon it runs, so this build is incomplete rather than misconfigured."
        case .dataRootUnavailable(let why):
            return "The daemon could not say where its data lives, so there is no way to tell whether one is already running: \(why)"
        case .exitedBeforeAnnouncing(let status, let log):
            return "The daemon exited with status \(status) before it started listening.\n\n\(log)"
        case .announceTimedOut(let log):
            return "The daemon did not start listening in time and was stopped.\n\n\(log)"
        case .unreadableAnnouncement(let line, let why):
            return "The daemon announced something this app could not read (\(why)): \(line)"
        }
    }
}

/// The daemon's stderr, kept so a failure to start can be shown to a person
/// instead of a blank window. Everything the daemon says to a person goes to
/// stderr; stdout belongs to the handshake.
final class DaemonLog: @unchecked Sendable {
    private let lock = NSLock()
    private var text = ""
    private let limit = 64_000

    func append(_ data: Data) {
        guard let s = String(data: data, encoding: .utf8), !s.isEmpty else { return }
        lock.lock()
        defer { lock.unlock() }
        text += s
        if text.count > limit { text.removeFirst(text.count - limit) }
    }

    var tail: String {
        lock.lock()
        defer { lock.unlock() }
        return text
    }
}

/// The daemon as the shell sees it: one that is already running and can be
/// adopted, or the binary inside the bundle, started as a sidecar
/// (docs/design/01-prd.md R69).
///
/// Nothing here knows what a studio is. The shell's whole part in this is to
/// end up with a URL to load, and to know whether the daemon behind it is one
/// it started and may therefore stop.
@MainActor
final class Daemon {
    /// The process this shell started, if it started one. A daemon that was
    /// already running when the app launched is not the shell's to end.
    private(set) var child: Process?
    private(set) var handshake: Handshake?
    private(set) var adopted = false
    let log = DaemonLog()

    /// The daemon binary inside the bundle. It lives beside the app's own
    /// executable in Contents/MacOS, which is where a nested Mach-O binary
    /// has to be for the hardened runtime and the Developer ID signature to
    /// cover it (R71).
    static var bundledBinary: URL {
        Bundle.main.bundleURL.appending(path: "Contents/MacOS/helmstudio-daemon")
    }

    /// The registry the app ships with. R72 ships the shell, the daemon and
    /// this together, which is what makes adding a studio to the catalogue a
    /// release. The daemon's own default is a relative path that resolves to
    /// nothing outside the repository, so the shell always passes this.
    static var bundledStudios: URL {
        Bundle.main.bundleURL.appending(path: "Contents/Resources/studios")
    }

    /// Where the daemon would keep its data. The shell asks the daemon rather
    /// than resolving it again: the precedence runs through HELMSTUDIO_HOME,
    /// HELMSTUDIO_DATA_DIR and a platform default, and a second implementation
    /// of it is a second answer (R73).
    static func dataRoot() async throws -> String {
        let binary = bundledBinary
        guard FileManager.default.isExecutableFile(atPath: binary.path) else {
            throw DaemonError.noBundledDaemon(binary.path)
        }
        let proc = Process()
        proc.executableURL = binary
        proc.arguments = ["-print-data-dir"]
        let out = Pipe(), err = Pipe()
        proc.standardOutput = out
        proc.standardError = err
        do {
            try proc.run()
        } catch {
            throw DaemonError.dataRootUnavailable(error.localizedDescription)
        }
        let data = out.fileHandleForReading.readDataToEndOfFile()
        let stderr = err.fileHandleForReading.readDataToEndOfFile()
        proc.waitUntilExit()
        let root = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        if proc.terminationStatus != 0 || root.isEmpty {
            throw DaemonError.dataRootUnavailable(String(decoding: stderr, as: UTF8.self)
                .trimmingCharacters(in: .whitespacesAndNewlines))
        }
        return root
    }

    /// The record a running daemon leaves in its data root.
    static func record(in dataRoot: String) -> Handshake? {
        let path = URL(filePath: dataRoot).appending(path: "daemon.json")
        guard let bytes = try? Data(contentsOf: path) else { return nil }
        return try? Handshake.decode(bytes)
    }

    /// Adopt a daemon that is already running, or return nil.
    ///
    /// The record alone is not enough: a daemon killed with `kill -9` leaves
    /// one behind naming a pid that is gone, or that some later process
    /// inherited. The daemon settles that on its side by recording a start
    /// time beside the pid; the shell does not repeat that check, because what
    /// it actually needs to know is not whether a process exists but whether
    /// helmstudio is answering on that port for this tree. So: the pid is
    /// alive, and `about` says it is helmstudio and says it is this data root.
    static func adopt(dataRoot: String) async -> Handshake? {
        guard let record = record(in: dataRoot) else { return nil }
        guard record.pid > 0, kill(record.pid, 0) == 0 else { return nil }
        guard let about = await about(at: record.url) else { return nil }
        guard about.paths.data == dataRoot else { return nil }
        return record
    }

    /// Ask a daemon to describe itself. Anything but a helmstudio answering
    /// for this tree — a timeout, another server on a recycled port, an error
    /// document — is nil, which means "do not adopt".
    static func about(at base: String) async -> About? {
        guard let url = URL(string: base + "/api/v1/launcher/settings/about") else { return nil }
        var request = URLRequest(url: url)
        request.timeoutInterval = 3
        guard let (data, response) = try? await URLSession.shared.data(for: request),
              let http = response as? HTTPURLResponse, http.statusCode == 200 else { return nil }
        return try? JSONDecoder().decode(About.self, from: data)
    }

    /// Start the bundled daemon and wait for it to say it is listening.
    ///
    /// The handshake is the readiness signal, so there is no port to guess and
    /// nothing to poll. The deadline is generous because a starting daemon
    /// re-adopts whatever the last one left running before it listens.
    func start(dataRoot: String, timeout: Duration = .seconds(30)) async throws -> Handshake {
        let binary = Daemon.bundledBinary
        guard FileManager.default.isExecutableFile(atPath: binary.path) else {
            throw DaemonError.noBundledDaemon(binary.path)
        }
        let proc = Process()
        proc.executableURL = binary
        proc.arguments = ["-studios", Daemon.bundledStudios.path]
        let out = Pipe(), err = Pipe()
        proc.standardOutput = out
        proc.standardError = err
        let log = self.log
        err.fileHandleForReading.readabilityHandler = { handle in
            log.append(handle.availableData)
        }
        try proc.run()
        child = proc

        let reader = Task.detached { () -> Data? in
            var buffer = Data()
            while true {
                let chunk = out.fileHandleForReading.availableData
                if chunk.isEmpty { return nil }          // the pipe closed: no line is coming
                buffer.append(chunk)
                if let end = buffer.firstIndex(of: UInt8(ascii: "\n")) {
                    return buffer[buffer.startIndex..<end]
                }
            }
        }
        // Stopping the daemon closes its stdout, which ends the read above;
        // a blocking read cannot be cancelled any other way.
        let deadline = Task {
            try await Task.sleep(for: timeout)
            proc.terminate()
        }
        let line = await reader.value
        deadline.cancel()

        guard let line else {
            proc.waitUntilExit()
            err.fileHandleForReading.readabilityHandler = nil
            log.append(err.fileHandleForReading.readDataToEndOfFile())
            child = nil
            if proc.terminationReason == .uncaughtSignal {
                throw DaemonError.announceTimedOut(log.tail)
            }
            throw DaemonError.exitedBeforeAnnouncing(proc.terminationStatus, log.tail)
        }
        do {
            let shake = try Handshake.decode(line)
            handshake = shake
            adopted = false
            return shake
        } catch {
            throw DaemonError.unreadableAnnouncement(String(decoding: line, as: UTF8.self),
                                                     error.localizedDescription)
        }
    }

    /// Resolve a daemon: adopt one if it is there, start one otherwise.
    func resolve() async throws -> Handshake {
        let root = try await Daemon.dataRoot()
        if let existing = await Daemon.adopt(dataRoot: root) {
            handshake = existing
            adopted = true
            child = nil
            return existing
        }
        return try await start(dataRoot: root)
    }

    /// R69: on quit the shell shuts the daemon down cleanly, or leaves it
    /// running when the user chose to keep studios alive.
    ///
    /// A daemon this shell adopted is left alone either way — it was running
    /// before the app and is not the app's to end. SIGTERM is the clean path:
    /// the daemon stops its studios, records what it stopped, and withdraws
    /// its record. Only `kill -9` leaves studios behind for the next daemon,
    /// which is what its recorded start time exists to sort out.
    ///
    /// The wait yields rather than blocks, so a window stays drawn while a
    /// studio holding twenty gigabytes takes its time going down.
    func stop(timeout: Duration = .seconds(120)) async {
        guard !adopted, let proc = child, proc.isRunning else { return }
        proc.terminate()
        let deadline = ContinuousClock.now.advanced(by: timeout)
        while proc.isRunning && ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(50))
        }
        child = nil
    }

    /// Whether quitting should stop the daemon: only one this shell started,
    /// and only when the user has not asked to keep studios running.
    var shouldStopOnQuit: Bool {
        guard !adopted, let proc = child, proc.isRunning else { return false }
        _ = proc
        return !UserDefaults.standard.bool(forKey: Defaults.keepStudiosRunning)
    }
}

/// The shell's own preferences. 02 §11: window bounds, the update channel and
/// the menu-bar preference live in the app's own user data, never in helm.db,
/// because a second settings store would be a second source of truth.
enum Defaults {
    /// R69: quit leaves the daemon running when the user chose to keep
    /// studios alive in the menu bar.
    static let keepStudiosRunning = "keepStudiosRunning"
    /// The window's saved frame, which AppKit writes under this name.
    static let windowFrame = "helmstudioWindow"
}
