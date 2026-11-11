#!/bin/sh
# Builds Exhale.dmg: Exhale.app for Apple silicon and Intel in a disk image
# with a link to Applications (ADR 0029, 0030). Runs on macOS (Apple's clang
# for cgo, lipo, iconutil, codesign, hdiutil); the web UI must be built
# first (web/dist).
#
#   sh deploy/desktop/macos/build.sh 0.1.0 [out-dir]
set -eu
cd "$(dirname "$0")/../../.."

version="${1:?usage: build.sh <version> [out-dir]}"
out="${2:-dist-desktop}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

[ -f web/dist/index.html ] || { echo "build the web UI first: cd web && pnpm build" >&2; exit 1; }

app="$work/Exhale.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources" "$out"
out="$(cd "$out" && pwd)"

# One binary for both kinds of Mac. The desktop tag adds Exhale's window,
# WebKit through cgo (ADR 0030): Go hands clang -arch for each one, and
# macOS 12 is the oldest it runs on, as Info.plist says. desktopApp makes it
# open Exhale when it is started with no arguments, and start Exhale at
# login; launchd runs this same binary as the server.
for arch in arm64 amd64; do
	CGO_ENABLED=1 GOOS=darwin GOARCH="$arch" MACOSX_DEPLOYMENT_TARGET=12.0 go build -trimpath -tags desktop,production \
		-ldflags="-s -w -X main.version=$version -X main.desktopApp=true" \
		-o "$work/exhale-$arch" ./cmd/exhale
done
lipo -create -output "$app/Contents/MacOS/exhale" "$work/exhale-arm64" "$work/exhale-amd64"

sed "s/{{VERSION}}/$version/g" deploy/desktop/macos/Info.plist >"$app/Contents/Info.plist"
plutil -lint "$app/Contents/Info.plist"

# The icon, at every size Finder and the Dock ask for.
iconset="$work/Exhale.iconset"
mkdir "$iconset"
for s in 16 32 128 256 512; do
	sips -z "$s" "$s" deploy/desktop/icon-1024.png --out "$iconset/icon_${s}x${s}.png" >/dev/null
	d=$((s * 2))
	sips -z "$d" "$d" deploy/desktop/icon-1024.png --out "$iconset/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$iconset" -o "$app/Contents/Resources/Exhale.icns"

# Signed ad hoc (no Apple Developer ID yet): the whole bundle is sealed, so
# macOS asks once to confirm it instead of calling it damaged. With a
# Developer ID this becomes a real signature plus notarization.
codesign --force --deep --sign - --identifier io.github.akamachinecode.exhale "$app"
codesign --verify --deep --strict "$app"

stage="$work/dmg"
mkdir "$stage"
cp -R "$app" "$stage/"
ln -s /Applications "$stage/Applications"
rm -f "$out/Exhale.dmg"
hdiutil create -volname Exhale -srcfolder "$stage" -fs HFS+ -format UDZO -ov "$out/Exhale.dmg" >/dev/null
echo "$out/Exhale.dmg"
