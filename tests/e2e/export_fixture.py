#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Fixture for tests/e2e/export.sh: a NetBox filled in by hand.

  seed      create the objects through the API, none carrying the gitops tag
  snapshot  write every object of every endpoint the export reads to a file
  compare   compare two snapshots: same objects, same fields, tags only gained
  cleanup   delete what `seed` created (and whatever the sync tagged with it)

Everything goes through the REST API directly, never through the controller:
the point is objects the controller did not make.
"""
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

URL = os.environ["NETBOX_URL"].rstrip("/")
TOKEN = os.environ["NETBOX_TOKEN"]
AUTH = ("Bearer " if TOKEN.startswith("nbt_") else "Token ") + TOKEN
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))

# Everything the export reads, plus what the sync could create as a side
# effect (manufacturers, module bays, MAC addresses).
ENDPOINTS = [
    "extras/tags", "extras/custom-fields",
    "dcim/device-roles", "dcim/platforms", "dcim/manufacturers",
    "tenancy/tenant-groups", "tenancy/tenants",
    "dcim/sites", "dcim/racks",
    "ipam/vrfs", "ipam/vlan-groups", "ipam/vlans", "ipam/prefixes",
    "dcim/device-types", "dcim/module-types",
    "dcim/interface-templates", "dcim/front-port-templates", "dcim/rear-port-templates",
    "dcim/module-bay-templates", "dcim/device-bay-templates",
    "virtualization/cluster-types", "virtualization/cluster-groups", "virtualization/clusters",
    "dcim/devices", "dcim/interfaces", "dcim/front-ports", "dcim/rear-ports",
    "dcim/module-bays", "dcim/modules", "dcim/cables", "ipam/ip-addresses",
    "dcim/mac-addresses",
    "virtualization/virtual-machines", "virtualization/interfaces",
]

# Fields NetBox changes on its own when an object is saved, or that render
# other objects. A difference there says nothing about what the sync sent.
VOLATILE = {"last_updated", "display", "display_url", "url", "tags", "created",
            "tagged_items"}  # a tag's usage counter: the gitops tag's rises


def call(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(f"{URL}/api/{path}", data=data, method=method,
                                 headers={"Authorization": AUTH, "Content-Type": "application/json",
                                          "Accept": "application/json"})
    try:
        with OPENER.open(req) as r:
            raw = r.read()
            return json.loads(raw) if raw else None
    except urllib.error.HTTPError as e:
        sys.exit(f"{method} {path} {body}: HTTP {e.code}: {e.read().decode()[:500]}")


def post(ep, **body):
    return call("POST", f"{ep}/", body)


def patch(ep, oid, **body):
    return call("PATCH", f"{ep}/{oid}/", body)


def list_all(ep, query="limit=1000"):
    out, path = [], f"{ep}/?{query}"
    while path:
        page = call("GET", path)
        out += page["results"]
        nxt = page.get("next")
        path = nxt.split("/api/", 1)[1] if nxt else None
    return out


def one(ep, query):
    query = urllib.parse.urlencode(dict(p.split("=", 1) for p in query.split("&")))
    r = list_all(ep, query)
    if len(r) != 1:
        sys.exit(f"{ep}?{query}: expected one object, found {len(r)}")
    return r[0]


def require_empty():
    """Refuse to touch an instance that holds anything already.

    This test creates objects without the gitops tag and deletes every object
    afterwards, untagged ones included. Against a real NetBox that would be a
    disaster, so it only ever runs against one that starts out empty.
    """
    for ep in ("dcim/sites", "dcim/devices", "virtualization/virtual-machines", "ipam/prefixes"):
        if call("GET", f"{ep}/?limit=1")["count"]:
            sys.exit(f"{URL} is not empty ({ep} has objects). This test deletes untagged objects; "
                     "run it only against a throwaway NetBox (tests/e2e/provision-local.sh).")


def seed():
    require_empty()
    prod = post("extras/tags", name="Production", slug="prod", color="ff0000")
    backup = post("extras/tags", name="Backup", slug="backup", color="00ff00", description="nightly")
    post("extras/custom-fields", name="owner", type="text", object_types=["dcim.device"], label="Owner")
    post("extras/custom-fields", name="vmid", type="integer", object_types=["virtualization.virtualmachine"])

    server = post("dcim/device-roles", name="Server", slug="server", color="0000ff", vm_role=True)
    switch = post("dcim/device-roles", name="Switch", slug="switch", color="00ffff")
    pprole = post("dcim/device-roles", name="Patch Panel", slug="patch-panel", color="aaaaaa")
    dell = post("dcim/manufacturers", name="Dell", slug="dell")
    ubuntu = post("dcim/platforms", name="Ubuntu 24.04", slug="ubuntu-24-04")
    tg = post("tenancy/tenant-groups", name="Internal", slug="internal")
    tenant = post("tenancy/tenants", name="Platform", slug="platform", group=tg["id"], tags=[prod["id"]])

    site = post("dcim/sites", name="Berlin DC", slug="berlin-dc", status="active",
                time_zone="Europe/Berlin", description="main site", tags=[prod["id"]])
    lab = post("dcim/sites", name="Lab", slug="lab", status="planned")
    rack = post("dcim/racks", name="Rack A01", site=site["id"], status="active", u_height=42, tags=[backup["id"]])

    vrf = post("ipam/vrfs", name="prod", rd="65000:1", enforce_unique=True)
    vg = post("ipam/vlan-groups", name="Berlin VLANs", slug="berlin-vlans",
              scope_type="dcim.site", scope_id=site["id"], vid_ranges=[[1, 4094]])
    mgmt = post("ipam/vlans", name="mgmt", vid=100, site=site["id"], group=vg["id"], tags=[prod["id"]])
    data = post("ipam/vlans", name="data", vid=200, site=site["id"], group=vg["id"], status="reserved")
    post("ipam/vlans", name="lab-net", vid=300, site=lab["id"])
    post("ipam/prefixes", prefix="10.0.0.0/24", scope_type="dcim.site", scope_id=site["id"],
         vrf=vrf["id"], vlan=mgmt["id"], description="mgmt net")
    post("ipam/prefixes", prefix="10.9.0.0/16", status="container", is_pool=False)

    # Device types; creating a device instantiates their templates.
    r740 = post("dcim/device-types", model="PowerEdge R740", slug="r740", manufacturer=dell["id"],
                u_height=2, is_full_depth=True, part_number="R740-X", tags=[prod["id"]])
    for n in ("eth0", "eth1", "eth2", "eth3"):
        post("dcim/interface-templates", device_type=r740["id"], name=n, type="10gbase-t")
    post("dcim/interface-templates", device_type=r740["id"], name="idrac", type="1000base-t", mgmt_only=True)
    post("dcim/module-bay-templates", device_type=r740["id"], name="PCIe 1", position="1")
    sw = post("dcim/device-types", model="S5248F", slug="s5248f", manufacturer=dell["id"], u_height=1)
    for n in ("Eth1/1", "Eth1/2", "Eth1/3", "Eth1/10"):
        post("dcim/interface-templates", device_type=sw["id"], name=n, type="25gbase-x-sfp28")
    pp12 = post("dcim/device-types", model="PP-12", slug="pp-12", manufacturer=dell["id"], u_height=1, is_full_depth=False)
    rpt = post("dcim/rear-port-templates", device_type=pp12["id"], name="R1", type="mpo", positions=12)
    for i in (1, 2):
        post("dcim/front-port-templates", device_type=pp12["id"], name=str(i), type="lc",
             rear_ports=[{"position": 1, "rear_port": rpt["id"], "rear_port_position": i}])
    gpu = post("dcim/module-types", model="A100 GPU", manufacturer=dell["id"], part_number="A100")
    post("dcim/interface-templates", module_type=gpu["id"], name="gpu{module}", type="other")

    srv = post("dcim/devices", name="srv-01", site=site["id"], role=server["id"], device_type=r740["id"],
               rack=rack["id"], position=10, face="front", serial="ABC123", asset_tag="INV-1",
               platform=ubuntu["id"], tenant=tenant["id"], tags=[prod["id"], backup["id"]],
               custom_fields={"owner": "team-a"}, description="database host")
    swd = post("dcim/devices", name="sw-01", site=site["id"], role=switch["id"], device_type=sw["id"],
               rack=rack["id"], position=40, face="rear", status="planned")
    pp = post("dcim/devices", name="pp-01", site=site["id"], role=pprole["id"], device_type=pp12["id"],
              rack=rack["id"], position=42, face="front")
    post("dcim/devices", name="lab-srv", site=lab["id"], role=server["id"], device_type=r740["id"], status="offline")

    def iface(dev, name):
        return one("dcim/interfaces", f"device_id={dev['id']}&name={name}")

    e0 = iface(srv, "eth0")
    patch("dcim/interfaces", e0["id"], mtu=9000, mode="access", untagged_vlan=mgmt["id"], description="uplink")
    bond = post("dcim/interfaces", device=srv["id"], name="bond0", type="lag", mode="tagged",
                tagged_vlans=[mgmt["id"], data["id"]])
    patch("dcim/interfaces", iface(srv, "eth1")["id"], lag=bond["id"], tags=[backup["id"]])
    patch("dcim/interfaces", iface(srv, "eth2")["id"], lag=bond["id"])
    patch("dcim/interfaces", iface(srv, "eth3")["id"], enabled=False)
    post("dcim/interfaces", device=srv["id"], name="usb0", type="virtual")  # not in the template

    ip = post("ipam/ip-addresses", address="10.0.0.10/24", vrf=vrf["id"], dns_name="srv-01.example.net",
              assigned_object_type="dcim.interface", assigned_object_id=e0["id"])
    patch("dcim/devices", srv["id"], primary_ip4=ip["id"])
    # A second address on the same interface: only one can be declared.
    post("ipam/ip-addresses", address="10.0.0.11/24", vrf=vrf["id"], status="reserved",
         assigned_object_type="dcim.interface", assigned_object_id=e0["id"])
    # In the global table, inside the VRF's prefix: must not be declared.
    post("ipam/ip-addresses", address="10.0.0.20/24",
         assigned_object_type="dcim.interface", assigned_object_id=iface(swd, "Eth1/3")["id"])

    bay = one("dcim/module-bays", f"device_id={srv['id']}&name=PCIe 1")
    post("dcim/modules", device=srv["id"], module_bay=bay["id"], module_type=gpu["id"], serial="GPU1")

    sw1, sw2 = iface(swd, "Eth1/1"), iface(swd, "Eth1/2")
    patch("dcim/interfaces", sw2["id"], enabled=False, description="to panel")
    fp1 = one("dcim/front-ports", f"device_id={pp['id']}&name=1")
    # Switch side as A: the export has to choose the end itself.
    post("dcim/cables", a_terminations=[{"object_type": "dcim.interface", "object_id": sw1["id"]}],
         b_terminations=[{"object_type": "dcim.interface", "object_id": e0["id"]}],
         type="cat6a", color="0000ff", length=3, length_unit="m", label="C-001")
    post("dcim/cables", a_terminations=[{"object_type": "dcim.frontport", "object_id": fp1["id"]}],
         b_terminations=[{"object_type": "dcim.interface", "object_id": sw2["id"]}], type="mmf-om4")

    ct = post("virtualization/cluster-types", name="Proxmox", slug="proxmox")
    cg = post("virtualization/cluster-groups", name="Prod", slug="prod-clusters")
    cl = post("virtualization/clusters", name="pve-berlin", type=ct["id"], group=cg["id"],
              scope_type="dcim.site", scope_id=site["id"], tenant=tenant["id"])
    vm = post("virtualization/virtual-machines", name="web-01", cluster=cl["id"], site=site["id"],
              role=server["id"], platform=ubuntu["id"], tenant=tenant["id"], vcpus=4, memory=8192,
              disk=100, tags=[prod["id"]], custom_fields={"vmid": 101})
    vif = post("virtualization/interfaces", virtual_machine=vm["id"], name="eth0", mode="access",
               untagged_vlan=data["id"], mtu=1500)
    mac = post("dcim/mac-addresses", mac_address="AA:BB:CC:DD:EE:01",
               assigned_object_type="virtualization.vminterface", assigned_object_id=vif["id"])
    patch("virtualization/interfaces", vif["id"], primary_mac_address=mac["id"])
    vip = post("ipam/ip-addresses", address="10.0.1.5/24", assigned_object_type="virtualization.vminterface",
               assigned_object_id=vif["id"])
    patch("virtualization/virtual-machines", vm["id"], primary_ip4=vip["id"])
    post("virtualization/virtual-machines", name="standalone", site=lab["id"], status="staged")
    print("seeded")


def snapshot(path):
    snap = {ep: {str(o["id"]): o for o in list_all(ep)} for ep in ENDPOINTS}
    with open(path, "w") as f:
        json.dump(snap, f, indent=1, sort_keys=True)
    print(f"{sum(len(v) for v in snap.values())} objects")


def tag_slugs(o):
    return sorted(t["slug"] for t in o.get("tags") or [])


def compare(before_path, after_path):
    before, after = json.load(open(before_path)), json.load(open(after_path))
    problems, adopted = [], 0
    for ep in ENDPOINTS:
        b, a = before.get(ep, {}), after.get(ep, {})
        for oid in sorted(set(a) - set(b), key=int):
            # The controller's own tag, on a NetBox it never wrote to before.
            if ep == "extras/tags" and a[oid].get("slug") == "gitops":
                continue
            problems.append(f"{ep} {oid} ({a[oid].get('display')}) was CREATED")
        for oid in sorted(set(b) - set(a), key=int):
            problems.append(f"{ep} {oid} ({b[oid].get('display')}) was DELETED")
        for oid in sorted(set(a) & set(b), key=int):
            ob, oa = b[oid], a[oid]
            for k in sorted(set(ob) | set(oa)):
                if k in VOLATILE:
                    continue
                if ob.get(k) != oa.get(k):
                    problems.append(f"{ep} {oid} ({ob.get('display')}): {k}: {json.dumps(ob.get(k))[:120]} -> {json.dumps(oa.get(k))[:120]}")
            if "tags" in ob:
                tb, ta = tag_slugs(ob), tag_slugs(oa)
                lost = set(tb) - set(ta)
                if lost:
                    problems.append(f"{ep} {oid} ({ob.get('display')}) LOST tags {sorted(lost)}")
                if set(ta) - set(tb) - {"gitops"}:
                    problems.append(f"{ep} {oid} ({ob.get('display')}) gained tags {sorted(set(ta) - set(tb))}")
                if "gitops" in ta and "gitops" not in tb:
                    adopted += 1
    for p in problems:
        print(f"    {p}")
    print(f"adopted={adopted} problems={len(problems)}")
    return 1 if problems else 0


def untagged(snapshot_path, ep, field, value):
    """Exit 0 if the object is in the snapshot without the gitops tag."""
    snap = json.load(open(snapshot_path))
    for o in snap[ep].values():
        if o.get(field) == value:
            return 0 if "gitops" not in tag_slugs(o) else 1
    return 2


def cleanup():
    # Only ever called by export.sh after `seed` verified the instance was
    # empty, so everything in it is this test's. Reverse dependency order.
    if os.environ.get("E2E_EXPORT_OWNS_INSTANCE") != "1":
        sys.exit("cleanup deletes every object; export.sh sets E2E_EXPORT_OWNS_INSTANCE=1 once it knows the instance is its own")
    order = [
        ("dcim/cables", None), ("ipam/ip-addresses", None), ("dcim/mac-addresses", None),
        ("virtualization/interfaces", None), ("virtualization/virtual-machines", None),
        ("dcim/modules", None), ("dcim/devices", None),
        ("virtualization/clusters", None), ("virtualization/cluster-groups", None),
        ("virtualization/cluster-types", None),
        ("dcim/module-types", None), ("dcim/device-types", None),
        ("ipam/prefixes", None), ("ipam/vlans", None), ("ipam/vlan-groups", None), ("ipam/vrfs", None),
        ("dcim/racks", None), ("dcim/sites", None), ("tenancy/tenants", None), ("tenancy/tenant-groups", None),
        ("dcim/platforms", None), ("dcim/manufacturers", None), ("dcim/device-roles", None),
        ("extras/custom-fields", None), ("extras/tags", None),
    ]
    for ep, _ in order:
        for o in list_all(ep):
            if ep == "extras/tags" and o["slug"] == "gitops":
                continue
            call("DELETE", f"{ep}/{o['id']}/")
    print("clean")


if __name__ == "__main__":
    cmd = sys.argv[1]
    if cmd == "seed":
        seed()
    elif cmd == "snapshot":
        snapshot(sys.argv[2])
    elif cmd == "compare":
        sys.exit(compare(sys.argv[2], sys.argv[3]))
    elif cmd == "untagged":
        sys.exit(untagged(*sys.argv[2:6]))
    elif cmd == "cleanup":
        cleanup()
    else:
        sys.exit(f"unknown command {cmd}")
