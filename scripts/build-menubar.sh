#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")/.."
if [[ "$(uname -s)" != Darwin ]]; then
  echo "The menu bar app requires macOS and Xcode Command Line Tools." >&2
  exit 1
fi

export CLANG_MODULE_CACHE_PATH="$PWD/build/module-cache"
export SWIFTPM_MODULECACHE_OVERRIDE="$CLANG_MODULE_CACHE_PATH"
swift build --disable-sandbox --cache-path build/swift-cache --package-path macos --scratch-path build/swift -c release
app="build/Dotshift.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources" build/Dotshift.iconset
cp build/swift/release/DotshiftMenuBar "$app/Contents/MacOS/"
cp dotshift "$app/Contents/Resources/dotshift"
cp macos/Info.plist "$app/Contents/Info.plist"
for size in 16 32 128 256 512; do
  sips -z "$size" "$size" brand/png/dotshift-app-icon-1024.png --out "build/Dotshift.iconset/icon_${size}x${size}.png" >/dev/null
  double=$((size * 2))
  sips -z "$double" "$double" brand/png/dotshift-app-icon-1024.png --out "build/Dotshift.iconset/icon_${size}x${size}@2x.png" >/dev/null
done
# Package the PNG representations directly. Unlike iconutil this also works in
# build sandboxes without access to the user's IconServices cache.
python3 - <<'PY'
from pathlib import Path
import struct

types = {
    'icp4': '16x16', 'icp5': '32x32', 'icp6': '32x32@2x',
    'ic07': '128x128', 'ic08': '256x256', 'ic09': '512x512',
    'ic10': '512x512@2x', 'ic11': '16x16@2x',
    'ic12': '32x32@2x', 'ic13': '128x128@2x', 'ic14': '256x256@2x',
}
chunks = []
for kind, size in types.items():
    png = Path(f'build/Dotshift.iconset/icon_{size}.png').read_bytes()
    chunks.append(kind.encode('ascii') + struct.pack('>I', len(png) + 8) + png)
body = b''.join(chunks)
Path('build/Dotshift.app/Contents/Resources/Dotshift.icns').write_bytes(
    b'icns' + struct.pack('>I', len(body) + 8) + body)
PY
# An ad-hoc signature gives local builds a stable bundle identity. Distribution
# builds should set a Developer ID identity and be notarized separately.
codesign --force --sign "${DOTSHIFT_SIGN_IDENTITY:--}" "$app/Contents/Resources/dotshift"
codesign --force --sign "${DOTSHIFT_SIGN_IDENTITY:--}" "$app"
echo "Built $app. Launch with ./dotshift menubar -config /path/to/dotshift.yaml"
