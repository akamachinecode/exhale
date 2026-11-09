#!/usr/bin/env bash
# Seeds a running Exhale server with realistic demo data (used for screenshots
# and manual testing). Usage: scripts/seed-demo.sh [base-url]
set -euo pipefail
B="${1:-http://127.0.0.1:8484}/api/v1"
post() { curl -sf -X POST "$B$1" -H 'content-type: application/json' -d "$2"; }
jqid() { python3 -c 'import json,sys; d=json.load(sys.stdin); print(eval("d"+sys.argv[1]))' "$1"; }

revolut=$(post /payment-methods '{"name":"Revolut","kind":"card","last4":"0937"}' | jqid '["paymentMethod"]["id"]')
visa=$(post /payment-methods '{"name":"Visa","kind":"card","last4":"4421","expiresOn":"2026-12-31"}' | jqid '["paymentMethod"]["id"]')
paypal=$(post /payment-methods '{"name":"PayPal","kind":"paypal"}' | jqid '["paymentMethod"]["id"]')
cat() { curl -sf "$B/household" | python3 -c 'import json,sys; print({c["name"]:c["id"] for c in json.load(sys.stdin)["categories"]}[sys.argv[1]])' "$1"; }
streaming=$(cat Streaming); music=$(cat Music); software=$(cat Software); cloud=$(cat "Cloud & Dev"); ai=$(cat AI); health=$(cat "Health & Fitness")

add() { post /subscriptions "$1" | jqid '["subscription"]["id"]'; }
add "{\"name\":\"Netflix Premium\",\"price\":\"19.99\",\"currency\":\"EUR\",\"interval\":1,\"unit\":\"month\",\"firstCharge\":\"2026-11-12\",\"categoryId\":\"$streaming\",\"paymentMethodId\":\"$visa\"}" >/dev/null
add "{\"name\":\"Spotify Family\",\"price\":\"17.99\",\"currency\":\"EUR\",\"interval\":1,\"unit\":\"month\",\"firstCharge\":\"2026-11-08\",\"categoryId\":\"$music\",\"paymentMethodId\":\"$revolut\"}" >/dev/null
add "{\"name\":\"iCloud+ 2 TB\",\"price\":\"9.99\",\"currency\":\"EUR\",\"interval\":1,\"unit\":\"month\",\"firstCharge\":\"2026-11-14\",\"categoryId\":\"$cloud\",\"paymentMethodId\":\"$revolut\"}" >/dev/null
add "{\"name\":\"GitHub Copilot\",\"price\":\"10.00\",\"currency\":\"USD\",\"interval\":1,\"unit\":\"month\",\"firstCharge\":\"2026-11-20\",\"categoryId\":\"$software\",\"paymentMethodId\":\"$visa\"}" >/dev/null
gpt=$(add "{\"name\":\"ChatGPT Plus\",\"price\":\"20.00\",\"currency\":\"USD\",\"interval\":1,\"unit\":\"month\",\"firstCharge\":\"2026-12-03\",\"categoryId\":\"$ai\",\"paymentMethodId\":\"$visa\"}")
add "{\"name\":\"Amazon Prime\",\"price\":\"49.90\",\"currency\":\"EUR\",\"interval\":1,\"unit\":\"year\",\"firstCharge\":\"2026-11-22\",\"categoryId\":\"$streaming\",\"paymentMethodId\":\"$revolut\"}" >/dev/null
add "{\"name\":\"Hetzner Cloud\",\"price\":\"4.51\",\"currency\":\"EUR\",\"interval\":1,\"unit\":\"month\",\"firstCharge\":\"2026-11-25\",\"categoryId\":\"$cloud\",\"paymentMethodId\":\"$paypal\"}" >/dev/null
add "{\"name\":\"Basic-Fit\",\"price\":\"29.99\",\"currency\":\"EUR\",\"interval\":4,\"unit\":\"week\",\"firstCharge\":\"2026-11-17\",\"categoryId\":\"$health\",\"paymentMethodId\":\"$revolut\"}" >/dev/null
add "{\"name\":\"1Password Families\",\"price\":\"59.88\",\"currency\":\"USD\",\"interval\":1,\"unit\":\"year\",\"firstCharge\":\"2027-01-03\",\"categoryId\":\"$software\",\"paymentMethodId\":\"$visa\"}" >/dev/null
# ChatGPT's November charge was due on the 3rd and is still unconfirmed.
chg=$(curl -sf "$B/subscriptions/$gpt" | jqid '["upcoming"][0]["id"]')
post /charges/move "{\"chargeId\":\"$chg\",\"newDate\":\"2026-11-03\",\"scope\":\"this\"}" >/dev/null
echo "seeded"
