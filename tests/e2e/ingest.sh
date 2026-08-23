#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Walk the whole ingestion path end to end, on the repository's own sample data.
#
# Unlike its neighbours this needs no NetBox and no credentials, because the
# claim it checks is precisely that ingestion never touches one: everything on
# this path ends at a changed file in a working copy. It is the demonstration
# docs/INGEST.md describes, run rather than written down.
#
# It asserts the four things the design rests on:
#
#   1. a scan updates a declared device's serial and hw_* fields INSIDE the
#      hand-written file, disturbing nothing else in it;
#   2. a scanned machine the repository does not declare lands as a parked
#      skeleton, and an unfinished one fails yamlcheck loudly when unparked;
#   3. a Proxmox guest absent from the YAML appears as generated, appliable
#      YAML, and declaring it yourself takes it back out again;
#   4. re-running an unchanged scan changes nothing at all.
#
#   E2E_KEEP   set to 1 to leave the work directory in place for inspection
#   E2E_WORK   where the binary, dataset and logs are written
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

WORK="${E2E_WORK:-$(mktemp -d)}"
BIN="${WORK}/netbox-gitops"
DATA="${WORK}/data"
FAILURES=0

fail() { printf '  ✗ %-46s %s\n' "$1" "${2:-}"; FAILURES=$((FAILURES+1)); }
ok()   { printf '  ✓ %-46s %s\n' "$1" "${2:-}"; }

# contains <label> <file> <needle>
contains() {
  if grep -qF -- "$3" "$2"; then ok "$1"; else fail "$1" "expected to find: $3"; fi
}

# absent <label> <file> <needle>
absent() {
  if grep -qF -- "$3" "$2"; then fail "$1" "did not expect to find: $3"; else ok "$1"; fi
}

cleanup() {
  if [ "${E2E_KEEP:-0}" = "1" ] || [ "${FAILURES}" -gt 0 ]; then
    printf '\nWork directory kept: %s\n' "${WORK}"
  else
    rm -rf "${WORK}"
  fi
}
trap cleanup EXIT

echo "Building..."
go build -o "${BIN}" ./cmd/netbox-gitops/ || { echo "build failed"; exit 1; }

# A throwaway copy of the committed sample data, so the repository is untouched.
cp -r example "${DATA}"
SERVERS="${DATA}/inventory/hardware/active/servers.yaml"
ORIGINAL="${WORK}/servers.yaml.orig"
cp "${SERVERS}" "${ORIGINAL}"

# A scan of two machines: one the sample data declares, one it has never heard
# of. The first is matched on its hostname, the second on nothing at all.
cat > "${WORK}/scan.json" <<'JSON'
{
  "servers": [
    {"host": "10.0.100.11", "hostname": "example-server-01",
     "collected_at": "2026-08-23T06:14:02Z",
     "model": "PowerEdge R650", "manufacturer": "Dell Inc.",
     "serial_number": "CN7792048K0042", "service_tag": "7XKQ2M3",
     "bios_version": "1.14.2", "power_state": "On",
     "cpus": [{"cores": 32}], "cpu_count": 2, "cpu_model": "Intel Xeon Gold 6338",
     "memory": [{"type": "DDR4", "speed_mhz": 3200, "state": "Enabled"}],
     "total_memory_gib": 512, "memory_slots_total": 32,
     "memory_slots_used": 16, "memory_slots_free": 16,
     "drives": [{"capacity_gb": 894}, {"capacity_gb": 894}],
     "drive_count": 2, "total_storage_tb": 1.75,
     "power_consumed_watts": 284, "power_peak_watts": 612},

    {"host": "10.0.9.9", "hostname": "mystery-box",
     "collected_at": "2026-08-23T06:14:05Z",
     "model": "PowerEdge R7615", "manufacturer": "Dell Inc.",
     "serial_number": "UNKNOWN-42", "service_tag": "ZZ9PLURAL",
     "bios_version": "2.0.1", "power_state": "Off",
     "cpus": [{"cores": 64}], "cpu_count": 1, "cpu_model": "AMD EPYC 9554",
     "memory": [{"type": "DDR5", "speed_mhz": 4800, "state": "Enabled"}],
     "total_memory_gib": 768, "memory_slots_total": 24,
     "memory_slots_used": 12, "memory_slots_free": 12,
     "drives": [], "drive_count": 0, "total_storage_tb": 0},

    {"host": "10.0.9.10", "collected_at": "2026-08-23T06:14:07Z",
     "error": "dial tcp 10.0.9.10:443: connect: connection refused"}
  ],
  "stats": {"total_servers": 3, "successful_count": 2, "failed_count": 1}
}
JSON

INGEST=("${BIN}" ingest --data-dir "${DATA}" --format idrac-json --input "${WORK}/scan.json")

echo
echo "── A dry run writes nothing ─────────────────────────────────"
"${INGEST[@]}" --dry-run --detailed-exitcode > "${WORK}/dry-run.log" 2>&1
[ $? -eq 2 ] && ok "--detailed-exitcode reports pending changes" \
             || fail "--detailed-exitcode reports pending changes" "expected exit 2"
if cmp -s "${SERVERS}" "${ORIGINAL}"; then ok "the declared file is untouched"; else fail "the declared file is untouched"; fi
if [ -d "${DATA}/inventory/discovered" ]; then fail "no discovered/ directory is created"; else ok "no discovered/ directory is created"; fi
contains "the diff names the new serial" "${WORK}/dry-run.log" '+    serial: "CN7792048K0042"'

echo
echo "── Applying ─────────────────────────────────────────────────"
"${INGEST[@]}" --detailed-exitcode > "${WORK}/apply.log" 2>&1
[ $? -eq 2 ] && ok "the run reports what it changed" || fail "the run reports what it changed" "expected exit 2"
contains "the failed BMC is reported, not fatal" "${WORK}/apply.log" "10.0.9.10"
contains "the summary reads like the sync's plan line" "${WORK}/apply.log" "Facts: 1 updated"

echo
echo "── 1. Facts land inside the hand-written file ───────────────"
contains "the scanned serial replaced the declared one" "${SERVERS}" 'serial: "CN7792048K0042"'
contains "the service tag became the asset tag"         "${SERVERS}" 'asset_tag: "7XKQ2M3"'
contains "the hw_ custom fields were written"           "${SERVERS}" "hw_cpu_count: 2"
contains "including the volatile ones"                  "${SERVERS}" "hw_power_consumed_watts: 284"
# The whitelist: nothing the scan also knows may appear.
absent  "the scan did not impose a device name"         "${SERVERS}" "mystery-box"
absent  "the scan did not write a model"                "${SERVERS}" "PowerEdge R650"
absent  "the scan did not write a manufacturer"         "${SERVERS}" "Dell Inc."
# The file around the edit is intact.
contains "the file's own comments survived"             "${SERVERS}" "# Example Server Inventory for Testing"
contains "the defaults block survived"                  "${SERVERS}" 'device_type_slug: "example-server-r100"'
contains "cabling survived"                             "${SERVERS}" 'peer_port: "GigabitEthernet1/0/1"'
# Only the edited device's block moved.
if diff "${ORIGINAL}" "${SERVERS}" | grep -q '^< '; then
  fail "the edit removed nothing" "$(diff "${ORIGINAL}" "${SERVERS}" | grep '^< ' | head -3)"
else
  ok "the edit removed nothing"
fi

echo
echo "── 2. The unknown machine is parked ─────────────────────────"
SKELETON="${DATA}/inventory/discovered/hardware/idrac/_unknown-42.yaml"
if [ -f "${SKELETON}" ]; then ok "a parked skeleton was written" "${SKELETON#${DATA}/}"; else fail "a parked skeleton was written"; fi
contains "the run named it in the summary"        "${WORK}/apply.log" "_unknown-42.yaml"
contains "it says it is not applied"              "${SKELETON}" "NOT APPLIED"
contains "it carries what the scan knew"          "${SKELETON}" 'serial: "UNKNOWN-42"'
contains "and the hardware facts"                 "${SKELETON}" "hw_cpu_cores: 64"
contains "it marks the site as unknown"           "${SKELETON}" 'site_slug: "TODO"'
contains "and the rack"                           "${SKELETON}" 'rack_slug: "TODO"'
contains "and the role"                           "${SKELETON}" 'role_slug: "TODO"'
contains "and the device type"                    "${SKELETON}" 'device_type_slug: "TODO"'
contains "it records the address it answered on"  "${SKELETON}" "management address: 10.0.9.9"

# Parked, so the reconciler's loader never sees it.
go run ./cmd/yamlcheck "${DATA}" > "${WORK}/yamlcheck-parked.log" 2>&1
if [ $? -eq 0 ]; then ok "yamlcheck passes with the skeleton parked"; else fail "yamlcheck passes with the skeleton parked" "see ${WORK}/yamlcheck-parked.log"; fi

# Unparked but unfinished, it fails loudly rather than reaching NetBox.
cp "${SKELETON}" "${DATA}/inventory/discovered/hardware/idrac/unknown-42.yaml"
go run ./cmd/yamlcheck "${DATA}" > "${WORK}/yamlcheck-unparked.log" 2>&1
if [ $? -ne 0 ]; then ok "an unfinished skeleton fails yamlcheck when unparked"; else fail "an unfinished skeleton fails yamlcheck when unparked"; fi
contains "and says which field is unfinished" "${WORK}/yamlcheck-unparked.log" 'site "TODO" is not declared'
rm "${DATA}/inventory/discovered/hardware/idrac/unknown-42.yaml"

echo
echo "── 3. An undeclared VM becomes appliable YAML ───────────────"
# A fake Proxmox serving the three endpoints the collector reads, so `collect`
# runs for real rather than against a mock inside the test binary. Two guests:
# one the sample data declares (web-01) and one it has never heard of.
cat > "${WORK}/fake-pve.py" <<'PY'
import http.server, json, sys

DATA = {
    "/api2/json/version": {"version": "8.2.2", "release": "8.2"},
    "/api2/json/cluster/resources": [
        {"vmid": 101, "name": "web-01", "node": "pve-01", "type": "qemu",
         "status": "running", "maxcpu": 8, "maxmem": 17179869184, "maxdisk": 214748364800},
        {"vmid": 104, "name": "cache-01", "node": "pve-02", "type": "qemu",
         "status": "running", "maxcpu": 2, "maxmem": 4294967296, "maxdisk": 53687091200},
        {"vmid": 800, "name": "ubuntu-template", "node": "pve-01", "type": "qemu",
         "status": "stopped", "maxcpu": 2, "maxmem": 2147483648, "template": 1},
    ],
    "/api2/json/nodes/pve-01/qemu/101/config": {"cores": 8, "net0": "virtio=BC:24:11:00:00:01,bridge=vmbr0"},
    "/api2/json/nodes/pve-02/qemu/104/config": {"cores": 2, "net0": "virtio=BC:24:11:00:00:04,bridge=vmbr0"},
}

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        # Proxmox filters the resource list with ?type=vm; key on the path.
        self.path = self.path.split("?", 1)[0]
        if self.path not in DATA:
            self.send_error(404)
            return
        body = json.dumps({"data": DATA[self.path]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass

http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
PY

PVE_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
python3 "${WORK}/fake-pve.py" "${PVE_PORT}" & PVE_PID=$!
trap 'kill "${PVE_PID}" 2>/dev/null; cleanup' EXIT
for _ in $(seq 1 50); do
  curl -sS --noproxy '*' "http://127.0.0.1:${PVE_PORT}/api2/json/version" >/dev/null 2>&1 && break
  sleep 0.1
done

cat > "${DATA}/collectors.yaml" <<YAML
sources:
  - name: pve-prod
    type: proxmox
    url: http://127.0.0.1:${PVE_PORT}
    token_env: PROXMOX_TOKEN
    cluster: berlin-prod-cluster
YAML

PROXMOX_TOKEN="root@pam!e2e=unused" NO_PROXY='*' no_proxy='*' \
  "${BIN}" collect --data-dir "${DATA}" --detailed-exitcode > "${WORK}/collect.log" 2>&1
[ $? -eq 2 ] && ok "collect reports what it found" || fail "collect reports what it found" "see ${WORK}/collect.log"

GENERATED="${DATA}/inventory/discovered/virtual/pve-prod/berlin-prod-cluster.yaml"
if [ -f "${GENERATED}" ]; then ok "a generated VM file was written" "${GENERATED#${DATA}/}"; else fail "a generated VM file was written"; fi
contains "the undeclared guest is in it"       "${GENERATED}" 'name: "cache-01"'
contains "with the facts the scan reported"    "${GENERATED}" "vmid: 104"
contains "and its NIC"                         "${GENERATED}" 'mac_address: "BC:24:11:00:00:04"'
contains "it says it was generated"            "${GENERATED}" "# Generated by"
absent  "the declared guest is not duplicated" "${GENERATED}" "web-01"
absent  "a template is not documented as a VM" "${GENERATED}" "ubuntu-template"

# The declared guest's facts went into the hand-written file instead.
contains "the declared guest's facts went to its own file" \
  "${DATA}/inventory/virtual/prod/web-01.yaml" "vcpus: 8"

# yamlcheck accepts the generated file: it is meant to be appliable as it is.
go run ./cmd/yamlcheck "${DATA}" > "${WORK}/yamlcheck-generated.log" 2>&1
if [ $? -eq 0 ]; then ok "yamlcheck accepts the generated VM file"; else fail "yamlcheck accepts the generated VM file" "see ${WORK}/yamlcheck-generated.log"; fi

# A second collect of the same cluster is byte-identical.
cp "${GENERATED}" "${WORK}/generated.first"
PROXMOX_TOKEN="root@pam!e2e=unused" NO_PROXY='*' no_proxy='*' \
  "${BIN}" collect --data-dir "${DATA}" --detailed-exitcode > "${WORK}/collect2.log" 2>&1
[ $? -eq 0 ] && ok "a second collect has nothing to do" || fail "a second collect has nothing to do" "expected exit 0"
if cmp -s "${GENERATED}" "${WORK}/generated.first"; then ok "the generated file is byte-stable"; else fail "the generated file is byte-stable"; fi

kill "${PVE_PID}" 2>/dev/null
trap cleanup EXIT
rm "${DATA}/collectors.yaml"

echo
echo "── 3b. Adopting a discovered VM removes it from the file ────"
mkdir -p "${DATA}/inventory/virtual/prod"
cat > "${DATA}/inventory/virtual/prod/cache-01.yaml" <<'YAML'
# Adopted from the generated file by declaring it here. The generated file is
# deliberately left untouched: the next run is what removes the block from it.
- name: "cache-01"
  cluster: "berlin-prod-cluster"
  role_slug: "vm"
  vcpus: 2
YAML
python3 "${WORK}/fake-pve.py" "${PVE_PORT}" & PVE_PID=$!
trap 'kill "${PVE_PID}" 2>/dev/null; cleanup' EXIT
for _ in $(seq 1 50); do
  curl -sS --noproxy '*' "http://127.0.0.1:${PVE_PORT}/api2/json/version" >/dev/null 2>&1 && break
  sleep 0.1
done
cat > "${DATA}/collectors.yaml" <<YAML
sources:
  - name: pve-prod
    type: proxmox
    url: http://127.0.0.1:${PVE_PORT}
    token_env: PROXMOX_TOKEN
    cluster: berlin-prod-cluster
YAML
PROXMOX_TOKEN="root@pam!e2e=unused" NO_PROXY='*' no_proxy='*' \
  "${BIN}" collect --data-dir "${DATA}" --detailed-exitcode > "${WORK}/adopt.log" 2>&1
[ $? -eq 2 ] && ok "the adoption is reported as a change" || fail "the adoption is reported as a change" "see ${WORK}/adopt.log"
contains "the summary names it an adoption" "${WORK}/adopt.log" "Adopted:"
# cache-01 was the only guest in the generated file, so the file goes away.
if [ -f "${GENERATED}" ]; then
  absent "the adopted guest left the generated file" "${GENERATED}" 'name: "cache-01"'
else
  ok "the emptied generated file was removed"
fi
contains "the operator's own declaration survived" "${DATA}/inventory/virtual/prod/cache-01.yaml" 'role_slug: "vm"'
go run ./cmd/yamlcheck "${DATA}" > "${WORK}/yamlcheck-adopted.log" 2>&1
if [ $? -eq 0 ]; then ok "yamlcheck still passes after the adoption"; else fail "yamlcheck still passes after the adoption" "see ${WORK}/yamlcheck-adopted.log"; fi
# And it stays adopted.
PROXMOX_TOKEN="root@pam!e2e=unused" NO_PROXY='*' no_proxy='*' \
  "${BIN}" collect --data-dir "${DATA}" --detailed-exitcode > "${WORK}/adopt2.log" 2>&1
[ $? -eq 0 ] && ok "a further run has nothing to do" || fail "a further run has nothing to do" "expected exit 0"
kill "${PVE_PID}" 2>/dev/null
trap cleanup EXIT
rm -f "${DATA}/collectors.yaml"

echo
echo "── 4. Re-running an unchanged scan changes nothing ──────────"
cp "${SERVERS}" "${WORK}/servers.yaml.after"
"${INGEST[@]}" --detailed-exitcode > "${WORK}/rerun.log" 2>&1
[ $? -eq 0 ] && ok "the second run reports nothing to do" || fail "the second run reports nothing to do" "expected exit 0"
if cmp -s "${SERVERS}" "${WORK}/servers.yaml.after"; then ok "the declared file is byte-identical"; else fail "the declared file is byte-identical"; fi
if cmp -s "${SKELETON}" "${SKELETON}"; then ok "the skeleton is byte-identical"; fi
contains "and counts the device as unchanged" "${WORK}/rerun.log" "Facts: 0 updated"

echo
echo "── A changed fact moves one line ────────────────────────────"
python3 - "${WORK}/scan.json" <<'PY'
import json, sys
path = sys.argv[1]
doc = json.load(open(path))
doc["servers"][0]["power_consumed_watts"] = 999
json.dump(doc, open(path, "w"), indent=2)
PY
"${INGEST[@]}" --dry-run > "${WORK}/onefact.log" 2>&1
CHANGED=$(grep -c '^[-+]      ' "${WORK}/onefact.log")
if [ "${CHANGED}" = "2" ]; then
  ok "one changed watt reading is a one-line diff"
else
  fail "one changed watt reading is a one-line diff" "${CHANGED} changed lines"
fi

echo
if [ "${FAILURES}" -eq 0 ]; then
  echo "✅ ingest: everything asserted holds"
  exit 0
fi
echo "❌ ingest: ${FAILURES} assertion(s) failed"
exit 1
