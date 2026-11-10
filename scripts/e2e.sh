#!/bin/sh
# Runs the end-to-end browser test against a freshly built server with an
# empty, temporary database. Used by `mise run test:e2e` and by CI.
set -eu
cd "$(dirname "$0")/.."

port="${EXHALE_E2E_PORT:-18484}"
data="$(mktemp -d)"
bin="$data/exhale"
trap 'kill "$pid" 2>/dev/null || true; rm -rf "$data"' EXIT INT TERM

[ -d web/dist/assets ] || (cd web && pnpm install --frozen-lockfile && pnpm build)
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
EXHALE_E2E_URL="http://127.0.0.1:$port" node --test --test-concurrency=1 e2e/
