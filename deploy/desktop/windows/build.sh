#!/bin/sh
# Builds Exhale-Setup.exe: Exhale.exe for Windows (no console window) in a
# one-click installer (ADR 0029). Runs on macOS or Linux with makensis
# (brew install makensis, apt install nsis); the web UI must be built
# first (web/dist).
#
#   sh deploy/desktop/windows/build.sh 0.1.0 [out-dir]
set -eu
cd "$(dirname "$0")/../../.."

version="${1:?usage: build.sh <version> [out-dir]}"
out="${2:-dist-desktop}"
work="$(mktemp -d)"
trap 'rm -rf "$work"; rm -f cmd/exhale/rsrc_windows_*.syso' EXIT

[ -f web/dist/index.html ] || { echo "build the web UI first: cd web && pnpm build" >&2; exit 1; }
mkdir -p "$out"
out="$(cd "$out" && pwd)"

# Windows wants four numbers: 0.1.0 → 0.1.0.0 (a suffix such as -rc.1 is dropped).
winversion="$(printf '%s' "$version" | sed 's/[^0-9.].*//').0"

# The icon and the version Explorer shows, linked into Exhale.exe.
(cd cmd/exhale && go run github.com/tc-hib/go-winres@v0.3.3 simply --arch amd64 --manifest gui \
	--icon ../../deploy/desktop/windows/exhale.ico --product-name Exhale --file-description Exhale \
	--product-version "$winversion" --file-version "$winversion" --original-filename Exhale.exe \
	--copyright AGPL-3.0-or-later)

# -H windowsgui: no console window. desktopApp makes it open Exhale when it
# is started with no arguments, and start Exhale at sign-in.
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
	-ldflags="-s -w -H windowsgui -X main.version=$version -X main.desktopApp=true" \
	-o "$work/Exhale.exe" ./cmd/exhale

cp deploy/desktop/windows/exhale.nsi deploy/desktop/windows/exhale.ico "$work/"
makensis -V2 -DVERSION="$version" -DWINVERSION="$winversion" -DOUTFILE="$out/Exhale-Setup.exe" "$work/exhale.nsi"
echo "$out/Exhale-Setup.exe"
