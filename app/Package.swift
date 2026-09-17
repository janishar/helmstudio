// swift-tools-version:6.0
//
// helmstudio.app is built with SwiftPM rather than an Xcode project: the
// bundle is assembled by `make app`, which is readable, runs without Xcode
// installed, and leaves nothing generated in the tree to review
// (docs/decisions.md, 2026-09-18).
import PackageDescription

let package = Package(
    name: "helmstudio",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(name: "helmstudio", path: "Sources/helmstudio")
    ]
)
