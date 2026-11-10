#!/bin/sh
# Runs after the package files are in place (deb postinst, rpm %post).
#   deb: $1 = configure, $2 = previous version (empty on a first install)
#   rpm: $1 = 1 on a first install, 2 on an upgrade
set -e

upgrade=no
case "${1:-}" in
configure) [ -n "${2:-}" ] && upgrade=yes ;;
1) ;;
[2-9]*) upgrade=yes ;;
*) exit 0 ;; # deb error handling (abort-upgrade, abort-remove…): nothing to do
esac

# The service user and its data directory, if missing. Nothing here ever
# deletes or empties /var/lib/exhale.
if ! getent group exhale >/dev/null 2>&1; then
	groupadd --system exhale
fi
if ! getent passwd exhale >/dev/null 2>&1; then
	useradd --system --gid exhale --home-dir /var/lib/exhale --no-create-home \
		--shell /usr/sbin/nologin --comment "Exhale subscription manager" exhale
fi
if [ ! -d /var/lib/exhale ]; then
	install -d -o exhale -g exhale -m 0750 /var/lib/exhale
fi

has_systemctl=no
if command -v systemctl >/dev/null 2>&1; then
	has_systemctl=yes
fi
running_systemd=no
if [ "$has_systemctl" = yes ] && [ -d /run/systemd/system ]; then
	running_systemd=yes
	systemctl daemon-reload || true
fi

if [ "$upgrade" = yes ]; then
	# Restart only if it was running; a stopped or disabled service stays so.
	if [ "$running_systemd" = yes ]; then
		systemctl try-restart exhale.service || true
	fi
	exit 0
fi

# First install: settings readable by root and the service only.
if [ -f /etc/exhale/exhale.env ]; then
	chown root:exhale /etc/exhale/exhale.env
	chmod 0640 /etc/exhale/exhale.env
fi

# Enabled (starts at boot) but not started: review the settings first.
if [ "$has_systemctl" = yes ]; then
	systemctl enable exhale.service >/dev/null 2>&1 || true
fi

cat <<'MSG'

Exhale is installed and will start at boot.

  Settings   /etc/exhale/exhale.env
  Data       /var/lib/exhale

Start it now:

  sudo systemctl start exhale
  sudo journalctl -u exhale -n 20     # the address and the one-time setup code

Then open http://127.0.0.1:8484 on this machine. To reach it from other
devices or over HTTPS, see:
  https://github.com/akamachinecode/exhale/tree/main/docs/install

MSG
