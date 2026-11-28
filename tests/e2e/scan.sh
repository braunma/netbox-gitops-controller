#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# `scan` against a real NetBox and a fake iDRAC.
#
# `scan` is the one command that writes something observed straight to NetBox,
# so what has to be proven is not only that the values arrive, but that the
# route is as narrow as it claims and does not fight the sync:
#
#   - a dry run reads and compares, and writes nothing;
#   - each source reaches only the devices it selects, with its own
#     credentials, at the address the inventory declares;
#   - only declared hw_ custom fields are written; serial and asset_tag never;
#   - a host that is down, and a device NetBox does not hold yet, are reported
#     and do not stop the others;
#   - a sync after a scan changes nothing: the scanned values survive it.
#
#   NETBOX_URL    base URL of a THROWAWAY NetBox (managed objects are pruned)
#   NETBOX_TOKEN  API token (v1 or v2)
#   E2E_KEEP      set to 1 to leave the work directory in place for inspection
#   E2E_WORK      where the binary, the data and the logs are written
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

: "${NETBOX_URL:?NETBOX_URL must be set}"
: "${NETBOX_TOKEN:?NETBOX_TOKEN must be set}"
WORK="${E2E_WORK:-$(mktemp -d)}"
BIN="${WORK}/netbox-gitops"
DIR="${WORK}/data"
FAILURES=0
FAKE_PID=""

fail() { printf '  ✗ %-44s %s\n' "$1" "$2"; FAILURES=$((FAILURES+1)); }
ok()   { printf '  ✓ %-44s %s\n' "$1" "${2:-}"; }

auth_header() {
  case "${NETBOX_TOKEN}" in
    nbt_*) printf 'Authorization: Bearer %s' "${NETBOX_TOKEN}" ;;
    *)     printf 'Authorization: Token %s'  "${NETBOX_TOKEN}" ;;
  esac
}
api() { curl -sS --noproxy '*' -H "$(auth_header)" "${NETBOX_URL}/api/$1"; }

# field <device> <key> -> a custom field (cf.<name>) or a top-level field of
# the device, "None" when unset, "" when the device does not exist.
field() {
  api "dcim/devices/?name=$1" | python3 -c '
import json, sys
key = sys.argv[1]
r = json.load(sys.stdin)["results"]
if len(r) == 1:
    d = r[0]
    print(d["custom_fields"].get(key[3:]) if key.startswith("cf.") else d.get(key))' "$2"
}

# expect <label> <actual> <wanted>
expect() { if [ "$2" = "$3" ]; then ok "$1" "$2"; else fail "$1" "got '$2', want '$3'"; fi; }
# has / lacks <label> <file> <needle>
has()   { if grep -qF -- "$3" "$2"; then ok "$1"; else fail "$1" "expected to find: $3"; fi; }
lacks() { if grep -qF -- "$3" "$2"; then fail "$1" "did not expect to find: $3"; else ok "$1"; fi; }

cleanup() {
  [ -n "${FAKE_PID}" ] && kill "${FAKE_PID}" 2>/dev/null
  if [ "${E2E_KEEP:-0}" = "1" ] || [ "${FAILURES}" -gt 0 ]; then
    printf '\nWork directory kept: %s\n' "${WORK}"
  else
    rm -rf "${WORK}"
  fi
}
trap cleanup EXIT

sync() { "${BIN}" --data-dir "${DIR}" --full >"${WORK}/$1.log" 2>&1; }
scan() { local log="$1"; shift; "${BIN}" scan --data-dir "${DIR}" "$@" >"${WORK}/${log}.log" 2>&1; }

echo "Building..."
go build -o "${BIN}" ./cmd/netbox-gitops/ || { echo "build failed"; exit 1; }

# Start from nothing this controller manages, and without this test's fields:
# --prune leaves custom fields alone, so they are removed directly.
mkdir -p "${WORK}/empty/definitions" "${WORK}/empty/inventory"
"${BIN}" --data-dir "${WORK}/empty" --full --prune >"${WORK}/wipe.log" 2>&1 || true
api "extras/custom-fields/?name__isw=hw_&limit=200" | python3 -c '
import json, sys
for cf in json.load(sys.stdin)["results"]: print(cf["id"])' | while read -r cfid; do
  curl -sS --noproxy '*' -X DELETE -H "$(auth_header)" "${NETBOX_URL}/api/extras/custom-fields/${cfid}/" >/dev/null
done

# ---------------------------------------------------------------------------
# A fake iDRAC. It listens on every loopback address and answers by the
# address it was reached on, so one process stands in for several machines:
#   127.0.0.1  srv-a     password pw-a
#   127.0.0.2  srv-b     password pw-b
#   127.0.0.3  srv-late  password pw-a
#   127.0.0.9  srv-down  answers 503
# Anything but a GET is counted, and the test requires that count to be zero.
# ---------------------------------------------------------------------------
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=fake-idrac" \
  -keyout "${WORK}/key.pem" -out "${WORK}/cert.pem" >/dev/null 2>&1 || { echo "openssl failed"; exit 1; }

cat > "${WORK}/fake-idrac.py" <<'PY'
import base64, json, ssl, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HOSTS = {
    "127.0.0.1": {"pw": "pw-a", "serial": "SERIAL-A", "tag": "TAG-A", "bios": "1.6.10", "watts": 412},
    "127.0.0.2": {"pw": "pw-b", "serial": "SERIAL-B", "tag": "TAG-B", "bios": "2.1.5", "watts": 655, "gpus": 3},
    "127.0.0.3": {"pw": "pw-a", "serial": "SERIAL-L", "tag": "TAG-L", "bios": "1.6.10", "watts": 300},
}
SYS = "/redfish/v1/Systems/System.Embedded.1"
writes = [0]

def tree(h):
    gpus = {"/p/gpu%d" % i: {"Model": "Tesla T4", "ProcessorType": "GPU", "TotalCores": 0, "Status": {"State": "Enabled"}}
            for i in range(h.get("gpus", 0))}
    return gpus | {
        SYS: {"Model": "PowerEdge R660", "Manufacturer": "Dell Inc.", "SerialNumber": h["serial"], "SKU": h["tag"],
              "BiosVersion": h["bios"], "HostName": "host", "PowerState": "On",
              "ProcessorSummary": {"Count": 2, "Model": "Intel Xeon Gold 6430"},
              "MemorySummary": {"TotalSystemMemoryGiB": 64},
              "Oem": {"Dell": {"DellSystem": {"MaxDIMMSlots": 16}}}},
        SYS + "/Processors": {"Members": [{"@odata.id": "/p/1"}, {"@odata.id": "/p/2"}] + [{"@odata.id": k} for k in gpus]},
        "/p/1": {"Model": "Xeon", "ProcessorType": "CPU", "TotalCores": 32, "Status": {"State": "Enabled"}},
        "/p/2": {"Model": "Xeon", "ProcessorType": "CPU", "TotalCores": 32, "Status": {"State": "Enabled"}},
        SYS + "/Memory": {"Members": [{"@odata.id": "/m/1"}, {"@odata.id": "/m/2"}]},
        "/m/1": {"MemoryDeviceType": "DDR5", "CapacityMiB": 32768, "OperatingSpeedMhz": 4800, "Status": {"State": "Enabled"}},
        "/m/2": {"MemoryDeviceType": "DDR5", "CapacityMiB": 32768, "OperatingSpeedMhz": 4800, "Status": {"State": "Enabled"}},
        SYS + "/Storage": {"Members": [{"@odata.id": "/s/1"}]},
        "/s/1": {"Drives": [{"@odata.id": "/d/1"}, {"@odata.id": "/d/2"}]},
        "/d/1": {"CapacityBytes": 960197124096},
        "/d/2": {"CapacityBytes": 960197124096},
        "/redfish/v1/Chassis/System.Embedded.1/Power":
            {"PowerControl": [{"PowerConsumedWatts": h["watts"], "PowerMetrics": {"MaxConsumedWatts": h["watts"] + 100}}]},
    }

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def reply(self, code, body=None):
        data = json.dumps(body).encode() if body is not None else b""
        self.send_response(code); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data))); self.end_headers(); self.wfile.write(data)
    def do_GET(self):
        if self.path == "/__writes":
            return self.reply(200, {"writes": writes[0]})
        host = HOSTS.get(self.connection.getsockname()[0])
        if host is None:
            return self.reply(503)
        auth = self.headers.get("Authorization", "")
        want = "Basic " + base64.b64encode(("reader:" + host["pw"]).encode()).decode()
        if auth != want:
            return self.reply(401)
        body = tree(host).get(self.path)
        return self.reply(200, body) if body is not None else self.reply(404)
    def other(self):
        writes[0] += 1; self.reply(405)
    do_POST = do_PATCH = do_PUT = do_DELETE = other

srv = ThreadingHTTPServer(("0.0.0.0", int(sys.argv[1])), Handler)
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER); ctx.load_cert_chain(sys.argv[2], sys.argv[3])
srv.socket = ctx.wrap_socket(srv.socket, server_side=True)
srv.serve_forever()
PY

PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
python3 "${WORK}/fake-idrac.py" "${PORT}" "${WORK}/cert.pem" "${WORK}/key.pem" & FAKE_PID=$!
FAKE_UP=0
for _ in $(seq 1 300); do
  if curl -sSk --noproxy '*' "https://127.0.0.1:${PORT}/__writes" >/dev/null 2>&1; then FAKE_UP=1; break; fi
  sleep 0.1
done
# Without the fake iDRAC every scan check below would fail for a reason that
# has nothing to do with the scanner, so stop here and say so.
[ "${FAKE_UP}" = 1 ] || { echo "the fake iDRAC did not come up on port ${PORT}"; exit 1; }

# ---------------------------------------------------------------------------
# The data: one site, one device type with an `idrac` port, four servers in
# two credential groups. hw_power_peak_watts is deliberately not declared.
# ---------------------------------------------------------------------------
mkdir -p "${DIR}"/definitions/{sites,racks,roles,extras,prefixes,device_types,custom_fields} "${DIR}/inventory/hardware/active"

cat > "${DIR}/definitions/sites/s.yaml" <<'YAML'
- { name: "Scan Lab", slug: "scan-lab", status: "active" }
YAML
cat > "${DIR}/definitions/roles/r.yaml" <<'YAML'
- { name: "Server", slug: "server", color: "00ff00" }
YAML
cat > "${DIR}/definitions/extras/tags.yaml" <<'YAML'
- { name: "Group A", slug: "grp-a", color: "2196f3" }
- { name: "Group B", slug: "grp-b", color: "ff9800" }
YAML
cat > "${DIR}/definitions/prefixes/p.yaml" <<'YAML'
- { prefix: "127.0.0.0/8", site_slug: "scan-lab", status: "active" }
YAML
cat > "${DIR}/definitions/device_types/dt.yaml" <<'YAML'
- model: "PowerEdge R660"
  slug: "r660"
  manufacturer: "Dell"
  u_height: 1
  interfaces:
    - { name: "idrac", type: "1000base-t", mgmt_only: true }
YAML
{
  echo 'defaults:'
  echo '  object_types: ["dcim.device"]'
  echo 'items:'
  for f in cpu_count:integer cpu_model:text cpu_cores:integer ram_total_gb:integer ram_slots_total:integer \
           ram_slots_used:integer ram_slots_available:integer memory_type:text memory_speed_mhz:integer \
           disk_count:integer storage_summary:text storage_total_tb:text bios_version:text power_state:text \
           power_consumed_watts:integer last_inventory:text gpu_count:integer gpu_model:text; do
    echo "  - { name: \"hw_${f%%:*}\", type: \"${f##*:}\" }"
  done
} > "${DIR}/definitions/custom_fields/hw.yaml"

write_devices() {
  # $1 = "yes" to also declare srv-late, the device NetBox does not hold yet.
  cat > "${DIR}/inventory/hardware/active/servers.yaml" <<'YAML'
defaults:
  site: "scan-lab"
  device_type: "r660"
  role: "server"
devices:
  - name: "srv-a"
    tags: ["grp-a"]
    serial: "SERIAL-A"
    asset_tag: "TAG-A"
    interfaces:
      - { name: "idrac", ip: { address: "127.0.0.1/8" } }
  - name: "srv-b"
    tags: ["grp-b"]
    serial: "TYPO-B"
    interfaces:
      - { name: "idrac", ip: { address: "127.0.0.2/8" } }
  - name: "srv-down"
    tags: ["grp-a"]
    interfaces:
      - { name: "idrac", ip: { address: "127.0.0.9/8" } }
YAML
  [ "${1:-}" = "yes" ] && cat >> "${DIR}/inventory/hardware/active/servers.yaml" <<'YAML'
  - name: "srv-late"
    tags: ["grp-a"]
    interfaces:
      - { name: "idrac", ip: { address: "127.0.0.3/8" } }
YAML
  return 0
}

write_sources() {
  # $1 = the selector of the second source.
  cat > "${DIR}/collectors.yaml" <<YAML
sources:
  - name: idrac-a
    type: idrac
    username_env: E2E_IDRAC_USER
    password_env: E2E_IDRAC_PASS_A
    verify_tls: false
    targets: inventory
    interface: idrac
    port: ${PORT}
    select: { tags: ["grp-a"] }
  - name: idrac-b
    type: idrac
    username_env: E2E_IDRAC_USER
    password_env: E2E_IDRAC_PASS_B
    verify_tls: false
    targets: inventory
    interface: idrac
    port: ${PORT}
    select: $1
YAML
}

export E2E_IDRAC_USER=reader E2E_IDRAC_PASS_A=pw-a E2E_IDRAC_PASS_B=pw-b

write_devices
write_sources '{ tags: ["grp-b"] }'

echo "Sync (creates the devices a scan will write to)..."
if sync apply; then ok "sync"; else fail "sync" "see ${WORK}/apply.log"; tail -20 "${WORK}/apply.log"; exit 1; fi
write_devices yes

# ---------------------------------------------------------------------------
echo "Dry run..."
if scan dry --dry-run; then ok "dry run exits 0"; else fail "dry run exits 0" "see ${WORK}/dry.log"; fi
has    "dry run reports the change"            "${WORK}/dry.log" "hw_bios_version (unset) → 1.6.10"
expect "dry run wrote nothing"                 "$(field srv-a cf.hw_cpu_count)" "None"

# ---------------------------------------------------------------------------
echo "Scan..."
if scan scan; then ok "scan exits 0"; else fail "scan exits 0" "see ${WORK}/scan.log"; fi
expect "srv-a cpu count"                       "$(field srv-a cf.hw_cpu_count)" "2"
expect "srv-a cores of one socket"             "$(field srv-a cf.hw_cpu_cores)" "32"
expect "srv-a ram"                             "$(field srv-a cf.hw_ram_total_gb)" "64"
expect "srv-a free ram slots"                  "$(field srv-a cf.hw_ram_slots_available)" "14"
expect "srv-a storage summary"                 "$(field srv-a cf.hw_storage_summary)" "2x894GB"
expect "srv-a bios"                            "$(field srv-a cf.hw_bios_version)" "1.6.10"
expect "srv-a power draw"                      "$(field srv-a cf.hw_power_consumed_watts)" "412"
# srv-b answers only to the second source's password, and has its own values.
expect "srv-b scanned with its own credentials" "$(field srv-b cf.hw_bios_version)" "2.1.5"
expect "srv-b power draw"                      "$(field srv-b cf.hw_power_consumed_watts)" "655"
# GPUs sit in the processor list: they are counted apart, not as sockets, and
# a machine without one says 0 rather than nothing.
expect "srv-b gpu count"                       "$(field srv-b cf.hw_gpu_count)" "3"
expect "srv-b gpu model"                       "$(field srv-b cf.hw_gpu_model)" "3× Tesla T4"
expect "srv-b cpu count leaves the GPUs out"   "$(field srv-b cf.hw_cpu_count)" "2"
expect "srv-a has no GPU"                      "$(field srv-a cf.hw_gpu_count)" "0"
expect "srv-a gpu model stays unset"           "$(field srv-a cf.hw_gpu_model)" "None"
# Identity is the YAML's. The BMC disagrees about srv-b's serial; the scan
# says so and leaves it.
expect "srv-b serial untouched"                "$(field srv-b serial)" "TYPO-B"
has    "serial mismatch reported"              "${WORK}/scan.log" 'serial MISMATCH — the YAML declares "TYPO-B"'
lacks  "matching serial not reported"          "${WORK}/scan.log" 'srv-a: serial MISMATCH'
has    "undeclared asset tag mentioned"        "${WORK}/scan.log" 'srv-b: the BMC reports asset_tag "TAG-B"'
expect "srv-b asset tag untouched"             "$(field srv-b asset_tag)" "None"
has    "down host reported"                    "${WORK}/scan.log" "srv-down (127.0.0.9): not scanned"
has    "device not in NetBox reported"         "${WORK}/scan.log" "srv-late: not in NetBox yet"
expect "scan created no device"                "$(field srv-late name)" ""
has    "undeclared field named, not written"   "${WORK}/scan.log" "hw_power_peak_watts"
has    "summary"                               "${WORK}/scan.log" "Devices: 2 updated, 0 unchanged, 1 not in NetBox, 1 not scanned"
expect "nothing but GETs reached the BMCs"     "$(curl -sSk --noproxy '*' "https://127.0.0.1:${PORT}/__writes")" '{"writes": 0}'

# ---------------------------------------------------------------------------
echo "Sync after scan..."
# The same devices as before the scan, so the sync has nothing of its own to
# do: any update it makes now is the sync undoing what the scan wrote.
write_devices
if sync resync; then ok "sync"; else fail "sync" "see ${WORK}/resync.log"; fi
s="$(grep -oE '[0-9]+ created, [0-9]+ updated, [0-9]+ deleted' "${WORK}/resync.log" | tail -1)"
if echo "${s}" | grep -qE '^0 created, 0 updated, 0 deleted'; then ok "sync after scan has nothing to do" "${s}"
else fail "sync after scan has nothing to do" "${s}"; fi
expect "scanned value survives the sync"       "$(field srv-a cf.hw_bios_version)" "1.6.10"

write_devices yes
sync late || fail "sync creating srv-late" "see ${WORK}/late.log"

echo "Second scan..."
scan rescan || fail "second scan exits 0" "see ${WORK}/rescan.log"
expect "late device is scanned once it exists" "$(field srv-late cf.hw_bios_version)" "1.6.10"
# Only the timestamp moves on a machine that did not change.
lacks  "stable facts are not rewritten"        "${WORK}/rescan.log" "srv-a: hw_bios_version"

# ---------------------------------------------------------------------------
echo "Failure modes..."
if scan strict --strict; then fail "--strict fails on a down host" "exited 0"; else ok "--strict fails on a down host"; fi

if E2E_IDRAC_PASS_B=wrong scan badpw --source idrac-b; then ok "wrong password does not fail the run"
else fail "wrong password does not fail the run" "see ${WORK}/badpw.log"; fi
has    "wrong password named as such"          "${WORK}/badpw.log" "srv-b (127.0.0.2): not scanned: the BMC rejected the credentials"
lacks  "--source limits the run"               "${WORK}/badpw.log" "srv-a"

if (unset E2E_IDRAC_PASS_A; scan nopw); then fail "unset credentials stop the run" "exited 0"; else ok "unset credentials stop the run"; fi
has    "unset variable named"                  "${WORK}/nopw.log" "E2E_IDRAC_PASS_A"

# Two sources that both select srv-a would send it two passwords.
write_sources '{ site: "scan-lab" }'
if scan overlap; then fail "overlapping sources are refused" "exited 0"; else ok "overlapping sources are refused"; fi
has    "overlap names the device and sources"  "${WORK}/overlap.log" "device srv-a is selected by 2 sources (idrac-a, idrac-b)"
write_sources '{ tags: ["grp-b"] }'

if "${BIN}" collect --data-dir "${DIR}" --source idrac-a >"${WORK}/collect.log" 2>&1; then
  fail "collect refuses a scan source" "exited 0"
else ok "collect refuses a scan source"; fi
has    "collect points at scan"                "${WORK}/collect.log" 'which `scan` reads, not `collect`'

echo
if [ "${FAILURES}" -gt 0 ]; then
  echo "${FAILURES} check(s) FAILED"
  exit 1
fi
echo "All scan checks passed"
