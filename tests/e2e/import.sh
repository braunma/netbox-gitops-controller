#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# End-to-end test for the reverse sync (`netbox-gitops import`): populate a real
# NetBox, import it into a fresh directory, and prove the round trip closes —
# the imported YAML is valid, applies back with nothing to do, and reproduces
# the same objects.
#
#   NETBOX_URL    base URL, e.g. http://netbox:8080
#   NETBOX_TOKEN  API token (v1 or v2)
#   E2E_SEEDS     generator seeds (default: "1 2")
#
# WARNING: this creates and prunes objects. Never point it at production.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

: "${NETBOX_URL:?NETBOX_URL must be set}"
: "${NETBOX_TOKEN:?NETBOX_TOKEN must be set}"
SEEDS="${E2E_SEEDS:-1 2}"
WORK="${E2E_WORK:-$(mktemp -d)}"
BIN="${WORK}/netbox-gitops"
FAILURES=0

log()  { printf '%s\n' "$*"; }
fail() { printf '  ✗ %-30s %s\n' "$1" "$2"; FAILURES=$((FAILURES+1)); }
ok()   { printf '  ✓ %-30s %s\n' "$1" "${2:-}"; }

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
    [ "$(curl -sS --noproxy '*' -o /dev/null -w '%{http_code}' "${NETBOX_URL}/api/" 2>/dev/null)" != "000" ] && return 0
    sleep 5
  done
  log "error: NetBox at ${NETBOX_URL} did not become reachable"; return 1
}

wipe() {
  local empty="${WORK}/empty"
  mkdir -p "${empty}/definitions" "${empty}/inventory"
  "${BIN}" --data-dir "${empty}" --prune >"${WORK}/wipe.log" 2>&1 || true
}

log "==> building"
go build -o "${BIN}" ./cmd/netbox-gitops/ || { log "build failed"; exit 1; }
wait_for_netbox || exit 1

for seed in ${SEEDS}; do
  DIR="${WORK}/data-${seed}"
  OUT="${WORK}/import-${seed}"
  log "==> seed ${seed}"
  python3 tests/e2e/gen.py "${DIR}" "${seed}" >"${WORK}/gen-${seed}.json" || { fail "generate" "seed ${seed}"; continue; }

  wipe
  # Populate NetBox from the generated dataset (the forward sync).
  if "${BIN}" --data-dir "${DIR}" >"${WORK}/seed-apply-${seed}.log" 2>&1; then
    ok "seed apply" "$(summary_of "${WORK}/seed-apply-${seed}.log")"
  else
    fail "seed apply" "$(grep -iE '✗|Error:' "${WORK}/seed-apply-${seed}.log" | head -1)"; continue
  fi

  # Record object counts before import, to compare after the round trip.
  before_devices="$(count dcim/devices)"
  before_vlans="$(count ipam/vlans)"
  before_vms="$(count virtualization/virtual-machines)"

  # 1. import into a fresh directory
  if "${BIN}" import --data-dir "${OUT}" --report "${WORK}/report-${seed}.md" >"${WORK}/import-${seed}.log" 2>&1; then
    ok "import" "$(ls -1 "${OUT}"/definitions "${OUT}"/inventory 2>/dev/null | wc -l) top-level files"
  else
    fail "import" "$(grep -iE '✗|Error:' "${WORK}/import-${seed}.log" | head -1)"; continue
  fi

  # 2. the imported YAML must pass strict model validation
  if go run ./cmd/yamlcheck "${OUT}/definitions" "${OUT}/inventory" --strict >"${WORK}/yamlcheck-${seed}.log" 2>&1; then
    ok "yamlcheck --strict"
  else
    fail "yamlcheck --strict" "$(grep -iE 'error|✗' "${WORK}/yamlcheck-${seed}.log" | head -1)"
  fi

  # 3. adopt: the first sync of the imported data writes only tags. It must
  #    succeed and, being over an already-populated instance, create nothing.
  "${BIN}" --data-dir "${OUT}" --adopt >"${WORK}/adopt-${seed}.log" 2>&1
  sa="$(summary_of "${WORK}/adopt-${seed}.log")"
  if echo "${sa}" | grep -qE '^0 created,'; then
    ok "adopt creates nothing" "${sa}"
  else
    fail "adopt creates nothing" "${sa}"
  fi

  # 4. THE invariant: a plain second apply of the imported data has nothing to do
  "${BIN}" --data-dir "${OUT}" >"${WORK}/reapply-${seed}.log" 2>&1
  sr="$(summary_of "${WORK}/reapply-${seed}.log")"
  if echo "${sr}" | grep -qE '^0 created, 0 updated, 0 deleted'; then
    ok "re-apply idempotent" "${sr}"
  else
    fail "re-apply idempotent" "${sr}"
    grep -E 'Creating|Updating' "${WORK}/reapply-${seed}.log" | head -5
  fi

  # 5. drift detection agrees the imported data is in sync
  "${BIN}" --data-dir "${OUT}" --dry-run --detailed-exitcode >/dev/null 2>&1
  [ $? -eq 0 ] && ok "drift exit code" || fail "drift exit code" "expected 0 on a converged instance"

  # 6. object counts unchanged across the round trip
  if [ "$(count dcim/devices)" = "${before_devices}" ] &&
     [ "$(count ipam/vlans)" = "${before_vlans}" ] &&
     [ "$(count virtualization/virtual-machines)" = "${before_vms}" ]; then
    ok "counts unchanged" "devices=${before_devices} vlans=${before_vlans} vms=${before_vms}"
  else
    fail "counts unchanged" "devices ${before_devices}->$(count dcim/devices), vlans ${before_vlans}->$(count ipam/vlans), vms ${before_vms}->$(count virtualization/virtual-machines)"
  fi

  # 7. a second import of the unchanged instance is byte-identical (determinism)
  OUT2="${WORK}/import2-${seed}"
  "${BIN}" import --data-dir "${OUT2}" --report - >/dev/null 2>&1
  if diff -r "${OUT}/definitions" "${OUT2}/definitions" >/dev/null 2>&1 &&
     diff -r "${OUT}/inventory" "${OUT2}/inventory" >/dev/null 2>&1; then
    ok "re-import is deterministic"
  else
    fail "re-import is deterministic" "a second import differs"
  fi
done

log ""
if [ "${FAILURES}" -eq 0 ]; then
  log "All import round-trip checks passed."
else
  log "${FAILURES} check(s) failed. Work dir left at ${WORK} for inspection."
fi
exit $(( FAILURES > 0 ? 1 : 0 ))
