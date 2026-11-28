#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Change-based sync, against a real NetBox and a real git repository.
#
# A sync that only looks at what changed is fast because of everything it does
# not do, so each thing it leaves out has to be shown to be safe to leave out,
# and each case in which it must not be trusted has to be shown to end in a
# full sync:
#
#   - the first sync is full and records its commit in NetBox
#   - an unchanged repository costs a handful of requests and changes nothing
#   - one edited device selects that device and no other
#   - a changed device type selects the devices built from it
#   - an edit made in NetBox by hand survives a change-based sync and is
#     reverted by --full
#   - a removed device survives a change-based sync, even with --prune, is
#     announced, and is deleted by --full --prune
#   - a hand-scoped run, an uncommitted edit and an unknown commit each make
#     the next sync a full one
#
#   NETBOX_URL    base URL, e.g. http://netbox:8080
#   NETBOX_TOKEN  API token (v1 or v2)
#   E2E_KEEP      set to 1 to leave the work directory in place for inspection
#   E2E_WORK      where the binary, the repository and the logs are written
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

: "${NETBOX_URL:?NETBOX_URL must be set}"
: "${NETBOX_TOKEN:?NETBOX_TOKEN must be set}"
WORK="${E2E_WORK:-$(mktemp -d)}"
BIN="${WORK}/netbox-gitops"
DIR="${WORK}/repo"
FAILURES=0

fail() { printf '  ✗ %-44s %s\n' "$1" "$2"; FAILURES=$((FAILURES+1)); }
ok()   { printf '  ✓ %-44s %s\n' "$1" "${2:-}"; }

auth_header() {
  case "${NETBOX_TOKEN}" in
    nbt_*) printf 'Authorization: Bearer %s' "${NETBOX_TOKEN}" ;;
    *)     printf 'Authorization: Token %s'  "${NETBOX_TOKEN}" ;;
  esac
}
api() { curl -sS --noproxy '*' -H "$(auth_header)" "${NETBOX_URL}/api/$1"; }
field() { api "$1" | python3 -c '
import json,sys
r = json.load(sys.stdin)["results"]
print(r[0].get(sys.argv[1], "") if len(r) == 1 else "")' "$2"; }
count() { api "$1" | python3 -c 'import json,sys; print(json.load(sys.stdin)["count"])'; }

# sync <log name> [flags...]: run against the test repository.
sync() { local log="$1"; shift; "${BIN}" --data-dir "${DIR}" "$@" >"${WORK}/${log}.log" 2>&1; }
git_() { git -C "${DIR}" -c user.name=e2e -c user.email=e2e@example.com "$@"; }
commit() { git_ add -A && git_ commit -q -m "$1"; }

# expect <label> <log name> <pattern>: the log has a line matching the pattern.
expect() {
  if grep -qE -- "$3" "${WORK}/$2.log"; then ok "$1"; else fail "$1" "no line matching /$3/ in $2.log"; fi
}
# refute <label> <log name> <pattern>
refute() {
  if grep -qE -- "$3" "${WORK}/$2.log"; then fail "$1" "unexpected line matching /$3/ in $2.log"; else ok "$1"; fi
}
requests() { grep -oE 'NetBox API requests: [0-9]+' "${WORK}/$1.log" | grep -oE '[0-9]+$'; }
state() { field "extras/tags/?slug=gitops-state" description; }
serial() { field "dcim/devices/?name=$1" serial; }

wipe() {
  local empty="${WORK}/empty"
  mkdir -p "${empty}/definitions" "${empty}/inventory"
  "${BIN}" --data-dir "${empty}" --full --prune >"${WORK}/wipe.log" 2>&1 || true
  # Tags are never pruned, so the state tag of an earlier run is still there.
  local tag
  tag="$(field "extras/tags/?slug=gitops-state" id)"
  [ -n "${tag}" ] && curl -sS --noproxy '*' -X DELETE -H "$(auth_header)" \
    "${NETBOX_URL}/api/extras/tags/${tag}/" >/dev/null
}

echo "==> building"
go build -o "${BIN}" ./cmd/netbox-gitops/ || { echo "build failed"; exit 1; }
echo "==> NetBox: $(api status/ | python3 -c 'import json,sys; print(json.load(sys.stdin)["netbox-version"])' 2>/dev/null || echo unknown)"
wipe

# The bundled example data, as a repository of its own.
mkdir -p "${DIR}"
cp -r example/definitions example/inventory "${DIR}/"
git -C "${DIR}" init -q
commit "initial"
SERVERS="${DIR}/inventory/hardware/active/servers.yaml"

# ---------------------------------------------------------------------------
echo "==> first sync"
sync first || fail "first sync" "exit $?"
expect "first sync is full"                 first 'Scope: full sync — NetBox holds no record'
expect "first sync records its commit"      first 'Recorded in NetBox'
head="$(git_ rev-parse HEAD)"
case "$(state)" in
  "commit=${head} "*) ok "state tag names the commit" ;;
  *) fail "state tag names the commit" "got: $(state)" ;;
esac
full_requests="$(requests first)"

# ---------------------------------------------------------------------------
echo "==> nothing changed"
sync noop --prune --detailed-exitcode; rc=$?
[ "${rc}" = "0" ] && ok "unchanged repository exits 0" || fail "unchanged repository exits 0" "exit ${rc}"
expect "scope is change-based"              noop "Scope: changes since ${head:0:10}"
expect "nothing selected"                   noop 'Nothing declared was added or changed'
n="$(requests noop)"
if [ "${n:-999}" -le 5 ]; then ok "an unchanged repository is cheap" "${n} requests (full: ${full_requests})"
else fail "an unchanged repository is cheap" "${n} requests"; fi

# ---------------------------------------------------------------------------
echo "==> one device edited"
python3 - "${SERVERS}" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
s = s.replace('  - name: "example-server-01"\n', '  - name: "example-server-01"\n    serial: "E2E-CHANGED"\n', 1)
open(p, "w").write(s)
PY
commit "serial for example-server-01"
sync plan-one --dry-run
expect "plan selects the edited device"     plan-one '• device example-server-01: changed: serial'
expect "plan selects one device only"       plan-one 'Reconciling 1 of [0-9]+ devices'
expect "plan skips the foundation"          plan-one 'Skipping phase 1 \(foundation\): nothing in it changed'
[ -z "$(serial example-server-01)" ] && ok "a plan writes nothing" || fail "a plan writes nothing" "serial already set"
case "$(state)" in
  "commit=${head} "*) ok "a plan leaves the state alone" ;;
  *) fail "a plan leaves the state alone" "got: $(state)" ;;
esac

sync apply-one || fail "apply" "exit $?"
[ "$(serial example-server-01)" = "E2E-CHANGED" ] && ok "the edit reached NetBox" || fail "the edit reached NetBox" "serial=$(serial example-server-01)"
n="$(requests apply-one)"
if [ "${n:-999}" -lt "${full_requests}" ]; then ok "fewer requests than a full sync" "${n} vs ${full_requests}"
else fail "fewer requests than a full sync" "${n} vs ${full_requests}"; fi
head="$(git_ rev-parse HEAD)"
case "$(state)" in
  "commit=${head} "*) ok "state advanced to the new commit" ;;
  *) fail "state advanced to the new commit" "got: $(state)" ;;
esac

# ---------------------------------------------------------------------------
# A rack and a module type have a slug only in the YAML; the phases that
# reconcile them register it. Here those phases are skipped, and the device
# phase has to resolve both from what NetBox already holds.
echo "==> a rack move and a module edit, earlier phases skipped"
python3 - "${SERVERS}" "${DIR}/inventory/hardware/active/gpu-servers.yaml" <<'PY'
import sys
servers, gpu = sys.argv[1], sys.argv[2]
s = open(servers).read()
assert '    position: 12\n' in s
open(servers, "w").write(s.replace('    position: 12\n', '    position: 12\n    rack: "rack-a02"\n', 1))
s = open(gpu).read()
assert 'serial: "GPU-CARD-003"' in s
open(gpu, "w").write(s.replace('serial: "GPU-CARD-003"', 'serial: "E2E-MODULE"', 1))
PY
commit "move example-server-02, edit a module"
sync apply-rack || fail "apply" "exit $?"
expect "the foundation is skipped"          apply-rack 'Skipping phase 1 \(foundation\)'
refute "the rack resolves"                  apply-rack 'Rack .* not found'
refute "the module type resolves"           apply-rack 'Module type .* not found'
expect "the run is recorded"                apply-rack 'Recorded in NetBox'
rack="$(api "dcim/devices/?name=example-server-02" | python3 -c 'import json,sys; r=json.load(sys.stdin)["results"][0]["rack"]; print(r["name"] if r else "")')"
[ "${rack}" = "Rack A-02" ] && ok "the device is in its new rack" || fail "the device is in its new rack" "rack=${rack}"
n="$(count "dcim/modules/?serial=E2E-MODULE")"
[ "${n}" = "1" ] && ok "the module edit reached NetBox" || fail "the module edit reached NetBox" "${n} modules with the new serial"
head="$(git_ rev-parse HEAD)"

# ---------------------------------------------------------------------------
echo "==> an edit made in NetBox by hand"
dev="$(field "dcim/devices/?name=example-server-02" id)"
curl -sS --noproxy '*' -X PATCH -H "$(auth_header)" -H 'Content-Type: application/json' \
  -d '{"serial":"BY-HAND"}' "${NETBOX_URL}/api/dcim/devices/${dev}/" >/dev/null
# Declare a serial for it, so the full sync has something to put back.
python3 - "${SERVERS}" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
s = s.replace('  - name: "example-server-02"\n', '  - name: "example-server-02"\n    serial: "DECLARED"\n', 1)
open(p, "w").write(s)
PY
commit "serial for example-server-02"
sync apply-two || fail "apply" "exit $?"
curl -sS --noproxy '*' -X PATCH -H "$(auth_header)" -H 'Content-Type: application/json' \
  -d '{"serial":"BY-HAND"}' "${NETBOX_URL}/api/dcim/devices/${dev}/" >/dev/null
sync drift-changed
[ "$(serial example-server-02)" = "BY-HAND" ] && ok "a change-based sync does not see drift" || fail "a change-based sync does not see drift" "serial=$(serial example-server-02)"
sync drift-full --full || fail "full sync" "exit $?"
expect "--full is a full sync"              drift-full 'Scope: full sync — --full was given'
[ "$(serial example-server-02)" = "DECLARED" ] && ok "--full reverts the hand edit" || fail "--full reverts the hand edit" "serial=$(serial example-server-02)"

# ---------------------------------------------------------------------------
echo "==> a changed device type"
TYPE="${DIR}/definitions/device_types/example-server.yaml"
python3 - "${TYPE}" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
if "comments:" in s:
    import re
    s = re.sub(r'comments:.*', 'comments: "e2e changed"', s, count=1)
else:
    s = s.rstrip("\n") + '\ncomments: "e2e changed"\n'
open(p, "w").write(s)
PY
commit "device type comment"
sync plan-type --dry-run
if grep -qE '• device type .*: (changed|new)' "${WORK}/plan-type.log"; then
  ok "the device type is selected"
  expect "its devices are selected with it" plan-type '• device example-server-0[12]: its device type .* changed'
  refute "other devices are not"            plan-type '• device example-switch-01'
else
  fail "the device type is selected" "see plan-type.log (did the fixture edit apply?)"
fi
sync apply-type || fail "apply" "exit $?"

# ---------------------------------------------------------------------------
echo "==> a removed device"
python3 - "${SERVERS}" <<'PY'
import sys, re
p = sys.argv[1]
s = open(p).read()
i = s.index('  - name: "example-server-02"')
m = re.search(r'\n  - name: ', s[i + 1:])
s = s[:i] + (s[i + 1 + m.start() + 1:] if m else "")
open(p, "w").write(s)
PY
commit "remove example-server-02"
sync removed-changed --prune || fail "apply" "exit $?"
expect "the removal is announced"           removed-changed '✗ device example-server-02'
expect "prune is left to a full sync"       removed-changed 'Prune: left to the next full sync'
[ "$(count "dcim/devices/?name=example-server-02")" = "1" ] && ok "a change-based sync deletes nothing" || fail "a change-based sync deletes nothing" "device is gone"
sync removed-full --full --prune || fail "full sync" "exit $?"
[ "$(count "dcim/devices/?name=example-server-02")" = "0" ] && ok "--full --prune deletes it" || fail "--full --prune deletes it" "device still there"

# ---------------------------------------------------------------------------
echo "==> runs after which the record cannot be trusted"
sync scoped --device example-server-01 || fail "scoped run" "exit $?"
case "$(state)" in
  commit=*) fail "a hand-scoped run withdraws the record" "got: $(state)" ;;
  *) ok "a hand-scoped run withdraws the record" ;;
esac
sync after-scoped
expect "the next sync is full"              after-scoped 'Scope: full sync — the last sync did not finish, or its scope was limited by hand'
expect "and records again"                  after-scoped 'Recorded in NetBox'

python3 - "${SERVERS}" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
open(p, "w").write(s.replace('E2E-CHANGED', 'E2E-UNCOMMITTED', 1))
PY
sync dirty || fail "sync of an uncommitted edit" "exit $?"
[ "$(serial example-server-01)" = "E2E-UNCOMMITTED" ] && ok "an uncommitted edit is applied" || fail "an uncommitted edit is applied" "serial=$(serial example-server-01)"
expect "but not recorded"                   dirty 'Sync state not recorded: the data directory has uncommitted changes'
git_ checkout -q -- .
sync after-dirty
expect "the next sync is full"              after-dirty 'Scope: full sync'
[ "$(serial example-server-01)" = "E2E-CHANGED" ] && ok "and puts the committed value back" || fail "and puts the committed value back" "serial=$(serial example-server-01)"

# A NetBox last synced from a commit this clone has never seen.
tag="$(field "extras/tags/?slug=gitops-state" id)"
curl -sS --noproxy '*' -X PATCH -H "$(auth_header)" -H 'Content-Type: application/json' \
  -d "{\"description\":\"$(state | sed -E 's/commit=[0-9a-f]+/commit=0123456789abcdef0123456789abcdef01234567/')\"}" \
  "${NETBOX_URL}/api/extras/tags/${tag}/" >/dev/null
sync unknown-commit --dry-run
expect "an unknown commit means a full sync" unknown-commit 'Scope: full sync — commit 0123456789, which NetBox was last synced to, is not in this clone'

wipe
echo
if [ "${FAILURES}" -eq 0 ]; then
  echo "==> all change-based sync checks passed"
else
  echo "==> ${FAILURES} change-based sync check(s) failed (work dir: ${WORK})"
  exit 1
fi
[ "${E2E_KEEP:-0}" = "1" ] || rm -rf "${WORK}"
