#!/bin/sh
# Runs before the package files are unpacked (deb preinst, rpm %pre).
# The service user must exist first, so /etc/exhale/exhale.env can belong to
# its group.
set -e

if ! getent group exhale >/dev/null 2>&1; then
	groupadd --system exhale
fi
if ! getent passwd exhale >/dev/null 2>&1; then
	useradd --system --gid exhale --home-dir /var/lib/exhale --no-create-home \
		--shell /usr/sbin/nologin --comment "Exhale subscription manager" exhale
fi
