#!/bin/sh
# Runs before the package files are removed (deb prerm, rpm %preun).
#   deb: $1 = remove (or upgrade, deconfigure…)
#   rpm: $1 = 0 on removal, 1 on an upgrade
# Stops and disables the service on removal only; an upgrade keeps it
# running until the new version restarts it.
set -e

case "${1:-}" in
remove | 0)
	if command -v systemctl >/dev/null 2>&1; then
		if [ -d /run/systemd/system ]; then
			systemctl disable --now exhale.service >/dev/null 2>&1 || true
		else
			systemctl disable exhale.service >/dev/null 2>&1 || true
		fi
	fi
	;;
esac
