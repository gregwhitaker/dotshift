import Foundation
import ImageIO

// Encode the PNG representations in memory so package builds only write the
// final icon, without using sips' temporary files or the IconServices cache.
guard CommandLine.arguments.count == 3 else {
    fatalError("Usage: build-icon source.png destination.icns")
}
let sourceURL = URL(fileURLWithPath: CommandLine.arguments[1])
guard let source = CGImageSourceCreateWithURL(sourceURL as CFURL, nil) else {
    fatalError("Cannot read icon source: \(sourceURL.path)")
}
let representations: [(String, Int)] = [
    ("icp4", 16), ("icp5", 32), ("icp6", 64),
    ("ic07", 128), ("ic08", 256), ("ic09", 512), ("ic10", 1024),
    ("ic11", 32), ("ic12", 64), ("ic13", 256), ("ic14", 512),
]

func lengthBytes(_ length: Int) -> Data {
    var bigEndian = UInt32(length).bigEndian
    return withUnsafeBytes(of: &bigEndian) { Data($0) }
}

var chunks = Data()
for (kind, size) in representations {
    let options: [CFString: Any] = [
        kCGImageSourceCreateThumbnailFromImageAlways: true,
        kCGImageSourceThumbnailMaxPixelSize: size,
        kCGImageSourceCreateThumbnailWithTransform: true,
    ]
    guard let image = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else {
        fatalError("Cannot resize icon to \(size) pixels")
    }
    let png = NSMutableData()
    guard let destination = CGImageDestinationCreateWithData(png, "public.png" as CFString, 1, nil) else {
        fatalError("Cannot create PNG encoder")
    }
    CGImageDestinationAddImage(destination, image, nil)
    guard CGImageDestinationFinalize(destination) else {
        fatalError("Cannot encode icon at \(size) pixels")
    }
    chunks.append(Data(kind.utf8))
    chunks.append(lengthBytes(png.length + 8))
    chunks.append(png as Data)
}
let icon = Data("icns".utf8) + lengthBytes(chunks.count + 8) + chunks
// A non-atomic write avoids Foundation's temporary directory outside the build.
try icon.write(to: URL(fileURLWithPath: CommandLine.arguments[2]))
