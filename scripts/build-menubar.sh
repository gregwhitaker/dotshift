#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")/.."
if [[ "$(uname -s)" != Darwin ]]; then
  echo "The menu bar app requires macOS and Xcode Command Line Tools." >&2
  exit 1
fi

export CLANG_MODULE_CACHE_PATH="$PWD/build/module-cache"
export SWIFTPM_MODULECACHE_OVERRIDE="$CLANG_MODULE_CACHE_PATH"
# The release bundle has two local modules and no package dependencies.
# Compile them directly so SwiftPM's Foundation temporary-file writes do not
# escape a package manager's build directory. SwiftPM still runs native tests.
native="$PWD/build/native"
mkdir -p "$native" "$CLANG_MODULE_CACHE_PATH"
target="$(uname -m)-apple-macosx13.0"
swiftc -O -whole-module-optimization -swift-version 5 -target "$target" \
  -module-cache-path "$CLANG_MODULE_CACHE_PATH" \
  -parse-as-library -emit-library -static -emit-module -module-name MenuBarCore \
  -emit-module-path "$native/MenuBarCore.swiftmodule" \
  -o "$native/libMenuBarCore.a" macos/Sources/MenuBarCore/*.swift
swiftc -O -whole-module-optimization -swift-version 5 -target "$target" \
  -module-cache-path "$CLANG_MODULE_CACHE_PATH" \
  -I "$native" -L "$native" -lMenuBarCore \
  -o "$native/DotshiftMenuBar" macos/Sources/DotshiftMenuBar/*.swift
app="build/Dotshift.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$native/DotshiftMenuBar" "$app/Contents/MacOS/"
cp dotshift "$app/Contents/Resources/dotshift"
cp macos/Info.plist "$app/Contents/Info.plist"
# Keep icon rendering in memory; sips and iconutil write to user caches outside
# Homebrew's build sandbox, even when their output is inside the build directory.
swiftc -O -module-cache-path "$CLANG_MODULE_CACHE_PATH" \
  -o "$native/build-icon" scripts/build-menubar-icon.swift
"$native/build-icon" brand/png/dotshift-app-icon-1024.png "$app/Contents/Resources/Dotshift.icns"
# An ad-hoc signature gives local builds a stable bundle identity. Distribution
# builds should set a Developer ID identity and be notarized separately.
codesign --force --sign "${DOTSHIFT_SIGN_IDENTITY:--}" "$app/Contents/Resources/dotshift"
codesign --force --sign "${DOTSHIFT_SIGN_IDENTITY:--}" "$app"
echo "Built $app. Launch with ./dotshift menubar -config /path/to/dotshift.yaml"
