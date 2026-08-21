#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Rename coverage: for every object type that declares `rename_from`, prove
# against a real NetBox that correcting its identity edits the existing object
# instead of creating a second one.
#
# The bug this guards against is silent. Without rename_from the old identity
# stops matching, so the sync creates a replacement and leaves the original
# behind — still holding every reference that pointed at it — and reports
# success. Asserting "no duplicate" is therefore not enough on its own: each
# case pins the NetBox object ID before the rename and requires the same ID
# afterwards, which is the only evidence that the object was edited rather
# than rebuilt.
#
#   NETBOX_URL    base URL, e.g. http://netbox:8080
#   NETBOX_TOKEN  API token (v1 or v2)
#   E2E_KEEP      set to 1 to leave the work directory in place for inspection
#   E2E_WORK      where the binary, datasets and logs are written
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

: "${NETBOX_URL:?NETBOX_URL must be set}"
: "${NETBOX_TOKEN:?NETBOX_TOKEN must be set}"
WORK="${E2E_WORK:-$(mktemp -d)}"
BIN="${WORK}/netbox-gitops"
DIR="${WORK}/data"
FAILURES=0

fail() { printf '  ✗ %-34s %s\n' "$1" "$2"; FAILURES=$((FAILURES+1)); }
ok()   { printf '  ✓ %-34s %s\n' "$1" "${2:-}"; }

auth_header() {
  case "${NETBOX_TOKEN}" in
    nbt_*) printf 'Authorization: Bearer %s' "${NETBOX_TOKEN}" ;;
    *)     printf 'Authorization: Token %s'  "${NETBOX_TOKEN}" ;;
  esac
}
api() { curl -sS --noproxy '*' -H "$(auth_header)" "${NETBOX_URL}/api/$1"; }

# id <endpoint> <query> -> the ID of the single matching object, or "" .
id() {
  api "$1/?$2" | python3 -c '
import json,sys
r = json.load(sys.stdin)["results"]
print(r[0]["id"] if len(r) == 1 else "")'
}
count() { api "$1/?$2" | python3 -c 'import json,sys; print(json.load(sys.stdin)["count"])'; }

sync() { "${BIN}" --data-dir "${DIR}" >"${WORK}/$1.log" 2>&1; }

wipe() {
  local empty="${WORK}/empty"
  mkdir -p "${empty}/definitions" "${empty}/inventory"
  "${BIN}" --data-dir "${empty}" --prune >"${WORK}/wipe.log" 2>&1 || true
  # --prune deliberately leaves custom fields alone: deleting one destroys the
  # value it holds on every object that has it. They therefore survive a wipe,
  # and a leftover field under this test's *new* name would make the rename a
  # no-op — correctly, since two objects would then claim that identity — and
  # the run would look like a failure of the code rather than of its fixture.
  # So remove this test's own fields directly.
  local name cfid
  for name in vmid vmid_x; do
    cfid="$(id extras/custom-fields "name=${name}")"
    [ -n "${cfid}" ] && curl -sS --noproxy '*' -X DELETE -H "$(auth_header)" \
      "${NETBOX_URL}/api/extras/custom-fields/${cfid}/" >/dev/null
  done
}

# check <label> <endpoint> <query-before> <query-after>
# Requires: exactly one object before, the same ID after, and no duplicate.
check() {
  local label="$1" endpoint="$2" before_q="$3" after_q="$4"
  local before_id="$5"
  local after_id after_count
  after_id="$(id "${endpoint}" "${after_q}")"
  after_count="$(count "${endpoint}" "${before_q}")"

  if [ -z "${after_id}" ]; then
    fail "${label}" "nothing found under the new identity (${after_q})"
  elif [ "${after_id}" != "${before_id}" ]; then
    fail "${label}" "ID changed ${before_id} → ${after_id}: recreated, not renamed"
  elif [ "${after_count}" != "0" ]; then
    fail "${label}" "the old identity still exists: a duplicate was created"
  else
    ok "${label}" "id ${before_id} kept"
  fi
}

echo "==> building"
go build -o "${BIN}" ./cmd/netbox-gitops/ || { echo "build failed"; exit 1; }
echo "==> NetBox: $(api status/ | python3 -c 'import json,sys; print(json.load(sys.stdin)["netbox-version"])' 2>/dev/null || echo unknown)"
wipe

# ---------------------------------------------------------------------------
# Pass 1: create everything under a deliberately mistyped identity.
# ---------------------------------------------------------------------------
mkdir -p "${DIR}"/definitions/{sites,racks,roles,platforms,tenant_groups,tenants,tags,vrfs,vlan_groups,vlans,prefixes,device_types,module_types,custom_fields} \
         "${DIR}"/definitions/virtualization/{cluster_types,cluster_groups,clusters} \
         "${DIR}"/inventory/hardware/active "${DIR}"/inventory/virtual/prod

write_data() {
  # $1 = "before" or "after"; the identity fields differ between the two.
  local p="$1"
  local site_slug rack_name role_slug plat_slug tg_slug tenant_slug tag_slug
  local vrf_name vg_slug vlan_vid prefix dt_slug mt_model dev_name iface_name
  local ct_slug cg_slug cluster_name vm_name vmif_name cf_name rename

  if [ "${p}" = "before" ]; then
    site_slug=frankfrut; rack_name="Rakc 01"; role_slug=compyte; plat_slug=lnux
    tg_slug=infar; tenant_slug=platfrom; tag_slug=managd; vrf_name="Managemnt"
    vg_slug=sitevlnas; vlan_vid=201; prefix="10.77.0.0/24"; dt_slug=r6600
    mt_model="OCP Mezz 25G x"; dev_name=srv-01x; iface_name=etho0; ct_slug=proxmoxx
    cg_slug=prd; cluster_name="Cluster Ax"; vm_name=vm-01x; vmif_name=eth00
    cf_name=vmid_x; rename=""
  else
    site_slug=frankfurt; rack_name="Rack 01"; role_slug=compute; plat_slug=linux
    tg_slug=infra; tenant_slug=platform; tag_slug=managed; vrf_name="Management"
    vg_slug=site-vlans; vlan_vid=210; prefix="10.78.0.0/24"; dt_slug=r660
    mt_model="OCP Mezz 25G"; dev_name=srv-01; iface_name=eth0; ct_slug=proxmox
    cg_slug=prod; cluster_name="Cluster A"; vm_name=vm-01; vmif_name=eth0
    cf_name=vmid; rename="yes"
  fi

  r() { [ -n "${rename}" ] && printf '  rename_from: %s\n' "$1"; }

  { echo "- name: Frankfurt"; echo "  slug: ${site_slug}"; echo "  status: active"
    [ -n "${rename}" ] && echo "  rename_from: frankfrut"; } > "${DIR}/definitions/sites/s.yaml"

  { echo "- name: ${rack_name}"; echo "  slug: rack-01"; echo "  site_slug: ${site_slug}"; echo "  status: active"
    [ -n "${rename}" ] && echo "  rename_from: Rakc 01"; } > "${DIR}/definitions/racks/r.yaml"

  { echo "- name: Compute"; echo "  slug: ${role_slug}"; echo '  color: "00ff00"'
    [ -n "${rename}" ] && echo "  rename_from: compyte"; } > "${DIR}/definitions/roles/r.yaml"

  { echo "- name: Linux"; echo "  slug: ${plat_slug}"
    [ -n "${rename}" ] && echo "  rename_from: lnux"; } > "${DIR}/definitions/platforms/p.yaml"

  { echo "- name: Infrastructure"; echo "  slug: ${tg_slug}"
    [ -n "${rename}" ] && echo "  rename_from: infar"; } > "${DIR}/definitions/tenant_groups/tg.yaml"

  { echo "- name: Platform Team"; echo "  slug: ${tenant_slug}"; echo "  group_slug: ${tg_slug}"
    [ -n "${rename}" ] && echo "  rename_from: platfrom"; } > "${DIR}/definitions/tenants/t.yaml"

  { echo "- name: Managed"; echo "  slug: ${tag_slug}"; echo '  color: "2196f3"'
    [ -n "${rename}" ] && echo "  rename_from: managd"; } > "${DIR}/definitions/extras.yaml.tmp"
  mkdir -p "${DIR}/definitions/extras" && mv "${DIR}/definitions/extras.yaml.tmp" "${DIR}/definitions/extras/tags.yaml"

  { echo "- name: ${cf_name}"; echo "  type: integer"; echo "  object_types:"; echo "    - virtualization.virtualmachine"
    [ -n "${rename}" ] && echo "  rename_from: vmid_x"; } > "${DIR}/definitions/custom_fields/cf.yaml"

  { echo "- name: ${vrf_name}"; echo '  rd: "65000:10"'
    [ -n "${rename}" ] && echo "  rename_from: Managemnt"; } > "${DIR}/definitions/vrfs/v.yaml"

  { echo "- name: Site VLANs"; echo "  slug: ${vg_slug}"
    [ -n "${rename}" ] && echo "  rename_from: sitevlnas"; } > "${DIR}/definitions/vlan_groups/vg.yaml"

  { echo "- name: Storage"; echo "  vid: ${vlan_vid}"; echo "  site_slug: ${site_slug}"; echo "  status: active"
    [ -n "${rename}" ] && echo '  rename_from: "201"'; } > "${DIR}/definitions/vlans/v.yaml"

  { echo "- prefix: ${prefix}"; echo "  site_slug: ${site_slug}"; echo "  status: active"
    [ -n "${rename}" ] && echo "  rename_from: 10.77.0.0/24"; } > "${DIR}/definitions/prefixes/p.yaml"

  { echo "- model: PowerEdge R660"; echo "  slug: ${dt_slug}"; echo "  manufacturer: Dell"; echo "  u_height: 1"
    echo "  module_bays:"; echo "    - name: OCP-1"; echo '      position: "1"'
    [ -n "${rename}" ] && echo "  rename_from: r6600"; } > "${DIR}/definitions/device_types/dt.yaml"

  { echo "- model: ${mt_model}"; echo "  slug: ocp-25g"; echo "  manufacturer: Dell"
    [ -n "${rename}" ] && echo "  rename_from: OCP Mezz 25G x"; } > "${DIR}/definitions/module_types/mt.yaml"

  { echo "- name: Proxmox"; echo "  slug: ${ct_slug}"
    [ -n "${rename}" ] && echo "  rename_from: proxmoxx"; } > "${DIR}/definitions/virtualization/cluster_types/ct.yaml"

  { echo "- name: Production"; echo "  slug: ${cg_slug}"
    [ -n "${rename}" ] && echo "  rename_from: prd"; } > "${DIR}/definitions/virtualization/cluster_groups/cg.yaml"

  { echo "- name: ${cluster_name}"; echo "  type_slug: ${ct_slug}"; echo "  group_slug: ${cg_slug}"
    [ -n "${rename}" ] && echo "  rename_from: Cluster Ax"; } > "${DIR}/definitions/virtualization/clusters/c.yaml"

  { echo "- name: ${dev_name}"
    [ -n "${rename}" ] && echo "  rename_from: srv-01x"
    echo "  site_slug: ${site_slug}"; echo "  device_type_slug: ${dt_slug}"; echo "  role_slug: ${role_slug}"
    echo "  rack_slug: rack-01"; echo "  status: active"
    echo "  interfaces:"; echo "    - name: ${iface_name}"
    [ -n "${rename}" ] && echo "      rename_from: etho0"
    echo "      type: 1000base-t"
    echo "  modules:"; echo "    - name: OCP-1"; echo "      module_type_slug: ocp-25g"
  } > "${DIR}/inventory/hardware/active/d.yaml"

  { echo "- name: ${vm_name}"
    [ -n "${rename}" ] && echo "  rename_from: vm-01x"
    echo "  cluster: ${cluster_name}"; echo "  role_slug: ${role_slug}"; echo "  status: active"
    echo "  interfaces:"; echo "    - name: ${vmif_name}"
    [ -n "${rename}" ] && echo "      rename_from: eth00"
  } > "${DIR}/inventory/virtual/prod/vm.yaml"
}

echo "==> pass 1: create under the mistyped identity"
write_data before
if sync apply-before; then
  ok "initial apply"
else
  fail "initial apply" "$(grep -iE '✗|Error:' "${WORK}/apply-before.log" | head -1)"
  echo "see ${WORK}/apply-before.log"; exit 1
fi

# Pin every object's ID before renaming.
SITE_ID=$(id dcim/sites "slug=frankfrut")
RACK_ID=$(id dcim/racks "name=Rakc%2001")
ROLE_ID=$(id dcim/device-roles "slug=compyte")
PLAT_ID=$(id dcim/platforms "slug=lnux")
TG_ID=$(id tenancy/tenant-groups "slug=infar")
TENANT_ID=$(id tenancy/tenants "slug=platfrom")
TAG_ID=$(id extras/tags "slug=managd")
CF_ID=$(id extras/custom-fields "name=vmid_x")
VRF_ID=$(id ipam/vrfs "name=Managemnt")
VG_ID=$(id ipam/vlan-groups "slug=sitevlnas")
VLAN_ID=$(id ipam/vlans "vid=201")
PREFIX_ID=$(id ipam/prefixes "prefix=10.77.0.0/24")
DT_ID=$(id dcim/device-types "slug=r6600")
MT_ID=$(id dcim/module-types "model=OCP%20Mezz%2025G%20x")
CT_ID=$(id virtualization/cluster-types "slug=proxmoxx")
CG_ID=$(id virtualization/cluster-groups "slug=prd")
CLUSTER_ID=$(id virtualization/clusters "name=Cluster%20Ax")
DEV_ID=$(id dcim/devices "name=srv-01x")
IFACE_ID=$(id dcim/interfaces "name=etho0")
VM_ID=$(id virtualization/virtual-machines "name=vm-01x")
VMIF_ID=$(id virtualization/interfaces "name=eth00")
MODULE_ID=$(id dcim/modules "device_id=${DEV_ID:-0}")

missing=""
for pair in "site:${SITE_ID}" "rack:${RACK_ID}" "role:${ROLE_ID}" "platform:${PLAT_ID}" \
            "tenant-group:${TG_ID}" "tenant:${TENANT_ID}" "tag:${TAG_ID}" "custom-field:${CF_ID}" \
            "vrf:${VRF_ID}" "vlan-group:${VG_ID}" "vlan:${VLAN_ID}" "prefix:${PREFIX_ID}" \
            "device-type:${DT_ID}" "module-type:${MT_ID}" "cluster-type:${CT_ID}" \
            "cluster-group:${CG_ID}" "cluster:${CLUSTER_ID}" "device:${DEV_ID}" \
            "interface:${IFACE_ID}" "vm:${VM_ID}" "vm-interface:${VMIF_ID}"; do
  [ -n "${pair#*:}" ] || missing="${missing} ${pair%%:*}"
done
if [ -n "${missing}" ]; then
  fail "objects created" "not found after the first apply:${missing}"
  echo "see ${WORK}/apply-before.log"; exit 1
fi
ok "objects created" "21 object types under a mistyped identity"

# ---------------------------------------------------------------------------
# Pass 2: correct every identity, declaring where it came from.
# ---------------------------------------------------------------------------
echo "==> pass 2: correct every identity with rename_from"
write_data after
if sync apply-after; then
  ok "rename apply"
else
  fail "rename apply" "$(grep -iE '✗|Error:' "${WORK}/apply-after.log" | head -1)"
  echo "see ${WORK}/apply-after.log"; exit 1
fi

check "site (slug)"           dcim/sites                       "slug=frankfrut"          "slug=frankfurt"        "${SITE_ID}"
check "rack (name)"           dcim/racks                       "name=Rakc%2001"          "name=Rack%2001"        "${RACK_ID}"
check "device role (slug)"    dcim/device-roles                "slug=compyte"            "slug=compute"          "${ROLE_ID}"
check "platform (slug)"       dcim/platforms                   "slug=lnux"               "slug=linux"            "${PLAT_ID}"
check "tenant group (slug)"   tenancy/tenant-groups            "slug=infar"              "slug=infra"            "${TG_ID}"
check "tenant (slug)"         tenancy/tenants                  "slug=platfrom"           "slug=platform"         "${TENANT_ID}"
check "tag (slug)"            extras/tags                      "slug=managd"             "slug=managed"          "${TAG_ID}"
check "custom field (name)"   extras/custom-fields             "name=vmid_x"             "name=vmid"             "${CF_ID}"
check "vrf (name)"            ipam/vrfs                        "name=Managemnt"          "name=Management"       "${VRF_ID}"
check "vlan group (slug)"     ipam/vlan-groups                 "slug=sitevlnas"          "slug=site-vlans"       "${VG_ID}"
check "vlan (vid)"            ipam/vlans                       "vid=201"                 "vid=210"               "${VLAN_ID}"
check "prefix (prefix)"       ipam/prefixes                    "prefix=10.77.0.0/24"     "prefix=10.78.0.0/24"   "${PREFIX_ID}"
check "device type (slug)"    dcim/device-types                "slug=r6600"              "slug=r660"             "${DT_ID}"
check "module type (model)"   dcim/module-types                "model=OCP%20Mezz%2025G%20x" "model=OCP%20Mezz%2025G" "${MT_ID}"
check "cluster type (slug)"   virtualization/cluster-types     "slug=proxmoxx"           "slug=proxmox"          "${CT_ID}"
check "cluster group (slug)"  virtualization/cluster-groups    "slug=prd"                "slug=prod"             "${CG_ID}"
check "cluster (name)"        virtualization/clusters          "name=Cluster%20Ax"       "name=Cluster%20A"      "${CLUSTER_ID}"
check "device (name)"         dcim/devices                     "name=srv-01x"            "name=srv-01"           "${DEV_ID}"
check "interface (name)"      dcim/interfaces                  "name=etho0"              "name=eth0"             "${IFACE_ID}"
check "vm (name)"             virtualization/virtual-machines  "name=vm-01x"             "name=vm-01"            "${VM_ID}"
check "vm interface (name)"   virtualization/interfaces        "name=eth00"              "name=eth0"             "${VMIF_ID}"

# A rename must carry dependants with it rather than orphaning them: the module
# installed in the renamed device is the same object, still in the same bay.
after_module="$(id dcim/modules "device_id=${DEV_ID}")"
if [ -n "${MODULE_ID}" ] && [ "${after_module}" = "${MODULE_ID}" ]; then
  ok "module survives device rename" "id ${MODULE_ID} kept"
else
  fail "module survives device rename" "module id ${MODULE_ID:-none} → ${after_module:-none}"
fi

# ---------------------------------------------------------------------------
# Pass 3: the renamed state is the steady state.
# ---------------------------------------------------------------------------
echo "==> pass 3: idempotency after renaming"
sync apply-idem
s="$(grep -oE '[0-9]+ created, [0-9]+ updated, [0-9]+ deleted' "${WORK}/apply-idem.log" | tail -1)"
if echo "${s}" | grep -qE '^0 created, 0 updated, 0 deleted'; then
  ok "second apply is a no-op" "${s}"
else
  fail "second apply is a no-op" "${s:-no summary}"
  grep -E 'Creating|Updating' "${WORK}/apply-idem.log" | head -5
fi

# Dropping the now-applied rename_from must change nothing at all.
sed -i '/rename_from/d' $(find "${DIR}" -name '*.yaml')
sync apply-dropped
s="$(grep -oE '[0-9]+ created, [0-9]+ updated, [0-9]+ deleted' "${WORK}/apply-dropped.log" | tail -1)"
if echo "${s}" | grep -qE '^0 created, 0 updated, 0 deleted'; then
  ok "no-op after dropping rename_from" "${s}"
else
  fail "no-op after dropping rename_from" "${s:-no summary}"
fi

# Nothing should have been left behind for --prune to reap.
"${BIN}" --data-dir "${DIR}" --prune >"${WORK}/prune.log" 2>&1
s="$(grep -oE '[0-9]+ created, [0-9]+ updated, [0-9]+ deleted' "${WORK}/prune.log" | tail -1)"
if echo "${s}" | grep -qE ', 0 deleted'; then
  ok "rename left no orphans" "${s}"
else
  fail "rename left no orphans" "${s:-no summary}"
fi

wipe
echo
if [ "${FAILURES}" -eq 0 ]; then
  echo "==> all rename checks passed"
else
  echo "==> ${FAILURES} rename check(s) failed (work dir: ${WORK})"
  exit 1
fi
[ "${E2E_KEEP:-0}" = "1" ] || rm -rf "${WORK}"
