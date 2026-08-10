#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Generate a random but valid netbox-gitops dataset using Dell hardware.

Device types are split at random between the project's native format
(definitions/device_types/*.yaml, a YAML list, underscored keys) and the
community device type library layout (definitions/device_type_library/
Dell/<model>.yaml, one document per file, hyphenated keys), so both loader
paths and their merge are exercised on every seed.
"""
import json, os, random, re, shutil, sys, yaml

IFACE = ["1000base-t","10gbase-t","25gbase-x-sfp28","10gbase-x-sfpp","100gbase-x-qsfp28"]
PORTT = ["8p8c","lc","sc","mpo"]
CABLE = ["cat6","cat6a","mmf-om4","smf-os2","dac-passive"]
STATUS = ["active","planned","staged","offline"]

# Real Dell hardware. (name, part, u_height, [(iface_name, type, mgmt_only)])
DELL_SERVERS = [
    ("PowerEdge R640","R640",1,[("iDRAC","1000base-t",True),("NIC1","25gbase-x-sfp28",False),("NIC2","25gbase-x-sfp28",False)]),
    ("PowerEdge R650","R650",1,[("iDRAC","1000base-t",True),("NIC1","25gbase-x-sfp28",False),("NIC2","25gbase-x-sfp28",False)]),
    ("PowerEdge R660","R660",1,[("iDRAC","1000base-t",True),("NIC1","25gbase-x-sfp28",False)]),
    ("PowerEdge R740","R740",2,[("iDRAC","1000base-t",True),("NIC1","10gbase-t",False),("NIC2","10gbase-t",False)]),
    ("PowerEdge R750","R750",2,[("iDRAC","1000base-t",True),("NIC1","25gbase-x-sfp28",False)]),
    ("PowerEdge R7615","R7615",2,[("iDRAC","1000base-t",True),("NIC1","25gbase-x-sfp28",False)]),
    ("PowerEdge XE9680","XE9680",6,[("iDRAC","1000base-t",True)]+[(f"NIC{i}","100gbase-x-qsfp28",False) for i in range(1,5)]),
]
DELL_SWITCHES = [
    ("PowerSwitch S4148F-ON","S4148F-ON",1,48,"10gbase-x-sfpp"),
    ("PowerSwitch S5248F-ON","S5248F-ON",1,48,"25gbase-x-sfp28"),
    ("PowerSwitch S5232F-ON","S5232F-ON",1,32,"100gbase-x-qsfp28"),
    ("PowerSwitch N3248TE-ON","N3248TE-ON",1,48,"1000base-t"),
]
DELL_CHASSIS = ("PowerEdge MX7000","MX7000",7,8)
DELL_SLED    = ("PowerEdge MX750c","MX750c")
DELL_PANEL   = ("Dell Networking Patch Panel 24","PP-24",1)

def w(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path,"w") as f:
        yaml.safe_dump(data, f, sort_keys=False, allow_unicode=True, default_flow_style=False)

def slugify(s):
    """Mirror of utils.Slugify, for predicting a library module type's key."""
    return re.sub(r"-+", "-", re.sub(r"[^a-z0-9]+", "-", s.lower())).strip("-")

def to_library(dt, kind="device"):
    """Translate a native type dict into the community library shape.

    Module types have no slug in the library schema — NetBox gives them none,
    and the loader derives the local reference key from the model — so it is
    dropped here rather than emitted as a field a strict reader would reject.
    """
    out = {"manufacturer": dt["manufacturer"], "model": dt["model"]}
    if kind == "device":
        out["slug"] = dt["slug"]
    for k in ("part_number","u_height","is_full_depth","subdevice_role","airflow",
              "description","comments","weight","weight_unit"):
        if k in dt: out[k] = dt[k]
    for native, hyphen in (("interfaces","interfaces"),("console_ports","console-ports"),
                           ("power_ports","power-ports"),("power_outlets","power-outlets"),
                           ("rear_ports","rear-ports"),("front_ports","front-ports"),
                           ("module_bays","module-bays"),("device_bays","device-bays")):
        if dt.get(native): out[hyphen] = dt[native]
    return out

def gen(root, seed):
    rnd = random.Random(seed)
    shutil.rmtree(root, ignore_errors=True)
    D, I = f"{root}/definitions", f"{root}/inventory"

    w(f"{D}/extras/tags.yaml", [{"name":"GitOps Managed","slug":"gitops","color":"2196f3"}])
    roles = [{"name":n,"slug":s,"color":f"{rnd.randrange(16**6):06x}","vm_role":vm}
             for n,s,vm in [("Compute","compute",True),("Leaf Switch","leaf-switch",False),
                            ("Spine Switch","spine-switch",False),("Patch Panel","patch-panel",False)]]
    w(f"{D}/roles/roles.yaml", roles)
    w(f"{D}/platforms/platforms.yaml", [{"name":"Dell iDRAC","slug":"dell-idrac"},
                                        {"name":"Dell SONiC","slug":"dell-sonic"}])
    w(f"{D}/tenant_groups/tenant_groups.yaml", [{"name":"Infrastructure","slug":"infrastructure"}])
    w(f"{D}/tenants/tenants.yaml", [{"name":"Platform Team","slug":"platform-team","group_slug":"infrastructure"}])

    # ---- sites / racks ----------------------------------------------------
    nsites = rnd.randint(2,4)
    sites, racks = [], []
    for s in range(nsites):
        sites.append({"name":rnd.choice([f"Frankfurt DC {s}",f"München Süd {s}",f"Zürich {s}",f"Berlin DC {s}"]),
                      "slug":f"site-{s}","status":"active"})
        for r in range(rnd.randint(1,3)):
            racks.append({"name":f"Rack {s}-{r:02d}","slug":f"rack-{s}-{r}","site_slug":f"site-{s}",
                          "u_height":rnd.choice([42,45,47]),"width":rnd.choice([19,23]),"status":"active"})
    rnd.shuffle(sites); rnd.shuffle(racks)
    w(f"{D}/sites/sites.yaml", sites)
    h = len(racks)//2 or 1
    w(f"{D}/racks/a.yaml", racks[:h])
    if racks[h:]: w(f"{D}/racks/b.yaml", racks[h:])

    # ---- vrfs / vlans / prefixes -----------------------------------------
    vrfs=[{"name":n,"rd":rd,"enforce_unique":True} for n,rd in
          [("Management","65000:10"),("Production","65000:100"),("Storage","65000:200")][:rnd.randint(1,3)]]
    w(f"{D}/vrfs/vrfs.yaml", vrfs)
    vlans, prefixes, vid = [], [], 100
    for s in range(nsites):
        for v in range(rnd.randint(1,4)):
            vlans.append({"name":f"VLAN-{s}-{v}","vid":vid,"site_slug":f"site-{s}","status":"active"})
            pfx={"prefix":f"10.{s}.{v}.0/24","site_slug":f"site-{s}","vlan_name":f"VLAN-{s}-{v}","status":"active"}
            if rnd.random()<0.6: pfx["vrf_name"]=rnd.choice(vrfs)["name"]
            prefixes.append(pfx); vid+=1
    rnd.shuffle(vlans); rnd.shuffle(prefixes)
    w(f"{D}/vlans/vlans.yaml", vlans)
    w(f"{D}/prefixes/prefixes.yaml", prefixes)

    # ---- Dell device types ------------------------------------------------
    dts=[]
    # The first server type always gets module bays: which types get them is
    # otherwise random, and a seed that gave none would silently drop module
    # coverage — the module path fails by warning and skipping, so an empty
    # result still looks like a clean, idempotent run.
    for idx,(model,part,uh,ifaces) in enumerate(rnd.sample(DELL_SERVERS, rnd.randint(2,4))):
        dt={"model":model,"slug":"dell-"+part.lower(),"manufacturer":"Dell","part_number":part,
            "u_height":uh,"is_full_depth":True,"airflow":"front-to-rear",
            "interfaces":[{"name":n,"type":t,**({"mgmt_only":True} if m else {})} for n,t,m in ifaces],
            "console_ports":[{"name":"Serial","type":"rj-45"}],
            "power_ports":[{"name":f"PSU{i}","type":"iec-60320-c14","maximum_draw":rnd.choice([750,1100])} for i in (1,2)]}
        if idx==0 or rnd.random()<0.4:
            dt["module_bays"]=([{"name":f"OCP-{i}","position":str(i)} for i in (1,2)]
                               +[{"name":f"PSU-BAY-{i}","position":str(i)} for i in (1,2)])
        dts.append(dt)
    for model,part,uh,nports,itype in rnd.sample(DELL_SWITCHES, rnd.randint(1,3)):
        dts.append({"model":model,"slug":"dell-"+part.lower(),"manufacturer":"Dell","part_number":part,
                    "u_height":uh,"is_full_depth":False,"airflow":"front-to-rear",
                    "interfaces":[{"name":f"Ethernet{i}","type":itype} for i in range(1, min(nports,12)+1)]
                                 +[{"name":"ManagementEthernet","type":"1000base-t","mgmt_only":True}],
                    "console_ports":[{"name":"Console","type":"rj-45"}],
                    "power_ports":[{"name":f"PSU{i}","type":"iec-60320-c14"} for i in (1,2)]})
    cm,cp,cu,cbays = DELL_CHASSIS
    dts.append({"model":cm,"slug":"dell-"+cp.lower(),"manufacturer":"Dell","part_number":cp,"u_height":cu,
                "is_full_depth":True,"subdevice_role":"parent",
                "device_bays":[{"name":f"Slot-{i}"} for i in range(1,cbays+1)],
                "power_ports":[{"name":f"PSU{i}","type":"iec-60320-c20"} for i in range(1,4)]})
    sm,sp = DELL_SLED
    dts.append({"model":sm,"slug":"dell-"+sp.lower(),"manufacturer":"Dell","part_number":sp,"u_height":0,
                "subdevice_role":"child",
                "interfaces":[{"name":"iDRAC","type":"1000base-t","mgmt_only":True},
                              {"name":"NIC1","type":"25gbase-x-sfp28"}]})
    pm,pp,pu = DELL_PANEL
    npp = rnd.randint(4,10)
    dts.append({"model":pm,"slug":"dell-"+pp.lower(),"manufacturer":"Dell","part_number":pp,"u_height":pu})

    # ---- Dell module types -------------------------------------------------
    # Components carry the literal "{module}" placeholder that NetBox expands to
    # the bay position at install time; it must survive the round trip verbatim.
    module_types=[
        {"model":"Dell OCP 25GbE Mezz","slug":"dell-ocp-25g","manufacturer":"Dell",
         "part_number":"OCP-25G-2P","weight":0.3,"weight_unit":"kg",
         "interfaces":[{"name":f"{{module}}-25GbE-{i}","type":"25gbase-x-sfp28"} for i in range(2)]},
        {"model":"Dell OCP 10GbE Mezz","slug":"dell-ocp-10g","manufacturer":"Dell",
         "part_number":"OCP-10G-4P","description":"Quad-port OCP 3.0 mezzanine",
         "interfaces":[{"name":f"{{module}}-10GbE-{i}","type":"10gbase-x-sfpp",
                        **({"enabled":False} if i==3 else {})} for i in range(4)]},
        {"model":"Dell PSU 1100W","slug":"dell-psu-1100","manufacturer":"Dell",
         "part_number":"PSU-1100-AC","airflow":"front-to-rear",
         "power_ports":[{"name":"{module}-Input","type":"iec-60320-c14","maximum_draw":1100}],
         "power_outlets":[{"name":"{module}-Output","type":"iec-60320-c13","power_port":"{module}-Input"}]},
    ]
    # Split module types across the native format and the community library
    # layout, exactly as the device types are, so both readers stay covered.
    rnd.shuffle(module_types)
    msplit = rnd.randint(1, len(module_types)-1)
    mt_native, mt_library = module_types[:msplit], module_types[msplit:]
    w(f"{D}/module_types/module_types.yaml", mt_native)
    for mt in mt_library:
        w(f"{D}/module_type_library/Dell/{mt['slug']}.yaml", to_library(mt, "module"))
    # A native module type is referenced by its declared slug; a library one by
    # the slug the loader derives from the model. Devices must use the right key
    # for the format the type actually came from.
    def mt_key(mt):
        return mt["slug"] if mt in mt_native else slugify(mt["model"])
    module_slugs=[mt_key(mt) for mt in module_types]
    # Bays are filled with a module that plausibly belongs in them, so the
    # fixture stays readable as hardware rather than as a random pairing.
    mt_slug_by_kind={
        "psu":[mt_key(mt) for mt in module_types if "PSU" in mt["model"]],
        "ocp":[mt_key(mt) for mt in module_types if "OCP" in mt["model"]],
    }

    # Split between the native format and the community library layout.
    rnd.shuffle(dts)
    split = rnd.randint(1, len(dts)-1)
    native, library = dts[:split], dts[split:]
    w(f"{D}/device_types/dell.yaml", native)
    for dt in library:
        w(f"{D}/device_type_library/Dell/{dt['slug']}.yaml", to_library(dt))
    # Occasionally shadow one library entry with a local override of the same
    # slug, so the merge precedence is exercised too.
    if library and rnd.random() < 0.4:
        dup = dict(library[0]); dup["description"] = "local override"
        w(f"{D}/device_types/override.yaml", [dup])

    by_slug = {d["slug"]: d for d in dts}
    server_slugs = ["dell-"+p.lower() for _,p,_,_ in DELL_SERVERS if "dell-"+p.lower() in by_slug]
    switch_slugs = ["dell-"+p.lower() for _,p,_,_,_ in DELL_SWITCHES if "dell-"+p.lower() in by_slug]

    # ---- devices ----------------------------------------------------------
    devices, used = [], {}
    def place(rack_slug, height):
        u=used.setdefault(rack_slug,set())
        for _ in range(80):
            p=rnd.randint(1,34); span={p+k for k in range(max(1,int(height)))}
            if not span & u: u|=span; return p
        return None
    rack_by_site={}
    for r in racks: rack_by_site.setdefault(r["site_slug"],[]).append(r)

    n=0
    for s in range(nsites):
        rl=rack_by_site.get(f"site-{s}",[])
        if not rl: continue
        site_vlans=[v for v in vlans if v["site_slug"]==f"site-{s}"]
        for _ in range(rnd.randint(2,5)):
            slug=rnd.choice(server_slugs+switch_slugs); dt=by_slug[slug]
            rack=rnd.choice(rl); pos=place(rack["slug"], dt.get("u_height",1))
            if pos is None: continue
            is_switch = slug in switch_slugs
            dev={"name":("sw" if is_switch else "srv")+f"-{s}-{n:02d}","site_slug":f"site-{s}",
                 "role_slug":("leaf-switch" if is_switch else "compute"),
                 "device_type_slug":slug,"rack_slug":rack["slug"],"position":pos,
                 "face":"front","status":rnd.choice(STATUS)}
            if rnd.random()<0.6: dev["serial"]=f"CN{rnd.randrange(10**7):07d}"
            ifs=[]
            for j,t in enumerate(dt.get("interfaces",[])[:4]):
                cfg={"name":t["name"]}
                if rnd.random()<0.6: cfg["ip"]=f"10.{s}.{rnd.randint(0,3)}.{20+n*4+j}/24"
                if rnd.random()<0.4 and site_vlans:
                    cfg["mode"]="access"; cfg["untagged_vlan"]=rnd.choice(site_vlans)["name"]
                if rnd.random()<0.3: cfg["mtu"]=rnd.choice([1500,9000])
                if len(cfg)>1: ifs.append(cfg)
            if ifs: dev["interfaces"]=ifs
            # Install modules into whatever module bays this type declares. The
            # module's name is the bay it goes into; its components arrive from
            # the module type with "{module}" expanded to the bay position.
            bays=dt.get("module_bays") or []
            if bays and rnd.random()<0.7:
                mods=[]
                for b in bays:
                    if rnd.random()>=0.8: continue
                    kind = "psu" if b["name"].startswith("PSU") else "ocp"
                    choices = mt_slug_by_kind[kind] or module_slugs
                    m={"name":b["name"],"module_type_slug":rnd.choice(choices)}
                    if rnd.random()<0.5: m["serial"]=f"MZ{rnd.randrange(10**6):06d}"
                    mods.append(m)
                if mods: dev["modules"]=mods
            devices.append(dev); n+=1

        if rnd.random()<0.7:
            rack=rl[0]; pos=place(rack["slug"], cu)
            if pos:
                ch={"name":f"mx7000-{s}","site_slug":f"site-{s}","role_slug":"compute",
                    "device_type_slug":"dell-"+cp.lower(),"rack_slug":rack["slug"],"position":pos,"face":"front"}
                sleds=[{"name":f"mx750c-{s}-{b}","site_slug":f"site-{s}","role_slug":"compute",
                        "device_type_slug":"dell-"+sp.lower(),"parent_device":f"mx7000-{s}",
                        "device_bay":f"Slot-{b+1}"} for b in range(rnd.randint(1,3))]
                devices.extend(sleds+[ch] if rnd.random()<0.5 else [ch]+sleds)

        if s==0:
            rack=rl[0]; pos=place(rack["slug"], pu)
            if pos:
                rp=[{"name":f"R{i}","type":rnd.choice(PORTT),"positions":1} for i in range(1,npp+1)]
                fp=[{"name":f"F{i}","type":rp[i-1]["type"],"rear_port":f"R{i}","rear_port_position":1}
                    for i in range(1,npp+1)]
                devices.append({"name":f"panel-{s}","site_slug":f"site-{s}","role_slug":"patch-panel",
                                "device_type_slug":"dell-"+pp.lower(),"rack_slug":rack["slug"],
                                "position":pos,"face":"front","rear_ports":rp,"front_ports":fp})

    # Same reasoning as the bays above: make sure at least one device really
    # installs a module, whatever the seed rolled.
    if not any(d.get("modules") for d in devices):
        for d in devices:
            bays=by_slug.get(d.get("device_type_slug"),{}).get("module_bays") or []
            if bays:
                d["modules"]=[{"name":bays[0]["name"],"module_type_slug":rnd.choice(module_slugs)}]
                break

    panels=[d for d in devices if d["name"].startswith("panel-")]
    if panels:
        p=panels[0]; free=[f["name"] for f in p["front_ports"]]
        for d in devices:
            if not free or not d["name"].startswith(("srv-0-","sw-0-")) or "interfaces" not in d: continue
            d["interfaces"][0]["link"]={"peer_device":p["name"],"peer_port":free.pop(0),
                                        "cable_type":rnd.choice(CABLE)}

    rnd.shuffle(devices)
    h=len(devices)//2 or 1
    w(f"{I}/hardware/active/a.yaml", devices[:h])
    if devices[h:]: w(f"{I}/hardware/active/b.yaml", devices[h:])

    # ---- virtualization ---------------------------------------------------
    w(f"{D}/virtualization/cluster_types/ct.yaml", [{"name":"Dell VxRail","slug":"dell-vxrail"}])
    w(f"{D}/virtualization/cluster_groups/cg.yaml", [{"name":"Production","slug":"production"}])
    clusters=[{"name":f"vxrail-{s}","type_slug":"dell-vxrail","group_slug":"production","site_slug":f"site-{s}"}
              for s in range(min(2,nsites))]
    w(f"{D}/virtualization/clusters/cl.yaml", clusters)
    vms=[]
    for i in range(rnd.randint(1,4)):
        c=rnd.choice(clusters)
        vm={"name":f"vm-{i}","cluster":c["name"],"role_slug":"compute","status":"active",
            "vcpus":rnd.choice([1,2,4,8]),"memory":rnd.choice([1024,2048,4096]),"disk":rnd.choice([20,40,80])}
        if rnd.random()<0.6: vm["interfaces"]=[{"name":"eth0","ip":f"10.9.9.{10+i}/24"}]
        vms.append(vm)
    w(f"{I}/virtual/prod/vms.yaml", vms)

    return dict(sites=len(sites),racks=len(racks),device_types=len(dts),
                native=len(native),library=len(library),devices=len(devices),
                module_types=len(module_types),mt_native=len(mt_native),mt_library=len(mt_library),
                modules=sum(len(d.get('modules',[])) for d in devices),
                vlans=len(vlans),prefixes=len(prefixes),vms=len(vms))

if __name__=="__main__":
    print(json.dumps(gen(sys.argv[1], int(sys.argv[2]))))
