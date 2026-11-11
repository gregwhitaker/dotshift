// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "DotshiftMenuBar",
    platforms: [.macOS(.v13)],
    products: [.executable(name: "DotshiftMenuBar", targets: ["DotshiftMenuBar"])],
    targets: [
        .target(name: "MenuBarCore"),
        .executableTarget(name: "DotshiftMenuBar", dependencies: ["MenuBarCore"]),
        .testTarget(name: "MenuBarCoreTests", dependencies: ["MenuBarCore"]),
    ]
)
