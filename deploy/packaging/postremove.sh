#!/bin/sh
# Runs after the package files are removed (deb postrm, rpm %postun).
#   deb: $1 = remove, then purge (or upgrade…)
#   rpm: $1 = 0 on removal, 1 on an upgrade
# Your data is never deleted: /var/lib/exhale and the exhale user stay.
set -e

case "${1:-}" in
remove | 0)
	if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
		systemctl daemon-reload || true
	fi
	if [ -d /var/lib/exhale ]; then
		cat <<'MSG'

Exhale was removed. Your data is still in /var/lib/exhale.
To delete it for good:  sudo rm -rf /var/lib/exhale && sudo userdel exhale

MSG
	fi
	;;
esac
