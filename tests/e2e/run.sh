#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# End-to-end test: drive the built binary against a real NetBox and assert the
# properties that unit tests with an in-memory fake cannot see.
#
# Everything here is transport-agnostic: point NETBOX_URL / NETBOX_TOKEN at any
# disposable NetBox (a GitLab `services:` container, a staging instance, or one
# started by provision-local.sh) and run this.
#
#   NETBOX_URL    base URL, e.g. http://netbox:8080
#   NETBOX_TOKEN  API token. A NetBox 4.5+ "v2" token (nbt_<key>.<secret>) and
#                 an older v1 token both work; the client picks the header.
#   E2E_SEEDS     space-separated generator seeds (default: "1 2 3")
#   E2E_KEEP      set to 1 to leave the generated data in place for inspection
#
# The instance is left populated on failure so the state can be inspected.
# WARNING: this creates and prunes objects. Never point it at production.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

: "${NETBOX_URL:?NETBOX_URL must be set}"
: "${NETBOX_TOKEN:?NETBOX_TOKEN must be set}"
SEEDS="${E2E_SEEDS:-1 2 3}"
WORK="${E2E_WORK:-$(mktemp -d)}"
BIN="${WORK}/netbox-gitops"
FAILURES=0

log()  { printf '%s\n' "$*"; }
fail() { printf '  ✗ %-28s %s\n' "$1" "$2"; FAILURES=$((FAILURES+1)); }
ok()   { printf '  ✓ %-28s %s\n' "$1" "${2:-}"; }

auth_header() {
  case "${NETBOX_TOKEN}" in
    nbt_*) printf 'Authorization: Bearer %s' "${NETBOX_TOKEN}" ;;
    *)     printf 'Authorization: Token %s'  "${NETBOX_TOKEN}" ;;
  esac
}
api()   { curl -sS --noproxy '*' -H "$(auth_header)" "${NETBOX_URL}/api/$1"; }
count() { api "$1/?limit=1" | python3 -c 'import json,sys; print(json.load(sys.stdin)["count"])' 2>/dev/null || echo ERR; }
summary_of() { grep -oE '[0-9]+ created, [0-9]+ updated, [0-9]+ deleted, [0-9]+ unchanged' "$1" | tail -1; }

wait_for_netbox() {
  for _ in $(seq 1 60); do
    if [ "$(curl -sS --noproxy '*' -o /dev/null -w '%{http_code}' "${NETBOX_URL}/api/" 2>/dev/null)" != "000" ]; then
      return 0
    fi
    sleep 5
  done
  log "error: NetBox at ${NETBOX_URL} did not become reachable"
  return 1
}

# Remove every object this controller manages, so each seed starts clean.
# Only gitops-tagged objects are touched: an empty data directory declares
# nothing, so --prune treats all of them as orphans.
wipe() {
  local empty="${WORK}/empty"
  mkdir -p "${empty}/definitions" "${empty}/inventory"
  "${BIN}" --data-dir "${empty}" --prune >"${WORK}/wipe.log" 2>&1 || true
}

log "==> building"
go build -o "${BIN}" ./cmd/netbox-gitops/ || { log "build failed"; exit 1; }
wait_for_netbox || exit 1
log "==> NetBox: $(api status/ | python3 -c 'import json,sys; print(json.load(sys.stdin)["netbox-version"])' 2>/dev/null || echo unknown)"

for seed in ${SEEDS}; do
  DIR="${WORK}/data-${seed}"
  log "==> seed ${seed}"
  python3 tests/e2e/gen.py "${DIR}" "${seed}" >/dev/null || { fail "generate" "seed ${seed}"; continue; }
  wipe

  # 1. every generated dataset must pass model validation
  if go run ./cmd/yamlcheck "${DIR}" >"${WORK}/yamlcheck-${seed}.log" 2>&1; then
    ok "yamlcheck"
  else
    fail "yamlcheck" "$(grep -iE 'error|✗' "${WORK}/yamlcheck-${seed}.log" | head -1)"
  fi

  # 2. a dry-run against an empty NetBox must plan without writing anything
  "${BIN}" --data-dir "${DIR}" --dry-run >"${WORK}/dryrun-${seed}.log" 2>&1
  rc=$?
  if [ "${rc}" -le 2 ]; then ok "dry-run"; else
    fail "dry-run" "exit ${rc}: $(grep -iE '✗|Error:' "${WORK}/dryrun-${seed}.log" | head -1)"
  fi
  if [ "$(count dcim/devices)" = "0" ]; then ok "dry-run wrote nothing"; else
    fail "dry-run wrote nothing" "objects appeared during a dry run"
  fi

  # 3. apply
  if "${BIN}" --data-dir "${DIR}" >"${WORK}/apply1-${seed}.log" 2>&1; then
    ok "apply" "$(summary_of "${WORK}/apply1-${seed}.log")"
  else
    fail "apply" "$(grep -iE '✗|Error:' "${WORK}/apply1-${seed}.log" | head -1)"
    continue
  fi

  # 4. THE invariant: a second apply must have nothing to do
  "${BIN}" --data-dir "${DIR}" >"${WORK}/apply2-${seed}.log" 2>&1
  s2="$(summary_of "${WORK}/apply2-${seed}.log")"
  if echo "${s2}" | grep -qE '^0 created, 0 updated, 0 deleted'; then
    ok "idempotent" "${s2}"
  else
    fail "idempotent" "${s2}"
    grep -E 'Creating|Updating' "${WORK}/apply2-${seed}.log" | head -5
  fi

  # 5. drift detection must agree that the instance is in sync
  "${BIN}" --data-dir "${DIR}" --dry-run --detailed-exitcode >/dev/null 2>&1
  [ $? -eq 0 ] && ok "drift exit code" || fail "drift exit code" "expected 0 on a converged instance"

  # 6. pruning a converged instance must delete nothing
  "${BIN}" --data-dir "${DIR}" --prune >"${WORK}/prune-${seed}.log" 2>&1
  sp="$(summary_of "${WORK}/prune-${seed}.log")"
  if echo "${sp}" | grep -qE ', 0 deleted,'; then ok "prune deletes nothing" "${sp}"; else
    fail "prune deletes nothing" "${sp}"
  fi
done

# 7. prune must reap an orphan while leaving unmanaged objects alone
log "==> prune semantics"
api dcim/sites/ >/dev/null 2>&1
curl -sS --noproxy '*' -X POST -H "$(auth_header)" -H 'Content-Type: application/json' \
  -d '{"name":"e2e-unmanaged","slug":"e2e-unmanaged"}' "${NETBOX_URL}/api/dcim/sites/" >/dev/null
wipe
if [ "$(api 'dcim/sites/?slug=e2e-unmanaged' | python3 -c 'import json,sys; print(json.load(sys.stdin)["count"])')" = "1" ]; then
  ok "unmanaged object survives"
else
  fail "unmanaged object survives" "prune deleted an object it does not manage"
fi
sid=$(api 'dcim/sites/?slug=e2e-unmanaged' | python3 -c 'import json,sys; r=json.load(sys.stdin)["results"]; print(r[0]["id"] if r else "")')
[ -n "${sid}" ] && curl -sS --noproxy '*' -X DELETE -H "$(auth_header)" "${NETBOX_URL}/api/dcim/sites/${sid}/" >/dev/null

if [ "${E2E_KEEP:-0}" != "1" ]; then rm -rf "${WORK}"; else log "==> kept: ${WORK}"; fi
if [ "${FAILURES}" -gt 0 ]; then log "==> FAILED (${FAILURES})"; exit 1; fi
log "==> all checks passed"
