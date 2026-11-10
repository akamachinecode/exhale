#!/bin/sh
# Exhale installer for Linux, for machines without the .deb or .rpm package.
#
#   sh install.sh                    install or upgrade to the latest release
#   sh install.sh --service          ...and set it up as a systemd service
#   sh install.sh --version 0.1.0    install that release
#
# What it does: downloads checksums.txt and the tarball for this CPU from
# GitHub Releases over HTTPS, checks the tarball's SHA-256, and only then
# copies the binary to /usr/local/bin. Nothing it downloads is run or piped
# into a shell. Running it again is safe: it upgrades, and never touches
# your data or an existing settings file.
#
# Docs: https://github.com/akamachinecode/exhale/blob/main/docs/install/linux.md
set -eu

REPO="akamachinecode/exhale"
# EXHALE_RELEASES_URL points the installer at a mirror (or a test server).
RELEASES_URL="${EXHALE_RELEASES_URL:-https://github.com/$REPO/releases}"
DOCS_URL="https://github.com/$REPO/tree/main/docs/install"

BIN_DIR="/usr/local/bin"
VERSION=""
SERVICE="no"
TMP=""

say() { printf '%s\n' "$*"; }
die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat <<'EOF'
Usage: sh install.sh [--service] [--version X.Y.Z] [--bin-dir DIR]

  --service        Also run Exhale as a systemd service: creates the "exhale"
                   user, /var/lib/exhale (data), /etc/exhale/exhale.env
                   (settings) and enables exhale.service.
  --version X.Y.Z  Install this release instead of the latest one.
  --bin-dir DIR    Where to put the binary (default /usr/local/bin).
  -h, --help       Show this help.
EOF
}

has() { command -v "$1" >/dev/null 2>&1; }

# as_root runs a command as root: directly, or through sudo.
as_root() {
	if [ "$(id -u)" -eq 0 ]; then
		"$@"
	elif has sudo; then
		sudo "$@"
	else
		die "this step needs root and sudo is not installed; run the installer as root"
	fi
}

cleanup() {
	if [ -n "$TMP" ] && [ -d "$TMP" ]; then
		rm -rf "$TMP"
	fi
}

# fetch URL FILE downloads over HTTPS only (plain HTTP is accepted solely for
# a mirror you set yourself in EXHALE_RELEASES_URL).
fetch() {
	if has curl; then
		case "$1" in
		https://*) curl -fsSL --proto '=https' --proto-redir '=https' --retry 2 -o "$2" "$1" ;;
		*) curl -fsSL --retry 2 -o "$2" "$1" ;;
		esac
	elif has wget; then
		wget -q -O "$2" "$1"
	else
		die "curl or wget is needed to download Exhale"
	fi
}

sha256_of() {
	if has sha256sum; then
		sha256sum "$1" | awk '{print $1}'
	elif has shasum; then
		shasum -a 256 "$1" | awk '{print $1}'
	elif has openssl; then
		openssl dgst -sha256 -r "$1" | awk '{print $1}'
	else
		die "sha256sum, shasum or openssl is needed to verify the download"
	fi
}

detect_arch() {
	case "$(uname -m)" in
	x86_64 | amd64) say amd64 ;;
	aarch64 | arm64) say arm64 ;;
	armv7l | armv7) say armv7 ;;
	*) die "no Exhale build for this CPU ($(uname -m)); builds exist for x86_64, arm64 and armv7. See $DOCS_URL" ;;
	esac
}

# install_binary FILE puts the binary in place atomically, so a running
# Exhale keeps its old file until it is restarted.
install_binary() {
	new="$BIN_DIR/.exhale.new.$$"
	if [ -d "$BIN_DIR" ] && [ -w "$BIN_DIR" ]; then
		install -m 0755 "$1" "$new"
		mv -f "$new" "$BIN_DIR/exhale"
	else
		as_root install -d -m 0755 "$BIN_DIR"
		as_root install -m 0755 "$1" "$new"
		as_root mv -f "$new" "$BIN_DIR/exhale"
	fi
}

install_service() {
	unit_src="$1"
	env_src="$2"

	if ! getent group exhale >/dev/null 2>&1; then
		as_root groupadd --system exhale
	fi
	if ! getent passwd exhale >/dev/null 2>&1; then
		as_root useradd --system --gid exhale --home-dir /var/lib/exhale --no-create-home \
			--shell /usr/sbin/nologin --comment "Exhale subscription manager" exhale
	fi
	if [ ! -d /var/lib/exhale ]; then
		as_root install -d -o exhale -g exhale -m 0750 /var/lib/exhale
	fi

	# The unit ships with the package path; point it at this binary.
	sed "s|^ExecStart=/usr/bin/exhale |ExecStart=$BIN_DIR/exhale |" "$unit_src" >"$TMP/exhale.service"
	grep -q "^ExecStart=$BIN_DIR/exhale serve" "$TMP/exhale.service" ||
		die "the service file in this release is not the one this installer expects"
	as_root install -m 0644 "$TMP/exhale.service" /etc/systemd/system/exhale.service

	if as_root test -e /etc/exhale/exhale.env; then
		say "Keeping your settings in /etc/exhale/exhale.env."
	else
		as_root install -d -m 0755 /etc/exhale
		as_root install -m 0640 -o root -g exhale "$env_src" /etc/exhale/exhale.env
	fi

	as_root systemctl daemon-reload
	as_root systemctl enable exhale.service >/dev/null 2>&1 ||
		die "could not enable exhale.service; see: systemctl status exhale"
}

main() {
	while [ $# -gt 0 ]; do
		case "$1" in
		--service) SERVICE="yes" ;;
		--version)
			[ $# -ge 2 ] || die "--version needs a value, e.g. --version 0.1.0"
			VERSION="$2"
			shift
			;;
		--version=*) VERSION="${1#--version=}" ;;
		--bin-dir)
			[ $# -ge 2 ] || die "--bin-dir needs a directory"
			BIN_DIR="$2"
			shift
			;;
		--bin-dir=*) BIN_DIR="${1#--bin-dir=}" ;;
		-h | --help)
			usage
			exit 0
			;;
		*)
			usage >&2
			die "unknown option: $1"
			;;
		esac
		shift
	done

	[ "$(uname -s)" = "Linux" ] ||
		die "this installer is for Linux. For macOS, Windows and Docker see $DOCS_URL"
	ARCH="$(detect_arch)"

	VERSION="${VERSION#v}"
	case "$VERSION" in
	*[!0-9A-Za-z.+-]*) die "this does not look like a version: $VERSION (expected something like 0.1.0)" ;;
	esac
	case "$BIN_DIR" in
	/*) ;;
	*) die "--bin-dir must be an absolute path" ;;
	esac
	BIN_DIR="${BIN_DIR%/}"

	if [ "$SERVICE" = "yes" ]; then
		if ! has systemctl || [ ! -d /run/systemd/system ]; then
			die "--service needs systemd, which is not running here. Install without --service and start 'exhale serve' with your init system"
		fi
		if [ -e /usr/lib/systemd/system/exhale.service ] || [ -e /lib/systemd/system/exhale.service ]; then
			die "Exhale is already installed here from a .deb or .rpm package; upgrade it with your package manager instead"
		fi
		case "$BIN_DIR" in
		/home/* | /root/* | /run/user/*) die "--service cannot use a binary in $BIN_DIR: the service is not allowed to read home directories. Use the default /usr/local/bin" ;;
		esac
	fi

	TMP="$(mktemp -d)"
	trap cleanup EXIT
	trap 'exit 1' INT TERM HUP

	# 1. Which release. checksums.txt names every file of the release, so it
	#    also tells us the latest version without calling the GitHub API.
	if [ -n "$VERSION" ]; then
		sums_url="$RELEASES_URL/download/v$VERSION/checksums.txt"
		fetch "$sums_url" "$TMP/checksums.txt" ||
			die "release $VERSION was not found ($sums_url). See $RELEASES_URL for the list"
	else
		sums_url="$RELEASES_URL/latest/download/checksums.txt"
		fetch "$sums_url" "$TMP/checksums.txt" ||
			die "no published release of Exhale was found ($sums_url). There may be none yet: check $RELEASES_URL, or build from source (see the README)"
	fi

	tarball="$(awk '{print $2}' "$TMP/checksums.txt" | sed 's/^\*//' |
		grep -E "^exhale_[0-9A-Za-z.+-]+_linux_${ARCH}\.tar\.gz\$" | head -n 1 || true)"
	[ -n "$tarball" ] || die "this release has no build for linux/$ARCH. See $RELEASES_URL"
	found="${tarball#exhale_}"
	found="${found%%_*}"
	if [ -n "$VERSION" ] && [ "$found" != "$VERSION" ]; then
		die "asked for version $VERSION but the release holds $found"
	fi
	VERSION="$found"

	current=""
	if [ -x "$BIN_DIR/exhale" ]; then
		current="$("$BIN_DIR/exhale" version 2>/dev/null | awk '{print $2}' || true)"
	fi

	# 2. Download, verify, unpack. Skipped when this version is already
	#    installed and there is no service to set up.
	if [ "$current" = "$VERSION" ] && [ "$SERVICE" = "no" ]; then
		say "Exhale $VERSION is already installed in $BIN_DIR. Nothing to do."
		exit 0
	fi

	say "Downloading Exhale $VERSION for linux/$ARCH..."
	fetch "$RELEASES_URL/download/v$VERSION/$tarball" "$TMP/$tarball" ||
		die "could not download $tarball"

	want="$(awk -v f="$tarball" '{ n = $2; sub(/^\*/, "", n); if (n == f) { print $1; exit } }' "$TMP/checksums.txt")"
	got="$(sha256_of "$TMP/$tarball")"
	[ -n "$want" ] && [ -n "$got" ] || die "could not compute the checksum of $tarball"
	if [ "$want" != "$got" ]; then
		die "checksum mismatch for $tarball: expected $want, got $got. Nothing was installed"
	fi
	say "Checksum verified (SHA-256 $got)."

	mkdir "$TMP/unpacked"
	tar -xzf "$TMP/$tarball" -C "$TMP/unpacked"
	[ -f "$TMP/unpacked/exhale" ] || die "the release tarball has no exhale binary"

	# 3. Install.
	changed="no"
	if [ "$current" = "$VERSION" ]; then
		say "Exhale $VERSION is already installed in $BIN_DIR."
	else
		install_binary "$TMP/unpacked/exhale"
		changed="yes"
		if [ -n "$current" ]; then
			say "Upgraded Exhale $current to $VERSION in $BIN_DIR."
		else
			say "Installed Exhale $VERSION in $BIN_DIR."
		fi
	fi

	if [ "$SERVICE" = "yes" ]; then
		[ -f "$TMP/unpacked/deploy/systemd/exhale.service" ] && [ -f "$TMP/unpacked/deploy/systemd/exhale.env" ] ||
			die "this release does not include the systemd files; the binary is installed, but set up the service by hand (see $DOCS_URL)"
		install_service "$TMP/unpacked/deploy/systemd/exhale.service" "$TMP/unpacked/deploy/systemd/exhale.env"
		if systemctl is-active --quiet exhale.service 2>/dev/null; then
			if [ "$changed" = "yes" ]; then
				as_root systemctl restart exhale.service
				say "The service was restarted with the new version."
			else
				say "The service is running."
			fi
			cat <<EOF

  Logs       sudo journalctl -u exhale -n 20
  Settings   /etc/exhale/exhale.env
  Data       /var/lib/exhale
EOF
		else
			cat <<EOF

Exhale is set up as a service and will start at boot.

  Settings   /etc/exhale/exhale.env
  Data       /var/lib/exhale

Start it now:

  sudo systemctl start exhale
  sudo journalctl -u exhale -n 20     # the address and the one-time setup code

Then open http://127.0.0.1:8484 on this machine.
To reach it from other devices or over HTTPS: $DOCS_URL
EOF
		fi
	else
		if has systemctl && systemctl is-active --quiet exhale.service 2>/dev/null; then
			say "Restart the service to use the new version:  sudo systemctl restart exhale"
		fi
		cat <<EOF

Start it:

  exhale serve

Then open http://127.0.0.1:8484 on this machine. Your data is kept in
~/.local/share/exhale. To run it as a service, run this installer again
with --service. More: $DOCS_URL
EOF
		case ":$PATH:" in
		*":$BIN_DIR:"*) ;;
		*) say "Note: $BIN_DIR is not in your PATH; run $BIN_DIR/exhale instead." ;;
		esac
	fi
}

main "$@"
