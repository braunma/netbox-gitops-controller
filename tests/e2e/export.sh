#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Adoption by `netbox-gitops export`, against a real NetBox.
#
# The claim of the export is that merging what it writes changes nothing in
# NetBox but the tags: every object is declared under the identity it has,
# with the values it holds, so the sync adopts it. An in-memory fake cannot
# prove that — it agrees with whatever it is sent — so this seeds a NetBox
# "by hand" (through the API, no gitops tag), snapshots every object, exports,
# applies, snapshots again and compares every field of every object.
#
# Only for a THROWAWAY instance: it refuses to start unless NetBox is empty,
# and it deletes every object at the end, untagged ones included.
#
#   NETBOX_URL    base URL, e.g. http://127.0.0.1:8000
#   NETBOX_TOKEN  API token (v1 or v2)
#   E2E_KEEP      set to 1 to leave the work directory and the objects in place
#   E2E_WORK      where the binary, data and logs are written
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

: "${NETBOX_URL:?NETBOX_URL must be set}"
: "${NETBOX_TOKEN:?NETBOX_TOKEN must be set}"
WORK="${E2E_WORK:-$(mktemp -d)}"
BIN="${WORK}/netbox-gitops"
DIR="${WORK}/data"
FIX="python3 tests/e2e/export_fixture.py"
FAILURES=0

fail() { printf '  ✗ %-44s %s\n' "$1" "$2"; FAILURES=$((FAILURES+1)); }
ok()   { printf '  ✓ %-44s %s\n' "$1" "${2:-}"; }

go build -o "${BIN}" ./cmd/netbox-gitops/ || exit 1
rm -rf "${DIR}" && mkdir -p "${DIR}"

echo ">>> seeding a hand-made NetBox (no gitops tag anywhere)"
${FIX} seed || exit 1
export E2E_EXPORT_OWNS_INSTANCE=1
cleanup() {
  if [ "${E2E_KEEP:-}" = "1" ]; then echo ">>> kept: ${WORK}"; return; fi
  ${FIX} cleanup >/dev/null
  rm -rf "${WORK}"
}
trap cleanup EXIT
${FIX} snapshot "${WORK}/before.json" >/dev/null

echo ">>> export"
"${BIN}" export --data-dir "${DIR}" --detailed-exitcode >"${WORK}/export.log" 2>&1
rc=$?
if [ "${rc}" = "2" ]; then ok "export wrote files" "$(grep -c 'imported' "${WORK}/export.log") file(s)"; else fail "export wrote files" "exit ${rc}, see ${WORK}/export.log"; fi
${FIX} snapshot "${WORK}/after-export.json" >/dev/null
if ${FIX} compare "${WORK}/before.json" "${WORK}/after-export.json" >"${WORK}/cmp-export.txt"; then
  ok "export wrote nothing to NetBox"
else
  fail "export wrote nothing to NetBox" "$(tail -1 "${WORK}/cmp-export.txt")"
fi
for w in "10.0.0.20/24 is in the global table" "has 2 IP addresses" "platform"; do
  if grep -q "${w}" "${WORK}/export.log"; then ok "reported: ${w}"; else fail "reported: ${w}" "not in the report"; fi
done

echo ">>> checks on the exported YAML"
if go run ./cmd/yamlcheck "${DIR}/definitions" "${DIR}/inventory" >"${WORK}/yamlcheck.log" 2>&1; then
  ok "yamlcheck accepts the export"
else
  fail "yamlcheck accepts the export" "see ${WORK}/yamlcheck.log"
fi
if "${BIN}" validate --data-dir "${DIR}" >"${WORK}/validate.log" 2>&1; then
  ok "validate accepts it against NetBox"
else
  fail "validate accepts it against NetBox" "see ${WORK}/validate.log"
fi

echo ">>> plan (what go_plan shows in the merge request)"
"${BIN}" --data-dir "${DIR}" --dry-run --prune --output json >"${WORK}/plan.json" 2>"${WORK}/plan.log"
python3 - "${WORK}/plan.json" >"${WORK}/plan-check.txt" <<'PY'
import json, sys
plan = json.load(open(sys.argv[1]))
bad = [c for c in plan["changes"] if c["action"] in ("create", "delete")
       or (c["action"] == "update" and set(c.get("fields") or {}) - {"tags"})]
for c in bad:
    print(f'    {c["action"]} {c["endpoint"]} {c.get("object")} {json.dumps(c.get("fields"))[:200]}')
s = plan["summary"]
print(f'create={s.get("create",0)} update={s.get("update",0)} delete={s.get("delete",0)} other={len(bad)}')
sys.exit(1 if bad else 0)
PY
if [ $? -eq 0 ]; then ok "plan: only tag updates, no create/delete" "$(tail -1 "${WORK}/plan-check.txt")"; else fail "plan: only tag updates, no create/delete" "$(tail -1 "${WORK}/plan-check.txt")"; cat "${WORK}/plan-check.txt"; fi
${FIX} snapshot "${WORK}/after-plan.json" >/dev/null
if ${FIX} compare "${WORK}/before.json" "${WORK}/after-plan.json" >/dev/null; then ok "dry-run wrote nothing"; else fail "dry-run wrote nothing" "NetBox changed"; fi

echo ">>> apply (what go_apply does)"
if "${BIN}" --data-dir "${DIR}" --prune >"${WORK}/apply.log" 2>&1; then
  ok "apply succeeded" "$(grep -E 'Summary:' "${WORK}/apply.log" | sed 's/.*Summary: //')"
else
  fail "apply succeeded" "see ${WORK}/apply.log"
fi
${FIX} snapshot "${WORK}/after-apply.json" >/dev/null
${FIX} compare "${WORK}/before.json" "${WORK}/after-apply.json" >"${WORK}/cmp-apply.txt"
if [ $? -eq 0 ]; then
  ok "NetBox unchanged except the gitops tag" "$(tail -1 "${WORK}/cmp-apply.txt")"
else
  fail "NetBox unchanged except the gitops tag" "$(tail -1 "${WORK}/cmp-apply.txt")"
  head -40 "${WORK}/cmp-apply.txt"
fi
# Left out on purpose, so it must still be unmanaged.
${FIX} untagged "${WORK}/after-apply.json" ipam/ip-addresses address 10.0.0.20/24
case $? in
  0) ok "the address left out stays unmanaged" ;;
  *) fail "the address left out stays unmanaged" "it was adopted or is gone" ;;
esac

echo ">>> converged"
"${BIN}" --data-dir "${DIR}" --dry-run --prune --detailed-exitcode >"${WORK}/second.log" 2>&1
rc=$?
if [ "${rc}" = "0" ]; then ok "second dry-run has nothing to do"; else fail "second dry-run has nothing to do" "exit ${rc}: $(grep -E 'Plan:' "${WORK}/second.log" | sed 's/.*Plan: //')"; fi
"${BIN}" export --data-dir "${DIR}" --dry-run --detailed-exitcode >"${WORK}/reexport.log" 2>&1
rc=$?
if [ "${rc}" = "0" ]; then ok "a second export finds nothing left"; else fail "a second export finds nothing left" "exit ${rc}"; fi

echo
if [ "${FAILURES}" -eq 0 ]; then
  echo "✅ export: everything asserted holds"
else
  echo "❌ export: ${FAILURES} assertion(s) failed (logs: ${WORK}; E2E_KEEP=1 keeps them)"
  E2E_KEEP=1
fi
exit $(( FAILURES > 0 ))
