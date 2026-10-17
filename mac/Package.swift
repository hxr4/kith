// swift-tools-version: 6.2
// Kith for macOS: a native SwiftUI client for the local kith daemon.
// Build the app bundle with `make app` (see Makefile); `swift build` alone
// produces the bare executable.
import PackageDescription

let package = Package(
    name: "KithMac",
    platforms: [.macOS(.v26)],
    targets: [
        .executableTarget(
            name: "Kith",
            path: "Sources/Kith",
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
        .testTarget(
            name: "KithTests",
            dependencies: ["Kith"],
            path: "Tests/KithTests",
            swiftSettings: [.swiftLanguageMode(.v5)]
        ),
    ]
)
