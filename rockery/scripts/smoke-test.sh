#!/usr/bin/env bash
# Smoke test against a running instance.
#   scripts/smoke-test.sh http://localhost:8080
#   scripts/smoke-test.sh "http://$(kubectl -n rockery get ingress rockery -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')"
set -euo pipefail

BASE="${1:?usage: smoke-test.sh <base-url>}"
fail() { echo "FAIL: $*" >&2; exit 1; }
code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }

[[ "$(code "$BASE/healthz")" == 200 ]] || fail "/healthz"
[[ "$(code "$BASE/readyz")"  == 200 ]] || fail "/readyz"
[[ "$(code "$BASE/rocks")"   == 200 ]] || fail "/rocks"

# Create a fresh rock so the test is repeatable, then adopt it.
loc="$(curl -s -D - -o /dev/null -X POST "$BASE/rocks" -H 'Content-Type: application/json' \
  -d '{"name":"Smokey","type":"pumice","weight_kg":0.2,"quirk":"Floats. Smug about it."}' \
  | tr -d '\r' | awk -F': ' 'tolower($1)=="location"{print $2}')"
[[ -n "$loc" ]] || fail "create rock returned no Location"

[[ "$(code -X POST "$BASE$loc/adopt" -H 'Content-Type: application/json' -d '{"adopter":"smoke-test"}')" == 200 ]] || fail "adopt"
[[ "$(code -X POST "$BASE$loc/adopt" -H 'Content-Type: application/json' -d '{"adopter":"intruder"}')"   == 409 ]] || fail "double-adopt should be 409"

echo "OK: smoke test passed against $BASE ($(curl -s "$BASE/version"))"
