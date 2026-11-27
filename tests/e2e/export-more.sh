#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Adoption by `netbox-gitops export` on more, and more varied, data than
# export.sh seeds by hand:
#
#   variety  every seed in E2E_EXPORT_SEEDS: a random inventory from gen.py
#            (chassis with blades, patch panels with cable paths, modules,
#            power ports, VMs, clusters, both device type formats) is applied
#            by the controller, then the gitops tag is taken off everything —
#            a NetBox someone filled by hand, in shapes the controller itself
#            produces.
#   volume   E2E_EXPORT_SCALE servers per site at four sites, created in bulk
#            through the API, with a VM per server.
#
# For each: snapshot every field of every object, export into an empty data
# directory, check it, plan, apply, snapshot again and compare. Only the
# gitops tag may change; a second dry-run and a second export must have
# nothing to do.
#
# Only for a THROWAWAY instance: it refuses to start unless NetBox is empty,
# and it deletes every object between datasets and at the end.
#
#   NETBOX_URL, NETBOX_TOKEN
#   E2E_EXPORT_SEEDS  default "1 2 3 4 5 6 7 8 9 10"; set it empty for none
#   E2E_EXPORT_SCALE  servers per site for the volume run; default 100, 0 skips it
#   E2E_KEEP=1        keep the work directory and the objects of a failing run
#   E2E_WORK          where binaries, data and logs go
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

: "${NETBOX_URL:?NETBOX_URL must be set}"
: "${NETBOX_TOKEN:?NETBOX_TOKEN must be set}"
WORK="${E2E_WORK:-$(mktemp -d)}"
BIN="${WORK}/netbox-gitops"
FIX="python3 tests/e2e/export_fixture.py"
SEEDS="${E2E_EXPORT_SEEDS-1 2 3 4 5 6 7 8 9 10}"  # empty: no seeds
SCALE="${E2E_EXPORT_SCALE:-100}"
FAILURES=0

fail() { printf '  ✗ %-40s %s\n' "$1" "$2"; FAILURES=$((FAILURES+1)); }
ok()   { printf '  ✓ %-40s %s\n' "$1" "${2:-}"; }

mkdir -p "${WORK}"
go build -o "${BIN}" ./cmd/netbox-gitops/ || exit 1
python3 -c 'import sys; sys.path.insert(0, "tests/e2e"); import export_fixture as f; f.require_empty()' || exit 1
export E2E_EXPORT_OWNS_INSTANCE=1

# adopt <name> <dir>: what export.sh asserts, for whatever NetBox holds now.
adopt() {
  local name="$1" w="${WORK}/$1" rc t0 t1
  local data="${w}/export"
  rm -rf "${data}" && mkdir -p "${data}"
  ${FIX} snapshot "${w}/before.json" >"${w}/count.txt"
  # Nothing may be managed yet, or this tests a re-sync, not an adoption.
  local managed
  managed="$(${FIX} managed-count "${w}/before.json")"
  if [ "${managed}" = "0" ]; then
    ok "${name}: NetBox holds, none managed" "$(cat "${w}/count.txt")"
  else
    fail "${name}: NetBox holds, none managed" "${managed} object(s) still carry the gitops tag"; return
  fi

  t0=$(date +%s)
  "${BIN}" export --data-dir "${data}" --detailed-exitcode >"${w}/export.log" 2>&1
  rc=$?; t1=$(date +%s)
  if [ "${rc}" = "2" ]; then
    ok "${name}: export" "$(grep -cE '^  (definitions|inventory)/' "${w}/export.log") files, $((t1-t0))s, $(grep -cE '^  - ' "${w}/export.log") report line(s)"
  else
    fail "${name}: export" "exit ${rc}"; tail -5 "${w}/export.log"; return
  fi

  if go run ./cmd/yamlcheck "${data}/definitions" "${data}/inventory" >"${w}/yamlcheck.log" 2>&1; then
    ok "${name}: yamlcheck"
  else
    fail "${name}: yamlcheck" "$(grep -m3 '❌' "${w}/yamlcheck.log" | tr '\n' ' ')"
  fi

  "${BIN}" --data-dir "${data}" --dry-run --prune --output json >"${w}/plan.json" 2>"${w}/plan.log"
  if python3 - "${w}/plan.json" >"${w}/plan-check.txt" <<'PY'
import json, sys
plan = json.load(open(sys.argv[1]))
bad = [c for c in plan["changes"] if c["action"] in ("create", "delete")
       or (c["action"] == "update" and set(c.get("fields") or {}) - {"tags"})]
for c in bad[:20]:
    print(f'    {c["action"]} {c["endpoint"]} {c.get("object")} {json.dumps(c.get("fields"))[:200]}')
s = plan["summary"]
print(f'update={s.get("update",0)} unchanged={s.get("unchanged",0)} other={len(bad)}')
sys.exit(1 if bad else 0)
PY
  then ok "${name}: plan is tag updates only" "$(tail -1 "${w}/plan-check.txt")"
  else fail "${name}: plan is tag updates only" "$(tail -1 "${w}/plan-check.txt")"; head -20 "${w}/plan-check.txt"
  fi

  t0=$(date +%s)
  if "${BIN}" --data-dir "${data}" --prune >"${w}/apply.log" 2>&1; then
    t1=$(date +%s)
    ok "${name}: apply" "$(grep -E 'Summary:' "${w}/apply.log" | sed 's/.*Summary: //'), $((t1-t0))s"
  else
    fail "${name}: apply" "$(grep -m3 -E 'ERROR|✗|Error' "${w}/apply.log" | tr '\n' ' ')"
  fi
  ${FIX} snapshot "${w}/after.json" >/dev/null
  if ${FIX} compare "${w}/before.json" "${w}/after.json" >"${w}/compare.txt"; then
    ok "${name}: no field changed but tags" "$(tail -1 "${w}/compare.txt")"
  else
    fail "${name}: no field changed but tags" "$(tail -1 "${w}/compare.txt")"; head -25 "${w}/compare.txt"
  fi

  "${BIN}" --data-dir "${data}" --dry-run --prune --detailed-exitcode >"${w}/second.log" 2>&1
  rc=$?
  if [ "${rc}" = "0" ]; then ok "${name}: converged"; else fail "${name}: converged" "exit ${rc}: $(grep -E 'Plan:' "${w}/second.log" | sed 's/.*Plan: //')"; fi
  "${BIN}" export --data-dir "${data}" --dry-run --detailed-exitcode >"${w}/reexport.log" 2>&1
  rc=$?
  if [ "${rc}" = "0" ]; then ok "${name}: nothing left to export"; else fail "${name}: nothing left to export" "exit ${rc}"; fi
}

for seed in ${SEEDS}; do
  echo ">>> variety: seed ${seed}"
  w="${WORK}/seed-${seed}"; mkdir -p "${w}"
  ${FIX} cleanup >/dev/null
  python3 tests/e2e/gen.py "${w}/gen" "${seed}" >"${w}/gen.json" || { fail "seed ${seed}: generate" ""; continue; }
  if ! "${BIN}" --data-dir "${w}/gen" >"${w}/seed-apply.log" 2>&1; then
    fail "seed ${seed}: build the instance" "see ${w}/seed-apply.log"; continue
  fi
  if ! ${FIX} untag >"${w}/untag.txt" 2>&1; then
    fail "seed ${seed}: untag" "$(tail -1 "${w}/untag.txt")"; continue
  fi
  adopt "seed-${seed}"
done

if [ "${SCALE}" -gt 0 ]; then
  echo ">>> volume: ${SCALE} servers per site, 4 sites"
  w="${WORK}/scale"; mkdir -p "${w}"
  ${FIX} cleanup >/dev/null
  t0=$(date +%s)
  if ${FIX} seed-scale "${SCALE}" >"${w}/seed.log" 2>&1; then
    ok "scale: seeded" "$(tail -1 "${w}/seed.log"), $(( $(date +%s) - t0 ))s"
    adopt "scale"
  else
    fail "scale: seeded" "$(tail -3 "${w}/seed.log" | tr '\n' ' ')"
  fi
fi

echo
if [ "${FAILURES}" -eq 0 ]; then
  echo "✅ export (more data): everything asserted holds"
  [ "${E2E_KEEP:-}" = "1" ] || { ${FIX} cleanup >/dev/null; rm -rf "${WORK}"; }
else
  echo "❌ export (more data): ${FAILURES} assertion(s) failed (logs and objects kept: ${WORK})"
fi
exit $(( FAILURES > 0 ))
