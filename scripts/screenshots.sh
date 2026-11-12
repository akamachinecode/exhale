#!/bin/sh
# Retakes the screenshots of the website and the README (site/img) from the
# current code: builds the web UI and the server, starts it with an empty,
# temporary database and no channels, fills it with demo data and captures
# the overview on a desktop and a phone, light and dark.
# Used by `mise run screenshots`; run it whenever the app's look changes.
set -eu
cd "$(dirname "$0")/.."

# Never reach a real bot, Slack or assistant from here.
for v in $(env | sed -n 's/^\(EXHALE_[A-Z_]*\)=.*/\1/p'); do unset "$v"; done

port="${EXHALE_SHOTS_PORT:-18485}"
data="$(mktemp -d)"
bin="$data/exhale"
trap 'kill "$pid" 2>/dev/null || true; rm -rf "$data"' EXIT INT TERM

(cd web && pnpm install --frozen-lockfile && pnpm build)
go build -o "$bin" ./cmd/exhale

"$bin" --data-dir "$data/data" serve --listen "127.0.0.1:$port" --no-backups >"$data/server.log" 2>&1 &
pid=$!
i=0
until curl -sf "http://127.0.0.1:$port/healthz" >/dev/null; do
	i=$((i + 1))
	if [ "$i" -gt 50 ]; then
		echo "the server did not start:" >&2
		cat "$data/server.log" >&2
		exit 1
	fi
	sleep 0.2
done

cd web
node scripts/site-screenshots.mjs "http://127.0.0.1:$port" ../site/img
