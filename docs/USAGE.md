# Running the controller

How to configure the controller and what each way of running a sync does. Every flag and variable is listed in [Configuration](CONFIGURATION.md).

## Environment Configuration

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
[`docs/CONFIGURATION.md`](CONFIGURATION.md).

## Dry-Run (Simulation)

Shows what *would* be applied without writing to NetBox. **Always run this first\!** One limit: a dry run cannot plan what depends on an object that does not exist yet, so the interfaces, cables and blades of a device that is still to be created appear only in the next plan.

```bash
./netbox-gitops --dry-run
```

Every dry run ends with a plan summary (an apply prints the same counts as
`Summary: 3 created, …`):

```text
Plan: 3 to create, 1 to update, 0 to delete, 41 unchanged
```

## Apply Changes

Executes the synchronization against the NetBox API.

```bash
./netbox-gitops
```

## What a sync looks at

When the data directory is in a git repository, a sync is **change-based**: it
compares the YAML as it is now with the YAML at the commit NetBox was last
brought up to date with, and reconciles only the objects that differ. A dry
run on a branch that does not contain that commit compares against the point
where the branch left the main line. An
unchanged repository costs three requests; one edited server costs that
server. Every run starts by saying what it picked and why:

```text
Scope: changes since 4f1c2a9b7e — the last completed sync (2026-11-27T22:14:03Z)
  Everything else is left as it is: an edit made in NetBox by hand is neither seen nor reverted. --full reconciles everything.
  Reconciling 3 of 212 devices, 0 of 4 VMs; definition groups: device-types
  • device type example-server-r100: changed: interfaces [definitions/device_types/example-server.yaml]
  • device example-switch-01: changed: serial [inventory/hardware/active/switches.yaml]
  • device example-server-01: its device type example-server-r100 changed
  • device example-server-02: changed: interfaces [inventory/hardware/active/servers.yaml]
  No longer declared since 4f1c2a9b7e (1). A change-based sync deletes nothing; the next full sync with --prune removes what the controller manages of these:
    ✗ interface eth3 of example-server-02
```

How the selection is made:

  * **On the resolved result, not on files.** Both commits are loaded the way
    a sync loads them, with `defaults`, derived values and cable paths
    applied. A device whose file was not touched is still picked when a
    `defaults` block gave it a different value; a file that was only
    reformatted picks nothing.
  * **Definitions run by group.** A changed tag, role, custom field, platform,
    tenant group, tenant, site or rack runs the whole foundation phase; a
    changed VRF, VLAN group, VLAN or prefix the whole network phase. Of device
    and module types only the changed ones are reconciled. The cluster
    definitions (cluster types, groups, clusters) run when one of them changed
    or a VM was picked. A group with no change is skipped, as with `--only`.
  * **Devices and VMs are picked one by one**, plus what depends on a change:
    the devices of a changed device or module type, the children of a
    reconciled parent device, a device declaring a cable to a changed device
    or through a changed patch panel, the VMs of a changed cluster.

What it does not do — and a full sync does:

  * **It does not see drift.** An object outside the groups and devices that
    run is not read from NetBox, so an edit made there by hand stays until a
    full sync.
  * **It prunes nothing**, even with `--prune`: pruning deletes every managed
    object a run did not reconcile, which here would be nearly all of them.
    What is no longer declared is listed, and removed by the next
    `--full --prune`.

```bash
./netbox-gitops --full            # reconcile everything
./netbox-gitops --full --prune    # ...and delete what is no longer declared
./netbox-gitops --dry-run --full --detailed-exitcode   # drift check: exit 2 = NetBox differs
./netbox-gitops --changed-since HEAD~3   # compare against a commit you name
./netbox-gitops --dry-run --verbose      # also print everything that was looked at and left alone
```

**What a sync prints.** The scope, each change with its fields, warnings and
the summary. Objects that were compared and found equal are counted in the
summary (`N unchanged`) and not listed; `--verbose` lists them, together with
what was loaded, every derived value and every cable path.

**Where the last commit is kept.** After a sync that covered everything it
was meant to, the controller writes the commit into NetBox itself: the
description of a tag named `gitops-state`, which is never attached to
anything. The note describes NetBox, so it lives there — restore NetBox from a
backup and the note goes back with it. Do not edit or declare that tag.

**When a sync is full anyway.** Whenever comparing would be a guess, the run
is a full one and its first lines say why:

| Situation | Why |
|---|---|
| The data directory is not in a git repository, or `git` is not installed (the distroless Docker image) | there is nothing to compare against |
| NetBox has no `gitops-state` tag yet | first sync |
| The last sync failed, or was limited with `--only`/`--site`/`--device`/`--vm` | NetBox may hold part of a commit; the record was withdrawn when that run started |
| The recorded commit is not in this clone | a shallow clone (set `GIT_DEPTH: 0` in CI to avoid it), or rewritten history |
| The controller's own `*.go`, `go.mod` or `go.sum` changed since that commit | unchanged YAML may now be applied differently |
| Ignore patterns, data directory or library paths differ from the recorded run | another set of files is loaded |
| A type library lies outside the repository | what it held at the last sync is unknown |
| The YAML at the recorded commit no longer loads | nothing to compare with |
| The sync state cannot be read from NetBox, the repository has no commit yet, or git cannot diff against the recorded commit | nothing reliable to compare with |

A run from a data directory with uncommitted changes is applied but not
recorded; if it changed anything, the sync after it is a full one too. If the controller is
installed separately from the data repository, run `--full` once after
upgrading it: the code check above only sees a controller that lives in the
same repository.

`--only`, `--site`, `--device` and `--vm` set the scope by hand; such a run is
not change-based.

## Drift Detection (CI-friendly)

With `--detailed-exitcode` the process exits with code `2` when changes are
pending (exit `0` means NetBox is in sync with the YAML). Drift is what nobody
changed in the YAML, so the check has to be a full plan. A scheduled CI job
becomes a drift monitor with zero extra infrastructure:

```bash
./netbox-gitops --dry-run --full --detailed-exitcode
```

## Machine-Readable Plan Output

`--output json` prints the planned (or applied) changes as JSON on stdout
at the end of a successful run;
all logs move to stderr so stdout stays clean for parsing. Useful for posting
a plan comment on a merge request:

```bash
./netbox-gitops --dry-run --output json 2>/dev/null > plan.json
```

```json
{
  "dry_run": true,
  "scope": { "mode": "changes", "base": "4f1c2a9b7e…" },
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

`scope.mode` is `full` (with a `reason`) or `changes` (with `base`, the list
`changed` of picked objects and `no_longer_declared`).

## Selective Sync

Restrict a run to specific phases or devices — handy for debugging and
targeted hotfixes:

```bash
# Only reconcile foundation objects (tags, roles, custom fields, platforms,
# tenants, sites, racks)
./netbox-gitops --only foundation

# Only network (VRFs, VLAN groups, VLANs, prefixes) and device types
./netbox-gitops --only network,device-types

# Only devices of one site
./netbox-gitops --only devices --site berlin-dc

# A single device (without --only, definitions and VMs still run in full)
./netbox-gitops --only devices --device srv-web-01

# Only virtual machines, and just one of them
./netbox-gitops --only virtualization --vm web-01
```

Valid `--only` values: `foundation`, `network`, `device-types`, `devices`,
`virtualization`. `--site` filters the device and virtualization phases by site
slug; `--device` restricts the devices phase to a single device; `--vm` filters a single virtual
machine by name. Selected phases always run in the fixed dependency order — see
[Phase Order](CONCEPTS.md#phase-order-dependency-model) — regardless of the order you list
them in.

> **Note:** Skipped phases are not validated — if you sync `--only devices`,
> the referenced sites, roles and device types must already exist in NetBox.
> Likewise `--only virtualization` assumes its sites, roles, platforms, tenants
> and VLANs already exist.
>
> **Note:** `--site` matches a VM only if the VM declares that `site`
> directly; a clustered VM (whose site comes from its cluster) is not matched by
> `--site`. Use `--vm` to target such a VM by name.

## Pruning Orphans

By default the controller only creates and updates objects. With `--prune` it
also deletes orphans: objects that still carry the `gitops` managed tag but are
no longer declared in YAML. Pruning leaves manually created objects (those
without the tag) alone. Separately from pruning, a cable that occupies a port a
declared `link:` needs is deleted to make room, with or without `--prune` and
whether or not it carries the tag.

```bash
# Preview what pruning would delete (no changes applied)
./netbox-gitops --dry-run --full --prune

# Apply, deleting orphaned managed objects
./netbox-gitops --full --prune

# Prune only the network phase's orphans (VRFs, VLAN groups, VLANs, prefixes)
./netbox-gitops --only network --prune
```

Only a full sync prunes. A [change-based sync](#what-a-sync-looks-at) given
`--prune` lists what is no longer declared and leaves it in NetBox: it has
reconciled only what changed, and pruning deletes whatever a run did not
reconcile. Outside a git repository every sync is full, so `--prune` alone
behaves as it always did.

The GitLab pipeline in this repository passes `--prune` to all four sync jobs.
A merge request that removes an object from the YAML shows it under "No longer
declared" in `go_plan`; `go_apply` leaves it, and `go_apply_full` (a button;
in a scheduled pipeline only with `NETBOX_SCHEDULED_APPLY=true`) deletes it. Set the CI/CD variable
`NETBOX_PRUNE=false` to turn deletion off.

Pruning runs after all selected phases and deletes endpoints in reverse
dependency order (device/VM children first, then sites/platforms/tenants
last) to respect NetBox foreign-key constraints. It is scoped to the phases that
run via `--only`, and cannot be combined with `--site`/`--device`/`--vm` (a
filtered run would delete the out-of-scope objects the filter excluded). **Tags
and custom fields are never pruned:** a tag removed from `definitions/extras` stays in NetBox,
along with every object's use of it, because other people and tools attach
tags too. Delete an unwanted tag by hand in NetBox. Device children
(interfaces, IP addresses, front/rear ports, modules) are pruned too, and so
are cables: every cable the controller creates carries the managed tag, a
cable it finds already wired as declared is adopted (tagged) on the next run,
and a managed cable no longer declared is pruned like any other object. One
exception to "only managed objects are deleted": a cable with no terminations
left — what NetBox keeps when a port or device is deleted — is swept whether
or not it carries the tag, because nothing can ever reach it again. The
ports a device type template defines are never pruned, even when the YAML
once listed one of them and stops doing so: they are the hardware's own.
