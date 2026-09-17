import Foundation

/// What a running daemon publishes about itself: one line of JSON on stdout
/// when it starts listening, and the same record left in its data root as
/// `daemon.json`. `internal/handshake` in the daemon writes both; this is the
/// only place the shell reads them.
struct Handshake: Decodable, Sendable, Equatable {
    /// Where the daemon listens, as it reported after opening the listener.
    let addr: String
    /// The origin the launcher is served from. The shell loads this rather
    /// than building a URL, so it can never build one the daemon's Origin
    /// check refuses.
    let url: String
    let pid: Int32
    let startTime: Int64
    let version: String
    /// The data root the record was written in.
    let data: String

    enum CodingKeys: String, CodingKey {
        case addr, url, pid, version, data
        case startTime = "start_time"
    }

    static func decode(_ bytes: Data) throws -> Handshake {
        try JSONDecoder().decode(Handshake.self, from: bytes)
    }
}

/// What `GET /api/v1/launcher/settings/about` answers. The shell reads it for
/// one purpose: to be sure the thing listening on a recorded port is really
/// helmstudio and really this tree, before adopting it.
struct About: Decodable, Sendable {
    let daemonVersion: String
    let paths: Paths

    struct Paths: Decodable, Sendable {
        let data: String
    }

    enum CodingKeys: String, CodingKey {
        case daemonVersion = "daemon_version"
        case paths
    }
}
