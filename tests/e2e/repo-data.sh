#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Apply the repository's own committed sample data to a real NetBox and assert
# that it converges — and that the conventions it is written in mean what the
# README says they mean.
#
# run.sh proves the controller handles randomly generated inventories. This
# proves the inventory *in this repository* is correct, which is a different
# claim: the sample data leans on conventions whose failure mode is silent.
# A cable declared on one end only either wires both ends or quietly wires
# none. An interface listed without a `type` either matches a template port or
# creates a second, typeless one beside it. A device type slug that matches no
# definition skips the device with a warning and exits 0. None of those show up
# as an error, so each is asserted here against the object NetBox actually
# holds.
#
#   NETBOX_URL    base URL, e.g. http://netbox:8080
#   NETBOX_TOKEN  API token (v1 or v2)
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

: "${NETBOX_URL:?NETBOX_URL must be set}"
: "${NETBOX_TOKEN:?NETBOX_TOKEN must be set}"
WORK="${E2E_WORK:-$(mktemp -d)}"
BIN="${WORK}/netbox-gitops"
FAILURES=0

fail() { printf '  ✗ %-38s %s\n' "$1" "$2"; FAILURES=$((FAILURES+1)); }
ok()   { printf '  ✓ %-38s %s\n' "$1" "${2:-}"; }

auth_header() {
  case "${NETBOX_TOKEN}" in
    nbt_*) printf 'Authorization: Bearer %s' "${NETBOX_TOKEN}" ;;
    *)     printf 'Authorization: Token %s'  "${NETBOX_TOKEN}" ;;
  esac
}
api() { curl -sS --noproxy '*' -H "$(auth_header)" "${NETBOX_URL}/api/$1"; }

count() { api "$1/?$2" | python3 -c 'import json,sys; print(json.load(sys.stdin)["count"])'; }

# field <endpoint> <query> <dotted.path> -> the value on the single match, or "".
# A numeric path segment indexes a list (connected_endpoints.0.device.name).
# Missing keys and null values both render as the empty string, so an assertion
# never passes on an absent field.
field() {
  api "$1/?$2" | python3 -c '
import json, sys
r = json.load(sys.stdin)["results"]
if len(r) != 1:
    print(""); raise SystemExit
v = r[0]
for key in sys.argv[1].split("."):
    if key.isdigit():
        if not isinstance(v, list) or len(v) <= int(key):
            print(""); raise SystemExit
        v = v[int(key)]
        continue
    if not isinstance(v, dict) or v.get(key) is None:
        print(""); raise SystemExit
    v = v[key]
print(v)' "$3"
}

# expect <label> <actual> <wanted>
expect() {
  if [ "$2" = "$3" ]; then ok "$1" "$3"; else fail "$1" "got '${2:-<empty>}', want '$3'"; fi
}

summary_of() { grep -oE '[0-9]+ created, [0-9]+ updated, [0-9]+ deleted' "$1" | tail -1; }

wipe() {
  local empty="${WORK}/empty"
  mkdir -p "${empty}/definitions" "${empty}/inventory"
  "${BIN}" --data-dir "${empty}" --prune >"${WORK}/wipe.log" 2>&1 || true
}

echo "==> building"
go build -o "${BIN}" ./cmd/netbox-gitops/ || { echo "build failed"; exit 1; }
echo "==> NetBox: $(api status/ | python3 -c 'import json,sys; print(json.load(sys.stdin)["netbox-version"])' 2>/dev/null || echo unknown)"
wipe

# ---------------------------------------------------------------------------
# The same properties run.sh asserts for generated data, for the real thing.
# ---------------------------------------------------------------------------
echo "==> repository data"

if go run ./cmd/yamlcheck definitions inventory >"${WORK}/yamlcheck.log" 2>&1; then
  ok "yamlcheck"
else
  fail "yamlcheck" "$(grep -E '❌' "${WORK}/yamlcheck.log" | head -1)"
fi

"${BIN}" --dry-run >"${WORK}/dryrun.log" 2>&1
rc=$?
if [ "${rc}" -le 2 ]; then ok "dry-run" "exit ${rc}"; else
  fail "dry-run" "exit ${rc}: $(grep -iE '✗|Error:' "${WORK}/dryrun.log" | head -1)"
fi
if [ "$(count dcim/devices '')" = "0" ]; then ok "dry-run wrote nothing"; else
  fail "dry-run wrote nothing" "objects appeared during a dry run"
fi

if "${BIN}" >"${WORK}/apply1.log" 2>&1; then
  ok "apply" "$(summary_of "${WORK}/apply1.log")"
else
  fail "apply" "$(grep -iE '✗|Error:' "${WORK}/apply1.log" | head -1)"
fi

# The invariant that matters most: a second apply has nothing to do.
"${BIN}" >"${WORK}/apply2.log" 2>&1
s="$(summary_of "${WORK}/apply2.log")"
if echo "${s}" | grep -qE '^0 created, 0 updated, 0 deleted'; then
  ok "second apply is a no-op" "${s}"
else
  fail "second apply is a no-op" "${s:-no summary}"
fi

"${BIN}" --dry-run --detailed-exitcode >"${WORK}/drift.log" 2>&1
rc=$?
if [ "${rc}" -eq 0 ]; then ok "no drift reported"; else
  fail "no drift reported" "exit ${rc} (2 = changes still pending)"
fi

"${BIN}" --prune >"${WORK}/prune.log" 2>&1
s="$(summary_of "${WORK}/prune.log")"
if echo "${s}" | grep -qE ', 0 deleted'; then
  ok "prune deletes nothing" "${s}"
else
  fail "prune deletes nothing" "${s:-no summary}"
fi

# ---------------------------------------------------------------------------
# The conventions the sample data is written in, asserted on the live objects.
# ---------------------------------------------------------------------------
echo "==> conventions"

# A device carries the file's `defaults`: site, role, rack, status and tags are
# written once per file and must still reach NetBox on every device.
expect "defaults reach NetBox (site)" \
  "$(field dcim/devices "name=berlin-srv-web-01" "site.slug")" "berlin-dc"
expect "defaults reach NetBox (role)" \
  "$(field dcim/devices "name=berlin-srv-web-01" "role.slug")" "server"
expect "managed tag applied" \
  "$(count dcim/devices "tag=gitops")" "$(count dcim/devices '')"

# Ports come from the device type template. Listing an interface without a
# `type` must match the template's port rather than create a second one: the
# R740 template has exactly five interfaces and the device must have five.
expect "template ports are not duplicated" \
  "$(count dcim/interfaces "device=berlin-srv-web-01")" "5"

# The XE9680's ports are named by its template; the inventory must name them
# the same way or NetBox ends up holding both.
expect "device type interface exists" \
  "$(field dcim/interfaces "device=berlin-srv-ai-01&name=NIC.Slot.33-1-1" "name")" "NIC.Slot.33-1-1"
expect "no interface beside the template's" \
  "$(count dcim/interfaces "device=berlin-srv-ai-01&name=eth2")" "0"
expect "compute IP on the template port" \
  "$(field ipam/ip-addresses "address=10.10.40.201/24" "assigned_object.name")" "NIC.Slot.33-1-1"

# address_role: primary sets the device's primary IP.
expect "primary IP set from address_role" \
  "$(field dcim/devices "name=berlin-srv-web-01" "primary_ip4.address")" "10.10.20.101/24"

# A cable is declared on the server side only. The switch side must be wired
# from that single declaration — the failure mode is no cable at all.
expect "one-ended cable wires both ends" \
  "$(field dcim/interfaces "device=berlin-srv-web-01&name=eth0" "connected_endpoints.0.device.name")" "berlin-leaf-01"
expect "peer port is the declared one" \
  "$(field dcim/interfaces "device=berlin-srv-web-01&name=eth0" "connected_endpoints.0.name")" "Eth1/1"

# Dropping the switch-side `link:` must not have dropped the switch-side VLAN
# configuration, which is the reason that interface is still listed at all.
expect "switch port keeps its access VLAN" \
  "$(field dcim/interfaces "device=berlin-leaf-01&name=Eth1/1" "untagged_vlan.name")" "Server-Production"
expect "switch uplink keeps its trunk" \
  "$(count dcim/interfaces "device=berlin-leaf-01&name=Eth1/49&mode=tagged")" "1"

# The storage system is a chassis with a node installed into a device bay.
expect "child device is installed in its bay" \
  "$(field dcim/devices "name=berlin-storage-01-node-1" "parent_device.name")" "berlin-storage-01"
expect "chassis is the racked device" \
  "$(field dcim/devices "name=berlin-storage-01" "rack.name")" "Rack A02"
expect "storage IP lives on the node" \
  "$(field ipam/ip-addresses "address=10.10.30.10/24" "assigned_object.device.name")" "berlin-storage-01-node-1"

# Every device type slug the inventory names must have resolved; an unresolved
# one skips the device with a warning and exits 0.
# 5 in servers.yaml (two web, one AI, the storage chassis and its node),
# 1 in munich-lab.yaml, 3 switches, 4 patch panels.
expect "every declared device exists" "$(count dcim/devices '')" "13"
expect "patch panel resolved its device type" \
  "$(field dcim/devices "name=berlin-pp-mm-01" "device_type.slug")" "pp-48-mm-lc"

# The parked template is never applied.
expect "parked template is not applied" \
  "$(count dcim/devices "name=CHANGEME-hostname")" "0"

wipe
echo
if [ "${FAILURES}" -eq 0 ]; then
  echo "==> repository data converges and holds its conventions"
else
  echo "==> ${FAILURES} check(s) failed (work dir: ${WORK})"
  exit 1
fi
[ "${E2E_KEEP:-0}" = "1" ] || rm -rf "${WORK}"
