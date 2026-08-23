# Fact ingestion

The rest of this project runs one way: YAML is the desired state, a merge
request changes it, a reconciler applies it to NetBox. This document is about
the other direction — how facts discovered *about* running infrastructure get
into that YAML in the first place.

The short version: they get in by being written into the repository and
reviewed like anything else.

```
  Proxmox API  ─┐
                ├─→  discovery.Snapshot  →  match  →  YAML changes  →  MR  →  merge  →  sync  →  NetBox
  iDRAC scan   ─┘
```

## The one rule

**Nothing on the discovery side writes to NetBox.** Not the collectors, not the
ingest adapters, not the YAML writer. `pkg/ingest` holds no NetBox client and
has no way to acquire one; its entire effect on the world is a set of changed
files in this repository.

That is not caution for its own sake. A fact that reaches NetBox without
passing a merge request has not been reviewed by anybody, and the whole value
of documenting infrastructure in git is that somebody looked at the change. The
sibling `idrac-netbox-importer` has a direct-to-NetBox mode; this project
deliberately does not use it, and consumes its JSON instead.

The corollaries hold everywhere:

- ingest **never deletes** an object from the YAML — a machine that stopped
  answering is a fact for a human to interpret, not a deletion. The single
  exception loses nothing: a block in a *generated* file is removed once you
  declare that object in a file of your own (see
  [Adopting a discovered object](#adopting-a-discovered-object));
- ingest **never removes a key it did not write**.

## Two sources, one model

| Source | Command | How it gets its facts |
|---|---|---|
| Proxmox VE | `netbox-gitops collect` | A compiled-in collector reads `/api2/json` with GETs only. |
| Dell iDRAC | `netbox-gitops ingest --format idrac-json` | Reads the JSON document written by `idrac-inventory -output json`. |

Both produce a `discovery.Snapshot` (`pkg/discovery`), and everything after
that point is source-agnostic. Adding a third source means writing one adapter,
not touching the pipeline.

### The idrac-json contract

The document is the sibling project's `-output json`: a `servers` array and a
`stats` block. The parser (`pkg/ingestfmt/idracjson`) reproduces the same
derivations the importer makes for its own NetBox custom fields — core count
from the first socket, memory type and speed from the first *populated* DIMM,
the storage summary grouped by capacity — so a device documented through either
path reads identically.

It is deliberately forgiving of that contract:

- a server entry carrying an `error` is skipped and reported by name; a BMC
  that was down during a scan must not stop the other fifty being documented;
- an unknown `schema_version` warns rather than failing, so an importer upgrade
  cannot break every ingest pipeline at once;
- a `stats` block that disagrees with the entries is reported, because that is
  what a truncated artifact looks like.

## What a collector may write

**The whitelist is the heart of this design.** It lives in one place in code
(`pkg/ingest/whitelist.go`) and is the complete list of what a machine may say
about itself:

| Object | Keys |
|---|---|
| Device | `serial`, `asset_tag`, and `custom_fields:` entries with the `hw_` prefix |
| Virtual machine | `vcpus`, `memory`, `disk`, `status`, `vmid` |

The `hw_` custom fields, in the order they are written:

| Field | Type | Fact |
|---|---|---|
| `hw_cpu_count` | Integer | Installed processors |
| `hw_cpu_model` | Text | Processor model name |
| `hw_cpu_cores` | Integer | Physical cores per socket |
| `hw_ram_total_gb` | Integer | Installed memory, GB |
| `hw_ram_slots_total` | Integer | Memory slots the machine has |
| `hw_ram_slots_used` | Integer | Memory slots populated |
| `hw_ram_slots_available` | Integer | Memory slots still free |
| `hw_memory_type` | Text | DDR4, DDR5, … |
| `hw_memory_speed_mhz` | Integer | Operating memory speed |
| `hw_disk_count` | Integer | Installed drives |
| `hw_storage_summary` | Text | Drives grouped by capacity, e.g. `2x894GB, 16x14901GB` |
| `hw_storage_total_tb` | Text | Total raw storage, two decimals |
| `hw_bios_version` | Text | System firmware version |
| `hw_power_state` | Text | `On` / `Off` at the last scan |
| `hw_power_consumed_watts` | Integer | Power draw at the last scan |
| `hw_power_peak_watts` | Integer | Highest draw the controller has recorded |
| `hw_last_inventory` | Text | RFC 3339 timestamp of the last successful scan |

The prefix is configurable with `--custom-field-prefix`; nothing outside it is
ever created, updated or reordered, so a custom field another team maintains on
the same device is untouched.

**Everything else is human-owned and never written by a machine:** names,
sites, racks, positions, faces, roles, device types, cabling, links, tags,
tenants, platforms, descriptions. The line is what a machine can *observe about
itself*. A serial is burned into the hardware. Where the machine stands and
what it is for are decisions people make, and no scan is entitled to an opinion
about them.

The definitions a fresh NetBox needs for these fields ship in
`example/definitions/custom_fields/custom_fields.yaml`. Copy them into your
private `definitions/custom_fields/` — the reconciler creates them in the
foundation phase, before anything sets a value into one.

## Facts are written into the file you wrote

For an object this repository already declares, the facts are written **into
the existing hand-written file, at that object's block**. Not into a shadow
file, not into a sidecar.

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
as a change to that line, and **merging it is what makes the fact the truth**.
If the scanned serial is wrong, you reject the merge request and go fix the
machine — you do not argue with the file.

Mechanically, the editing is text surgery guided by the parsed YAML nodes,
never a re-encode of the document: re-encoding loses blank lines and re-flows
anything spaced by hand, which would turn a one-line fact update into a
whole-file diff and destroy the review gate entirely. Comments, key order,
quoting style and every untouched byte survive. Afterwards the rewritten file
is re-parsed and checked against what was intended; if it does not match, the
file is **not written** and the run reports why.

Two properties follow, and both are covered by golden-file tests:

- a fact equal to what the YAML already says writes nothing, so **re-running on
  unchanged input produces a zero diff**;
- a file whose values cannot be located and replaced safely is abandoned whole,
  with an error naming the object and the key. A partially rewritten inventory
  file is far worse than an unwritten one.

## Objects the repository has never heard of

Discovered objects that match nothing in the repository are written as new
files under `inventory/discovered/`, which mirrors the hand-written split so
the reconciler picks them up with no special case:

```text
inventory/
├── hardware/{active,passive}/     # yours
├── virtual/                       # yours
└── discovered/
    ├── virtual/<source>/<cluster>.yaml    # complete, appliable
    └── hardware/<source>/_<serial>.yaml   # parked skeletons
```

**Unknown VMs** become complete, appliable YAML — one file per cluster, sorted,
with a fixed key order, and no timestamp anywhere in the header so the file is
byte-identical on unchanged input.

**Unknown physical machines** become *parked* skeletons: the filename starts
with an underscore, which is this repository's existing parking convention, so
the loader skips them and says it did. They are parked because a scan reaches a
BMC over the network and **cannot know** which site the machine stands in,
which rack, which unit, which way it faces, what role it plays or which device
type it is. Guessing any of those would put a plausible fiction into NetBox.

The skeleton carries everything the scan does know — the name guess, serial,
asset tag, `hw_*` fields, and the NICs it saw as comments — and explicit `TODO`
markers for everything it does not:

```yaml
- name: "mystery-box" # TODO: the name this device should carry in NetBox
  site_slug: "TODO" # TODO
  role_slug: "TODO" # TODO
  device_type_slug: "TODO" # TODO — the scan reports the model as PowerEdge R7615
  rack_slug: "TODO" # TODO
  position: 0 # TODO: lowest rack unit the chassis occupies
  face: "front" # TODO: front or rear
  serial: "UNKNOWN-42"
```

Every run names the parked files in its summary, because a file nobody notices
is a file nobody finishes.

### Finishing a skeleton

1. Fill in the `TODO` fields.
2. Drop the leading underscore, or move the block into the file for its rack.
3. `go run ./cmd/yamlcheck definitions inventory` — an unfinished skeleton
   fails it loudly (`site "TODO" is not declared in this repository`).
4. Open the merge request. The next sync creates the device in NetBox.

From then on the machine is declared, so later scans **match it and refresh its
facts in place** rather than writing a second copy. That holds even while the
file is still parked: a skeleton stays current until somebody finishes it.

## How a discovered object is matched

Devices, in order — the first rule that finds exactly one candidate wins:

1. **the management address it was scanned on**, against a declared interface
   carrying that address (an interface whose name reads like a management port
   — `idrac`, `ilo`, `ipmi`, `bmc`, `mgmt`, `oob`, `cimc` — breaks a tie);
2. **serial**;
3. **service tag**, against the declared `asset_tag`;
4. **name** (a BMC's fully qualified name also matches the short name in YAML).

The address goes first because it is not a claim the machine makes about
itself: it is how the scan reached *this* machine and not another one.

VMs: name **and** cluster; then name alone if it is unique.

**Any rule that matches more than one declaration is an error listing the
candidates and their `file:line`** — never a guess. This is the same bargain
`rename_from` strikes elsewhere in this project. Writing an observed serial
into the wrong device's block would be a quiet, plausible corruption of the
inventory, and no heuristic is worth that. Two scanned machines resolving to
one declaration is the same error seen from the other side.

Every object the repository declares **anywhere** is matched — including in
parked files, and including in files a previous run generated. That is what
keeps one machine from being declared twice.

### Adopting a discovered object

A generated file is an ordinary inventory file: it is applied to NetBox like
any other, and the facts of the objects in it are refreshed there in place. To
take an object over, **declare it in a file of your own** — you do not have to
edit the generated file at all. The next run sees two declarations of one
object, keeps yours, and removes the block from the generated file. If that
empties the file, the file is deleted.

That is the only circumstance in which anything is ever removed from the YAML,
and it loses nothing: every object it takes out is, by definition, declared
somewhere else. In particular **an object that stops answering is never
removed.** A machine missing from a scan may have been decommissioned, or its
BMC may be unplugged; which of those it is, is a fact for a person to
interpret, not a deletion to apply.

One combination is refused rather than half-applied: a single file that has
both new facts to write and an adopted object to remove in the same run. Both
rewrites are computed from the same original line numbers, so applying them
together would corrupt the file. The run says so and writes neither; merge what
it did write and run it again.

## Volatile metrics and churn

`hw_power_consumed_watts`, `hw_power_state` and `hw_last_inventory` change on
essentially every scan. They are written to git like any other fact, and the
consequence is accepted deliberately: **every scheduled scan produces a diff.**

How often that happens is a property of your CI schedule, not of this tool. A
nightly scan gives you one merge request a night with a readable power history
in the file's git log. An hourly one gives you twenty-four, which nobody will
read. Pick the interval you will actually review; if you want the inventory
facts without the churn, the honest options are to scan less often or to stop
declaring the volatile fields in `definitions/custom_fields/`.

## Commands

```bash
# Read every configured source and write what it reports
netbox-gitops collect

# One source only
netbox-gitops collect --source pve-prod

# An importer artifact from a CI job
netbox-gitops ingest --format idrac-json --input scan.json

# See the diffs, write nothing
netbox-gitops ingest --format idrac-json --input scan.json --dry-run

# Exit 2 when the repository would change, so CI can decide to open an MR
netbox-gitops collect --detailed-exitcode
```

Every flag is in [`CONFIGURATION.md`](CONFIGURATION.md).

### Configuring sources

`collect` reads `collectors.yaml` (see
[`collectors.yaml.example`](../collectors.yaml.example)). The file names the
*environment variable* holding each token, never the token, so it is safe to
commit and the secret stays in the CI variable store with `NETBOX_TOKEN`.

```yaml
sources:
  - name: pve-prod # also the directory generated files land in
    type: proxmox
    url: https://pve.example.com:8006
    token_env: PROXMOX_TOKEN # "user@realm!tokenid=secret"; PVEAuditor on / is enough
    verify_tls: true
    cluster: berlin-prod-cluster # the NetBox cluster these guests belong to
```

`verify_tls: false` is per-source and logs a warning on every run, the same
bargain `IGNORE_SSL_ERRORS` strikes for the NetBox client: one lab instance
behind a self-signed certificate must not quietly disable verification
everywhere else.

A mistyped key is an error naming the key, and a `--source` that matches nothing
is an error listing what is configured — neither turns into a run that quietly
scans nothing.

## In CI

The pipeline shape, for both commands, is the same:

1. a **scheduled** job produces a snapshot (run the importer container, or let
   `collect` fetch it);
2. `netbox-gitops … --detailed-exitcode` writes the YAML;
3. exit `2` means the repository changed → commit to a branch and open a merge
   request; exit `0` means nothing changed → the job ends and opens nothing;
4. a human reviews the diff and merges;
5. the normal apply pipeline syncs it to NetBox.

Commented, runnable examples for both are in
[`.gitlab-ci.ingest.example.yml`](../.gitlab-ci.ingest.example.yml). They are
not enabled by default: a job that opens merge requests on a schedule should be
switched on deliberately.

The credentials involved are read-only ones for the scanned systems. The
ingestion jobs need **no** `NETBOX_TOKEN` — they never talk to NetBox.

## Seeing it work

`tests/e2e/ingest.sh` walks the whole path against the committed sample data,
and needs no NetBox, no credentials and no network — which is the claim it
exists to make:

```bash
make e2e-ingest
```

It starts a fake Proxmox, feeds a recorded importer document through `ingest`,
and asserts each of the properties above: the facts land inside the
hand-written file and disturb nothing else in it; an unknown machine becomes a
parked skeleton that fails `yamlcheck` loudly if unparked unfinished; an
unknown guest becomes generated YAML that `yamlcheck` accepts as it stands; and
a re-run of an unchanged scan is byte-identical. It runs on every branch in
both pipelines.

The one step it cannot take is the last one, because that one needs a NetBox:
merge the branch and run a normal sync, and the serial, the `hw_*` fields, the
new VM and the completed device are all there.
