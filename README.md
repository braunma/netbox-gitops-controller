# NetBox GitOps Controller

This Go tool enables **declarative management** (Infrastructure as Code) for a NetBox instance. It synchronizes definitions from YAML files idempotently against the NetBox API.

The YAML in this repository is the desired state. You edit it, open a merge
request, and a pipeline applies it — nobody clicks anything in the NetBox UI.

-----

## ⏱ Quickstart

```bash
# 1. Build (Go 1.24+; dependencies download themselves)
make build

# 2. Point it at your NetBox
cp .env.example .env
$EDITOR .env                      # NETBOX_URL and NETBOX_TOKEN

# 3. See what it would do — against the bundled example data, so nothing of
#    yours is touched. --dry-run never writes.
./netbox-gitops --dry-run --data-dir example
```

That prints a plan and ends with a line like
`Plan: 43 to create, 0 to update, 0 to delete, 3 unchanged`. Nothing has been
written; drop `--dry-run` to apply.

Then, to change something real:

```bash
# 4. Edit the inventory. Start from the parked skeleton:
#    inventory/hardware/active/_new-server.yaml
$EDITOR inventory/hardware/active/servers.yaml

# 5. Check it without touching NetBox — syntax, models, and the cross-object
#    checks (duplicate IPs, rack collisions, cables, unknown slugs)
make check

# 6. Read the plan for your own data before opening the merge request
./netbox-gitops --dry-run
```

### Where to look next

| If you want to… | Read |
|---|---|
| add a server, switch or storage system | [Adding a server, end to end](#adding-a-server-end-to-end) |
| know what the checks catch before you push | [Checking the YAML before it reaches NetBox](#checking-the-yaml-before-it-reaches-netbox) |
| find out whether *your* NetBox accepts a value | [Checking against the live NetBox](#checking-against-the-live-netbox) |
| add hardware the repository has never seen | [Step 1: Define a Device Type](#step-1-define-a-device-type-if-new) — or vendor one from the community library |
| fix a typo in a name or slug | [Renaming an Object](#renaming-an-object-fixing-a-typo) — do **not** just edit it |
| understand why a change did nothing | [Common Errors](#common-errors) and [Phase Order](#phase-order-dependency-model) |
| remove something | [Pruning Orphans](#6-pruning-orphans) — deletion is opt-in |
| see every flag, variable or CI setting | [`docs/CONFIGURATION.md`](docs/CONFIGURATION.md) — one verified table per command |
| document machines that already exist, without typing them in | [`docs/INGEST.md`](docs/INGEST.md) — sources → YAML → MR → NetBox |
| know what is planned but not built | [`docs/ROADMAP.md`](docs/ROADMAP.md) |

-----

## 🚀 Key Features

  * **Single Source of Truth:** The YAML files in this repository represent the desired state of the network inventory.
  * **Idempotency:** The script calculates differences and only applies necessary changes. Repeated executions result in "No-Ops" (no API calls) if the state is already correct.
  * **Safety (Shared Management):**
      * Objects created by this tool are automatically stamped with a **`gitops`** tag.
      * **Opt-in Pruning (`--prune`):** Objects removed from YAML are deleted only when you pass `--prune`, and only if they carry the `gitops` tag — manually created objects are never touched. Combine with `--dry-run` to preview. See the Pruning section below.
  * **Auto-Wiring:** Physical cabling and LAG (Link Aggregation) members are automatically configured based on the YAML definition.
  * **Renames, not duplicates (`rename_from`):** Correcting a typo in a name, slug or other identifying field renames the existing object instead of creating a second one and orphaning the first. Supported on every managed object type. See the Renaming section below.
  * **Coverage:** Manages DCIM (sites, racks, device types, module types, devices, installed modules, cabling), IPAM (VRFs, VLAN groups, VLANs, prefixes), platforms/tenants, custom fields, and **virtualization** (cluster types/groups, clusters, virtual machines and VM interfaces with VLAN/IP assignment).
  * **Device & module type libraries:** Reads the community `devicetype-library` layout (`device-types/` and `module-types/`) alongside the native format, so vendor definitions can be vendored in as-is instead of retyped. Local definitions win over library ones of the same identity. See the Device type library section below.
  * **Fact ingestion (`collect` / `ingest`):** Facts discovered *about* running infrastructure — a Proxmox cluster's guests, an iDRAC scan's hardware inventory — are written **into this repository's YAML**, reviewed as a merge request, and become NetBox truth through the same reconcile every hand-written line takes. Nothing on that path writes to NetBox. See [Documenting what is actually out there](#-documenting-what-is-actually-out-there) and [`docs/INGEST.md`](docs/INGEST.md).
  * **Type Safety:** All input data is validated against typed Go models before interacting with the API to prevent bad requests.


-----

## 📂 Project Structure

```text
.
├── definitions/          # Your Private Definitions (gitignored)
│   ├── sites/           # Data Center Locations
│   ├── device_types/    # Hardware Models (incl. Interface Templates)
│   ├── roles/           # Device Roles (e.g., Server, Leaf Switch)
│   ├── vlans/           # VLAN Definitions
│   ├── prefixes/        # IP Subnets / Prefixes
│   ├── platforms/       # OS / firmware families
│   ├── tenant_groups/   # Tenant Groups (tenancy)
│   ├── tenants/         # Tenants (tenancy)
│   ├── custom_fields/   # Custom field definitions (vmid, hw_* hardware facts)
│   ├── virtualization/  # cluster_types/, cluster_groups/, clusters/
│   └── ...              # Other NetBox object types
├── inventory/           # Your Private Inventory (gitignored)
│   ├── hardware/
│   │   ├── active/      # Active Servers & Switches
│   │   └── passive/     # Patch Panels, PDUs
│   ├── virtual/         # Virtual Machines
│   └── discovered/      # Written by `collect`/`ingest`, reviewed in an MR
│       ├── virtual/     # Unknown VMs: complete, appliable YAML
│       └── hardware/    # Unknown servers: parked skeletons to finish by hand
├── example/             # Public Example Data for Tests
│   ├── definitions/     # Example definitions (for learning/testing)
│   └── inventory/       # Example inventory (for learning/testing)
├── docs/                # Reference documentation
│   ├── CONFIGURATION.md # Every flag, variable and CI setting
│   ├── INGEST.md        # Sources → YAML → MR → NetBox
│   └── ROADMAP.md       # What is not built yet
├── collectors.yaml      # Sources `collect` reads (copy the .example)
├── pkg/                 # Go Implementation (Core Logic)
│   ├── client/          # NetBox API Client
│   ├── collectors/      # Compiled-in source adapters (Proxmox)
│   ├── discovery/       # The normalized model every source funnels into
│   ├── ingest/          # Snapshot → YAML file changes (writes no NetBox)
│   ├── ingestfmt/       # External scan documents (idrac-json)
│   ├── lint/            # Cross-object checks (references, collisions)
│   ├── loader/          # YAML Data Loader
│   ├── models/          # Data Models
│   ├── reconciler/      # Synchronization Logic
│   ├── validate/        # Live checks against a NetBox instance
│   └── utils/           # Utilities
└── cmd/                 # Command-Line Interfaces
    ├── netbox-gitops/   # Main Entry Point (NetBox sync)
    └── yamlcheck/       # YAML syntax, model validation, cross-object lint
```

### 🔒 Private Data vs. Public Examples

**Important**: This repository separates your private data from public examples:

- **`definitions/` and `inventory/`**: Your actual private data (excluded from git via `.gitignore`)
- **`example/`**: Public example data used for tests and documentation (committed to git)

When you run the application, it uses your private `definitions/` and `inventory/` directories.
When you run tests, they use the `example/` directory.

-----

## 📝 Workflow: How to Add New Hardware

### Step 1: Define a Device Type (if new)

File: `definitions/device_types/servers.yaml` (any `*.yaml` under the
`definitions/device_types/` directory is loaded)

Here we define the "blueprint" including all physical ports. NetBox copies these ports *once* when a device is instantiated.

```yaml
- model: "Dell PowerEdge R640"
  slug: "dell-r640"
  manufacturer: "Dell"
  u_height: 1
  is_full_depth: true
  interfaces:
    - name: "idrac"
      type: "1000base-t"
      mgmt_only: true
    - name: "eth0"
      type: "25gbase-x-sfp28"
  # Optional: console_ports, console_server_ports, power_ports, power_outlets,
  # front_ports/rear_ports for patch panels, module_bays, device_bays
```

#### Reusing the community device type library

You do not have to hand-write a device type that already exists. The NetBox
community publishes thousands of them in the
[devicetype-library](https://github.com/netbox-community/devicetype-library),
and that format is consumed **unchanged** — one device type per file, laid out
as `<library>/<Manufacturer>/<model>.yaml`, with hyphenated component keys
(`console-ports`, `power-ports`, …).

Point the controller at a library checkout:

```bash
# Explicit path (a git submodule, a clone, or a directory of vendored files)
./netbox-gitops --devicetype-library ../devicetype-library/device-types

# Or via the environment
export DEVICETYPE_LIBRARY=../devicetype-library/device-types
```

With neither set, `definitions/device_type_library/` inside the data directory
is used if it exists — so vendoring just the models you need is as simple as
copying the files in:

```text
definitions/
├── device_types/              # Native format: a YAML list per file
│   └── servers.yaml
└── device_type_library/       # Community format: one device type per file
    └── Dell/
        └── poweredge-r650.yaml
```

Library entries are merged with your native definitions. If both define the
same slug, **your local definition wins** and the override is logged, so a
vendored library can be customised without editing the checkout. Fields the
library carries but this project does not manage (inventory items) are
reported rather than dropped silently.

See `example/definitions/device_type_library/` for a working example.

#### Module types from the same library

The library ships a `module-types/` tree alongside `device-types/`, covering
the hardware that goes *into* module bays — line cards, NIC mezzanines,
transceivers, power supplies. It is consumed the same way:

```bash
./netbox-gitops --moduletype-library ../devicetype-library/module-types
export MODULETYPE_LIBRARY=../devicetype-library/module-types
```

With neither set, `definitions/module_type_library/` inside the data directory
is used if it exists. A module type carries the same component templates as a
device type (interfaces, console and power ports, front/rear ports, module
bays — everything except device bays, which NetBox allows only on a device
type).

Component names in this library commonly contain the literal `{module}`
placeholder:

```yaml
interfaces:
  - name: '{module}-1GbE-0'
    label: '1'
    type: 1000base-t
```

NetBox substitutes it with the module bay position when the module is
installed, so the same module type in two bays produces `1-1GbE-0` and
`2-1GbE-0` rather than colliding. The placeholder is passed through verbatim.

> **Note:** NetBox requires a module type's manufacturer and model to be
> unique together, and module types have no slug. Two library files claiming
> the same pair cannot both be applied; the loader reports every such conflict
> at once so you can park the unwanted file with `--ignore-file`. The published
> library currently contains one (two Panduit part numbers sharing a model
> name).

#### Referring to a module type

A device installs a module by naming its bay and the module type:

```yaml
modules:
  - name: OCP-1                       # the module bay on the device
    module_type_slug: dell-ocp-25g
```

The slug is a local reference key, not something NetBox stores:

  * A module type defined **natively** uses the `slug` you gave it.
  * A module type read from a **library** has no slug in the file, so the key
    is derived from its model — `Dell OCP 25GbE Mezz` becomes
    `dell-ocp-25gbe-mezz`.

Different vendors may legitimately ship the same model name. When that happens
the bare name is ambiguous and is **not** accepted; use the
manufacturer-qualified form, which always works:

```yaml
    module_type_slug: dell/sfp-10g-sr
```

A run that detects the ambiguity warns and lists the qualified keys to use, so
an ambiguous reference fails with "module type not found" rather than quietly
installing another vendor's hardware.

### Step 2: Create Device Instances (Server/Switch)

File: `inventory/hardware/active/servers.yaml`

Inventory files support a grouped form: fields under `defaults` are merged
into every device in the file (device values win, `tags` are combined).
Since the ports already come from the device type template, an interface is
only listed here when it carries device-specific data — IPs, VLANs, cabling —
without repeating `type` or `enabled`.

The list under a grouped file may be spelled `devices:` or, as an exact
synonym, `items:`. `devices:` reads naturally for inventory; `items:` reads
naturally for definition kinds (sites, roles, …) and is what `import` writes.
Use one spelling or the other, never both.

```yaml
defaults:
  site_slug: "berlin"
  role_slug: "server"
  device_type_slug: "dell-r640"
  rack_slug: "rack-a01"
  status: "active"
  tags: ["gitops", "production"]

devices:
  - name: "srv-web-01"
    position: 10
    interfaces:
      - name: "idrac"
        ip: "10.0.10.50/24"   # string shorthand for `ip: { address: ... }`

      - name: "eth0"
        ip:
          address: "10.0.20.50/24"
          vrf: "Production"
        address_role: "primary"  # Sets the Primary IP on the Device object
        link:
          peer_device: "sw-leaf-01"
          peer_port: "Eth1/1"
          cable_type: "cat6a"    # Optional

  - name: "srv-web-02"
    position: 12
    interfaces:
      - name: "idrac"
        ip: "10.0.10.51/24"
```

The classic form — a plain YAML list of devices — is still fully supported;
`defaults` is just a shortcut, and every field can still be set per device.

### Step 3: Configure Switch Ports & VLANs

File: `inventory/hardware/active/switches.yaml`

```yaml
- name: "sw-leaf-01"
  # ... (Header params like site, role...) ...
  interfaces:
    - name: "Eth1/1"
      mode: "access"
      untagged_vlan: "Server-Vlan"
      # OR for Trunks:
      # mode: "tagged"
      # tagged_vlans: ["Vlan10", "Vlan20"]
```

### Conventions that keep the YAML small

  * **Ports live in the device type, not the inventory.** Define every
    physical port once as an interface template (Step 1). In the inventory,
    only list an interface when it needs device-specific settings, and never
    repeat its `type` — `type` on a concrete device is only needed for
    interfaces that do *not* exist in the template (e.g. virtual or LAG
    interfaces).
  * **Interfaces are enabled by default.** `enabled: true` is implied; only
    write `enabled: false` to shut a port.
  * **One interface carries `address_role: primary`.** NetBox stores a single
    `primary_ip4` and a single `primary_ip6` per device, so exactly one
    interface may claim it per address family — usually the in-band management
    NIC, not the BMC/iDRAC (an out-of-band address is documented by leaving the
    role off). The role may sit on the interface or inside its `ip:` mapping;
    both spellings mean the same thing. Two interfaces claiming it is
    `duplicate-primary-ip`, since NetBox would keep whichever one was written
    last.
  * **Declare each cable on one end only.** Pick a convention (e.g. the
    server/endpoint side owns the `link:`) — the reconciler wires both ends
    from a single declaration. Declaring the same cable from both sides works
    but means every re-patch is a two-file edit.
  * **Group shared fields in `defaults`.** One file per rack, role or
    hardware batch with a `defaults` block keeps each device down to its name,
    position and IPs.
  * **Start from the parked template.** `inventory/hardware/active/_new-server.yaml`
    holds a filled-in skeleton. The leading underscore parks it, so it is never
    applied (see *Parking a File* below) — copy a block out of it rather than
    editing it.

### Adding a server, end to end

With those conventions in place, a new machine in an existing rack is its
identity and its addresses, and nothing else:

```yaml
# inventory/hardware/active/servers.yaml  (defaults supply site, role,
# device type, rack, face, status and tags)
  - name: "berlin-srv-web-03"
    device_type_slug: "poweredge-r740"
    rack_slug: "berlin-rack-a01"
    position: 14
    serial: "WEB03-SN-KLMNO"
    tags: ["production"]

    interfaces:
      - name: "idrac"
        ip: "10.10.10.103/24"

      - name: "eth0"
        ip:
          address: "10.10.20.103/24"
          vrf: "Production"
        address_role: "primary"
        link:
          peer_device: "berlin-leaf-01"
          peer_port: "Eth1/4"
          cable_type: "dac-active"
```

Then check it before it reaches NetBox:

```bash
go run ./cmd/yamlcheck definitions inventory
```

Nothing else has to change — the switch side is wired from the server's
`link:`, and the ports come from the device type. The switch is edited only
when the new port needs a VLAN it does not already have.

### Checking the YAML before it reaches NetBox

`yamlcheck` reads the data directory and reports what is wrong with it without
opening a connection: YAML syntax, then the typed model validation (required
fields, cross-field constraints, NetBox choice values), then cross-object
checks that no single object can see on its own.

```bash
go run ./cmd/yamlcheck                       # definitions/, inventory/ and example/
go run ./cmd/yamlcheck path/to/data-dir      # a directory holding definitions/ and inventory/
go run ./cmd/yamlcheck --strict              # fail on warnings too
```

**Errors** — wrong regardless of what NetBox contains, so they fail the run:

| Check | What it catches |
|---|---|
| `duplicate-device`, `duplicate-vm`, `duplicate-interface` | the same name declared twice, where the second declaration silently overwrites the first |
| `duplicate-ip` | one address on two interfaces in the same VRF (the global table counts as one) |
| `duplicate-primary-ip` | two interfaces claiming the primary IP for one address family, where NetBox keeps only the one written last |
| `rack-collision` | two devices occupying the same rack unit, computed from each device type's `u_height` |
| `position-without-face` | a rack position with no `face`, which NetBox rejects outright (`Must specify rack face when defining rack position`) |
| `cable-conflict` | a port claimed by two different cables (a patch panel's front port and rear port of the same name are two ports, resolved by role as the reconciler does) |
| `unknown-peer-port` | a cable to a port its peer's device type does not have |
| `untyped-interface` | an interface that is neither an interface template on the device type nor carries a `type`, so NetBox has nothing to create it from |
| `unknown-lag-member` | a LAG whose `members` name an interface the device does not have |
| `unknown-module-bay`, `unknown-device-bay`, `missing-device-bay` | a module or child device installed into a bay the parent's device type does not have |
| `unknown-site`, `unknown-role`, `unknown-rack`, `unknown-device-type`, `unknown-vlan`, `unknown-vrf`, `unknown-module-type`, `unknown-cluster`, `unknown-platform`, `unknown-tenant`, `unknown-parent-device` | a reference that resolves to nothing declared here (VLANs are matched within the device's site, as NetBox resolves them) |
| `unknown-custom-field`, `custom-field-wrong-type` | a device setting a custom field this repository does not declare, or declares for another content type — the mistake a repository makes when it ingests `hw_*` facts without copying the definitions in |
| `invalid-ip`, `network-address`, `broadcast-address` | an address that is not a host address (a /31 point-to-point link is not flagged) |

**Warnings** — legitimate in some repositories, so they only fail under
`--strict`: `redundant-interface-type` and `redundant-enabled` (a value the
device type template or the default already supplies),
`interface-type-override`, `interface-not-in-template`,
`cable-declared-twice` (the same cable from both ends),
`undeclared-peer-device`, `ip-outside-prefix`, `unracked-device`,
`module-type-without-slug`.

Two things worth knowing about the reference checks:

  * A kind of object the repository declares **none** of is not checked at all.
    A repository that manages devices but not sites is a legitimate partial
    adoption, not one full of broken site references.
  * If you do declare everything but reference an object created outside this
    repository, `--allow-undeclared-refs` reports those as warnings instead.

### Checking against the live NetBox

`yamlcheck` cannot know what *your* instance accepts. The choices a NetBox
offers depend on its release and its plugins — 4.6 has 216 interface types —
so a hardcoded list can only approximate them, and a value it gets wrong
becomes a `400` partway through an apply, after earlier objects have been
written.

`validate` asks the instance instead:

```bash
./netbox-gitops validate               # against $NETBOX_URL, writes nothing
./netbox-gitops validate --skip-references
```

It reads each endpoint's `OPTIONS` response — the authority on what that server
accepts — and checks every choice value and string length the YAML sets against
it. References the repository does not declare itself are looked up, so a site
that exists only in NetBox is accepted and one that exists nowhere is reported.
Everything wrong is reported at once, rather than stopping at the first
rejection:

```text
✗ device srv-01 interface eth0: type "25gbase-x-sfp29" is not a value this
  NetBox accepts; did you mean "25gbase-x-sfp28"? (invalid-choice)
✗ cable declared on device srv-01 interface eth0: type "dac-activ" is not a
  value this NetBox accepts; did you mean "dac-active"? (invalid-choice)
✗ site does-not-exist: is declared nowhere in this repository and does not
  exist in NetBox either (missing-reference)
```

Every request it makes is a read, and the client runs in dry-run mode
throughout, so it cannot write even if asked to.

The three checks answer different questions, and a merge request is worth
running all three through:

| | Needs a NetBox | Answers |
|---|---|---|
| `yamlcheck` | no | Is this repository coherent with itself? |
| `validate` | yes | Will this instance accept these values? |
| `--dry-run` | yes | What would change? |

-----

## 🔎 Documenting what is actually out there

Everything above runs one way: you write YAML, a merge request changes it, the
reconciler applies it to NetBox. That works well for infrastructure you are
adding. It works badly for the two hundred machines that were already racked
before anybody wrote a line of this, and for the facts nobody wants to type —
serial numbers, DIMM counts, BIOS versions, power draw.

So there is a second direction. It is not a second write path:

```
  Proxmox API  ─┐
                ├─→  Snapshot  →  match  →  YAML changes  →  MR  →  merge  →  sync  →  NetBox
  iDRAC scan   ─┘
```

**Nothing on the discovery side writes to NetBox.** The collectors are
read-only against their sources, and the package that writes the YAML holds no
NetBox client and cannot acquire one. A discovered fact reaches NetBox by being
committed, reviewed and reconciled — exactly like a hand-written line, because
the whole value of documenting infrastructure in git is that somebody looked at
the change.

```bash
./netbox-gitops collect --dry-run                             # print the diffs, write nothing
./netbox-gitops collect                                       # the sources in collectors.yaml
./netbox-gitops ingest --format idrac-json --input scan.json  # a scan another tool produced
```

Each run ends with a summary in the sync's own shape, and `--detailed-exitcode`
exits `2` when the repository would change — which is how a scheduled CI job
decides whether to open a merge request at all:

```text
Facts: 12 updated, 3 new VMs, 2 parked devices, 41 unchanged
```

A machine the repository **already declares** has its facts written into *your*
file, at *that machine's block*, with comments, key order and every untouched
byte preserved:

```diff
   - name: "srv-01"
     position: 10 # lowest rack unit
-    serial: "OLD-SERIAL"
+    serial: "CN7792048K0042"
+    asset_tag: "7XKQ2M3"
+    custom_fields:
+      hw_cpu_count: 1
+      hw_cpu_model: "AMD EPYC 9354P 32-Core Processor"
```

That diff is the review gate. A fact that contradicts a declared value shows up
as a change to that line, and merging it is what makes the fact the truth. If
the scanned serial is wrong, reject the merge request and go fix the machine.

A machine it has **never heard of** lands under `inventory/discovered/`: a
virtual machine as complete, appliable YAML, a physical one as a *parked*
skeleton. Parked, because a scan reaches a BMC over the network and cannot know
which site the machine stands in, which rack or which unit — guessing would put
a plausible fiction into NetBox, so those fields are left as `TODO`s for
somebody who can walk into the room.

What a machine is allowed to say at all is a short, closed list:

| Object | Keys a collector may write |
|---|---|
| Device | `serial`, `asset_tag`, `custom_fields:` entries prefixed `hw_` |
| Virtual machine | `vcpus`, `memory`, `disk`, `status`, `vmid` |

Names, sites, racks, positions, roles, device types, cabling and tenants are
**never** written by a machine. The line is what a machine can observe about
itself: a serial is burned into the hardware, while where it stands and what it
is for are decisions people make.

**[`docs/INGEST.md`](docs/INGEST.md)** has the rest — the matching rules, the
skeleton and adoption workflows, the `idrac-json` contract, and an honest note
about the churn from volatile metrics like power draw. A commented, runnable
pipeline is in
[`.gitlab-ci.ingest.example.yml`](.gitlab-ci.ingest.example.yml).

-----

## ⚠️ Important Concepts & Troubleshooting

### Renaming an Object (Fixing a Typo)

Every object is matched against NetBox by an **identifying field**. That is what
makes the sync idempotent — and it is also why editing that field is not an
ordinary change. The old value stops matching anything, so the object is created
a *second* time and the original is left behind, still holding every reference
that pointed at it. Nothing errors; you just quietly end up with two.

Declare where the object came from and it is renamed in place instead:

```yaml
- name: Rack 01          # was "Rakc 01"
  slug: rack-01
  site_slug: frankfurt
  rename_from: Rakc 01   # ← the previous identity
```

Once the sync has run, NetBox holds the new value, the declaration becomes a
no-op, and you can delete the line. Leaving it in place is harmless.

**Which field is the identity?** It differs per object type, because NetBox
does. `rename_from` always holds the previous value of *that* field:

| Object | Identified by | `rename_from` holds |
|---|---|---|
| Site, device role, platform, tenant, tenant group, tag, VLAN group, cluster type, cluster group, device type | `slug` | the old slug |
| Rack, device, interface, VM, VM interface, cluster, VRF, custom field | `name` | the old name |
| Module type | `model` | the old model |
| VLAN | `vid` | the old VID, quoted (`"201"`) |
| Prefix | `prefix` | the old CIDR |

Two consequences worth knowing:

  * For a slug-identified object, changing only the **name** needs no
    declaration — the slug still matches, so it is a plain update. The same goes
    for a VLAN's name, since a VLAN is identified by its VID.
  * A rename changes *what an object is called*, not where it lives. The scope
    (site, device, cluster) is carried over unchanged; moving an object between
    scopes is a different operation and is not what this does.

**Safety.** `rename_from` describes an object's past, which is weaker evidence
than a plain identity match — a typo in it can name a real object that has
nothing to do with your declaration. So:

  * Only objects carrying the `gitops` tag can be renamed. To bring an existing
    unmanaged object under management, declare it under the name it *already*
    has; the next sync adopts and tags it, and it can be renamed after that.
  * If `rename_from` matches more than one object, the sync fails rather than
    guessing.
  * If objects exist under **both** the old and the new identity, nothing is
    renamed and you get a warning — which one survives is your call, not the
    tool's.
  * A `rename_from` that matches nothing is not an error; that is simply what it
    looks like once it has been applied.

`--dry-run` reports a rename as the update it is, without writing.

### Parking a File (Ignored Files)

Not everything in the repository should be applied. Inventory owned by another
system, a draft you are not ready to sync, a reference copy — all of these can
stay in place and be skipped:

```bash
# Default: filenames starting with an underscore are skipped
inventory/hardware/active/_imported-from-cmdb.yaml   # not applied

# Override the patterns (globs, matched against the filename)
./netbox-gitops --ignore-file '_*.yaml' --ignore-file 'imported-*.yaml'
export IGNORED_FILES='_*.yaml,imported-*.yaml'

# Apply everything, including parked files
./netbox-gitops --include-ignored-files
```

Every skipped file is logged, so a parked file never goes unnoticed. Patterns
match the **filename only**, not the path — a file inside a directory whose
name matches a pattern is still loaded. An invalid glob fails at startup rather
than silently matching nothing.

> **⚠️ Pruning:** A parked file is invisible to the controller, so `--prune`
> cannot tell its objects apart from orphans. Two cases, and only you can tell
> them apart:
>
> - Objects **another system owns** were never created here, carry no `gitops`
>   tag, and are safe — pruning never touches untagged objects.
> - Objects **this controller previously created** from a file you have now
>   parked still carry the tag and **will be deleted** as orphans.
>
> A run that both skips files and uses `--prune` warns and lists the skipped
> files before deleting anything. Preview with `--dry-run --prune` first.

### Front Ports Across NetBox Releases

NetBox 4.6 replaced a front port's singular `rear_port` / `rear_port_position`
fields with a `rear_ports` list of `{position, rear_port, rear_port_position}`
mappings — and **accepts the old fields silently rather than rejecting them**,
so sending the wrong shape leaves the port created but unwired.

The controller picks the shape from the release reported by `/api/status/`, so
patch panels wire correctly on both older releases and 4.6+. Nothing in your
YAML changes; `rear_port` and `rear_port_position` stay as they are.

### NetBox Version Compatibility

On startup the controller reads `/api/status/`, logs the detected release, and
refuses to run against NetBox older than **3.6** — the release that renamed the
device `device_role` field to `role`. Without this check an unsupported server
produces a scatter of `400 Bad Request` errors on individual fields rather than
one clear message.

A server whose version cannot be determined (no status endpoint, a proxy that
rewrites it) is reported as a warning and the run continues: the check exists
to explain failures, not to become a new way to fail.

### Phase Order (Dependency Model)

NetBox rejects an object whose references do not exist yet — a device needs its
site, role and device type; a prefix needs its VRF. The controller therefore
reconciles in a fixed order derived from the object types themselves, **not**
from file or directory names. You never have to number or sort your YAML: the
layout under `definitions/` and `inventory/` is free-form, and the same order
runs every time.

| # | Phase | Objects, in reconcile order |
|---|-------|-----------------------------|
| 1 | `foundation` | tags → roles → custom fields → platforms → tenant groups → tenants → sites → racks |
| 2 | `network` | VRFs → VLAN groups → VLANs → prefixes |
| 2 | `device-types` | manufacturers → device types (incl. interface/port/bay templates) → module types |
| 3 | `devices` | per device: device → interfaces → IP addresses → primary IP → modules → device bays → front/rear ports; **then all cables** |
| 4 | `virtualization` | cluster types → cluster groups → clusters → VMs → VM interfaces → VM IPs |

Two orderings inside a phase are worth knowing about, because both solve
problems that a "number your files" convention leaves to the user:

  * **Cables run last, in a second pass.** A cable needs the ports on *both*
    ends to exist, so every `link:` found while reconciling devices is queued
    and applied only after all devices and their ports are in place. Peer order
    in the YAML is irrelevant.
  * **Parents are reconciled before their children.** A device with
    `parent_device` is installed into a bay on that parent, so the parent must
    exist first. The device list is topologically sorted before reconciliation,
    at any nesting depth — a blade may be declared above its chassis, or in an
    alphabetically earlier file, and the run still succeeds. A `parent_device`
    that is not declared in the run is assumed to exist in NetBox already; a
    cycle (`a` parented to `b` parented to `a`) is reported as an error before
    any change is applied.

The phase order is asserted by `TestValidPhasesCreationOrder`, and its mirror
image — the reverse order used for `--prune` — by
`TestPruneTargetsReverseDependencyOrder`.

### The `gitops` Tag

  * The script automatically tags every object it creates with `GitOps Managed` (slug: `gitops`).
  * **Default behavior:** If you remove a device from the YAML file, it is **not** deleted from NetBox — the tool only creates and updates objects unless you opt into pruning.
  * **Pruning (`--prune`):** Run a sync with `--prune` to delete orphans — objects that still carry the `gitops` tag but are no longer declared in YAML. Only managed objects are ever deleted; manually created objects (without the tag) are protected. Combine with `--dry-run` to preview the deletions before applying them. Pruning is scoped to the phases that run (`--only`) and cannot be combined with `--site`/`--device`/`--vm`. See the Pruning section below for the full semantics.

### Common Errors

Run `go run ./cmd/yamlcheck` first — the first three below are reported by name
before a single request is made.

**`400 Bad Request: {"face": ["Must specify rack face when defining rack position."]}`**

  * **Cause:** the device declares `position` but no `face`. NetBox requires
    both together, and rejects the device — every interface, IP and cable
    behind it is then never created.
  * **Fix:** add `face: "front"` (or `"rear"`). Put it in the file's `defaults`
    block so it cannot be forgotten on the next device.

**`400 Bad Request: {"type": ["This field may not be blank."]}`**

  * **Cause:** an interface exists neither as a template on the device type nor
    with a `type` of its own.
  * **Fix:** name the template's interface, or give the interface a `type`.
    `yamlcheck` reports this as `untyped-interface`.

**Cables "flap" — deleted and recreated on every run**

  * **Cause:** two devices claim the same peer port. A port holds one cable.
  * **Fix:** check the `link:` declarations. `yamlcheck` reports this as
    `cable-conflict`.

**A device type change (e.g. a new port) does not appear on existing devices**

  * **Cause:** standard NetBox behaviour. Editing the blueprint does not
    retrofit instances that already exist.
  * **Fix:** use "Sync components" on the device type page in NetBox, or delete
    and re-sync the device. Global attributes like `u_height` do update
    immediately.


## 🛠 Local Development

### 1\. Clone Repository

```bash
git clone <repo-url>
cd netbox-gitops
```

### 2\. Build the Binary

Requires Go 1.24 or later. Dependencies are managed via Go modules and downloaded automatically.

```bash
go build -o netbox-gitops ./cmd/netbox-gitops/
```

### 3\. Environment Configuration

Copy `.env.example` to `.env` and fill it in:

```ini
NETBOX_URL=https://netbox.example.com
NETBOX_TOKEN=your_api_token_here
# Optional: skip TLS certificate verification (a lab NetBox behind a
# self-signed certificate). Every run that sets it says so in its output.
# IGNORE_SSL_ERRORS=true
```

The file is read at startup; `--config path/to/other.env` points at a different
one. **Anything already exported in the environment wins over the file**, so CI
variables are never shadowed by a `.env` that happens to be in the working
directory — and a missing `.env` is not an error when the environment already
carries the settings. A `--config` path you name explicitly *is* required to
exist, so a typo fails instead of quietly running against the wrong NetBox.

`.env` is gitignored. `.env.example` is the committed template and holds no
credentials.

> **TLS:** certificates are verified. `IGNORE_SSL_ERRORS=true` (also `1`,
> `yes`, `on`) turns verification off for a self-signed instance and logs a
> warning on every run.

Every setting — the environment variables, the flags of every command, the
GitLab CI/CD variables and the end-to-end knobs — is listed in
[`docs/CONFIGURATION.md`](docs/CONFIGURATION.md).

## ▶️ Usage

### 1\. Dry-Run (Simulation)

Shows exactly what changes *would* be applied without actually touching NetBox. **Always run this first\!**

```bash
./netbox-gitops --dry-run
```

Every run ends with a plan summary:

```text
Plan: 3 to create, 1 to update, 0 to delete, 41 unchanged
```

### 2\. Apply Changes

Executes the synchronization against the NetBox API.

```bash
./netbox-gitops
```

### 3\. Drift Detection (CI-friendly)

With `--detailed-exitcode` the process exits with code `2` when changes are
pending (exit `0` means NetBox is in sync with the YAML). A scheduled CI job
becomes a drift monitor with zero extra infrastructure:

```bash
./netbox-gitops --dry-run --detailed-exitcode
```

### 4\. Machine-Readable Plan Output

`--output json` prints the planned (or applied) changes as JSON on stdout;
all logs move to stderr so stdout stays clean for parsing. Useful for posting
a plan comment on a merge request:

```bash
./netbox-gitops --dry-run --output json 2>/dev/null > plan.json
```

```json
{
  "dry_run": true,
  "summary": { "create": 3, "update": 1, "delete": 0, "unchanged": 41 },
  "changes": [
    {
      "action": "create",
      "app": "dcim",
      "endpoint": "sites",
      "object": "slug=berlin-dc",
      "fields": { "name": "Berlin DC", "slug": "berlin-dc" }
    }
  ]
}
```

### 5\. Selective Sync

Restrict a run to specific phases or devices — handy for debugging and
targeted hotfixes:

```bash
# Only reconcile foundation objects (tags, roles, sites, racks)
./netbox-gitops --only foundation

# Only network (VRFs, VLAN groups, VLANs, prefixes) and device types
./netbox-gitops --only network,device-types

# Only devices of one site
./netbox-gitops --only devices --site berlin-dc

# A single device
./netbox-gitops --device srv-web-01

# Only virtual machines, and just one of them
./netbox-gitops --only virtualization --vm web-01
```

Valid `--only` values: `foundation`, `network`, `device-types`, `devices`,
`virtualization`. `--site` filters the device and virtualization phases by site
slug; `--device` filters a single device; `--vm` filters a single virtual
machine by name. Selected phases always run in the fixed dependency order — see
[Phase Order](#phase-order-dependency-model) — regardless of the order you list
them in.

> **Note:** Skipped phases are not validated — if you sync `--only devices`,
> the referenced sites, roles and device types must already exist in NetBox.
> Likewise `--only virtualization` assumes its sites, roles, platforms, tenants
> and VLANs already exist.
>
> **Note:** `--site` matches a VM only if the VM declares that `site_slug`
> directly; a clustered VM (whose site comes from its cluster) is not matched by
> `--site`. Use `--vm` to target such a VM by name.

### 6\. Pruning Orphans

By default the controller only creates and updates objects. With `--prune` it
also deletes orphans: objects that still carry the `gitops` managed tag but are
no longer declared in YAML. Manually created objects (those without the tag)
are never touched.

```bash
# Preview what pruning would delete (no changes applied)
./netbox-gitops --dry-run --prune

# Apply, deleting orphaned managed objects
./netbox-gitops --prune

# Prune only the network phase's orphans (VRFs, VLAN groups, VLANs, prefixes)
./netbox-gitops --only network --prune
```

Pruning runs after all selected phases and deletes endpoints in reverse
dependency order (device/VM children first, then sites/platforms/tenants/tags
last) to respect NetBox foreign-key constraints. It is scoped to the phases that
run via `--only`, and cannot be combined with `--site`/`--device`/`--vm` (a
filtered run would delete the out-of-scope objects the filter excluded). Device children
(interfaces, IP addresses, front/rear ports, modules) are pruned too; cables
are not (they are untagged and NetBox removes them when their port or device is
deleted).

## 📚 Example Files

The `example/` directory ships a small, self-contained dataset used by the test
suite and as a format reference. It covers two sites (Berlin DC, Test Lab) and a
few objects of each supported type:

- 2 sites, 5 device roles, 2 platforms, 1 tenant (+ group)
- 2 VRFs, 2 VLAN groups, 3 VLANs, 3 prefixes, 3 racks
- 6 device types (native format) + 1 in the community library format,
  2 module types, 2 VM custom fields (`vmid`, `vm_template_id`)
- 8 hardware devices — including a blade chassis with two child blades, a GPU
  server, and a patch panel (front/rear ports)
- 2 virtual machines (one clustered, one site-only)

See **[EXAMPLES.md](./EXAMPLES.md)** for the full breakdown, file layout, and the
key concepts each file demonstrates.

### Quick Test

To see the examples in action:

```bash
# Preview what would be created (uses the example data)
./netbox-gitops --dry-run --data-dir example

# Apply to your NetBox instance (requires .env configuration)
./netbox-gitops --data-dir example
```

**Note**: The examples create a complete test infrastructure suitable for learning and development. For production use, customize the files to match your actual environment.

-----

## 📄 License

Licensed under the [Apache License, Version 2.0](./LICENSE).

Every source file carries an `SPDX-License-Identifier: Apache-2.0` line, so
licence scanners in a CI pipeline can identify the project without parsing the
full text.
