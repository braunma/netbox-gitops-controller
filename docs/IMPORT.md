# Reverse sync: importing an existing NetBox

The rest of this project runs one way: YAML is the desired state, a merge
request changes it, a reconciler applies it to NetBox. `import` is the other
direction, and it exists for exactly one reason — so a populated NetBox can
adopt this controller without anyone retyping the estate.

```
  live NetBox  ──import──▶  ./data (YAML)  ──review──▶  merge  ──sync──▶  same NetBox  ==  no changes
```

## The one rule

**`import` never writes to NetBox.** It issues GETs (and OPTIONS) only; its
entire effect on the world is a set of files in a target directory. This mirrors
the rule `pkg/ingest` holds in the opposite direction (see
[INGEST.md](INGEST.md)); the client it runs is a dry-run client, and the import
path issues no write regardless.

A second rule follows: **`import` never invents a value.** Anything NetBox does
not hold is omitted. Anything NetBox holds that this schema cannot express is
written to `IMPORT-REPORT.md`, never silently dropped — because a silent gap
becomes data loss the first time someone runs `--prune` (see below).

Output is **deterministic**: no timestamps, run ids, instance URLs or NetBox ids
appear in it. Two imports of an unchanged instance produce identical bytes, so a
re-import is a reviewable diff — which is what `--diff` prints.

## Adopting an existing NetBox

```bash
export NETBOX_URL=https://netbox.example.com
export NETBOX_TOKEN=...

# 1. Generate the repository from the live instance.
netbox-gitops import --data-dir .

# 2. Read IMPORT-REPORT.md. It says what was imported and, more importantly,
#    what was not — the objects and fields this schema cannot represent.

# 3. Check the result against the same instance.
go run ./cmd/yamlcheck definitions inventory --strict

# 4. Commit, open a merge request, review the diff like any other.

# 5. The first sync ADOPTS: it tags every object it now manages, and nothing
#    else. Use --adopt so the first contact cannot reformat a field.
netbox-gitops --adopt

# 6. From here on it is an ordinary GitOps repository.
```

### What the first sync changes, and why that is fine

The first sync after an import will show an update to **every** adopted object.
That is not drift — it is the controller adding its managed `gitops` tag, which
is how it tells an object it manages from one it must leave alone. `--adopt`
restricts that first sync to the tag alone: no other field is written to any
existing object, so first contact is structurally incapable of reformatting
something. Creates (a declared object that does not exist yet) still happen
normally.

After that first tagging sync, a plain sync is a no-op — that is the property
the round-trip test asserts (`0 created, 0 updated, 0 deleted`).

One subtlety worth trusting: the sync only ever compares the fields the YAML
declares. A field NetBox holds that this schema never sets is invisible to the
diff and can never cause churn — which is what makes a *partial* adoption safe.

## `defaults:` extraction, and its two hazards

To keep the output readable rather than a wall of repetition, `import` hoists a
field shared across every item in a file into a `defaults:` block. How much can
be hoisted depends on how the inventory is partitioned: `--split-by site` (the
default) writes one file per site, so `site_slug` hoists; `--split-by rack`
writes one file per rack, so `rack_slug` hoists too. Turn extraction off with
`--no-defaults`; raise the floor with `--defaults-min-items`.

The rule is deliberately conservative, because two ways of getting it wrong are
silent:

- **A default cannot be un-inherited.** An item that omits a key inherits the
  default, so a key is hoisted **only when every item in the file carries it
  with an identical value**. Because the models omit zero-valued fields, a key
  any item leaves at its zero value is simply absent and therefore never
  hoisted. This is the invariant that stops a hoisted `rack_slug` from
  teleporting a device that has none into another device's rack.
- **Tags merge by union.** So a whole tag list is hoisted only when it is
  identical across the file; otherwise the tags stay per-item.

Identity keys (`name`, `slug`, `vid`, `prefix`, `model`) and per-object
structural keys (interfaces, modules, custom fields) are never hoisted.

## What is not imported

NetBox models more than this schema does. `IMPORT-REPORT.md` names every gap;
the standing ones include regions, site groups and locations; inventory items
and virtual chassis; console and power ports on concrete devices (only their
templates exist here); circuits, providers, wireless, tunnels, L2VPN and FHRP
groups; IP ranges, aggregates, RIRs, ASNs and services; contacts, config
contexts and journal entries. And shapes this schema cannot express: a VLAN with
no site, a prefix scoped to a region rather than a site, an IP assigned to
nothing, a second IP on one interface, a cable with more than one termination
per end.

### `--site` does not filter IPAM the way you might expect

A `--site` filter restricts site-scoped objects, but much of IPAM has no site to
filter on, and the gap is invisible unless you know to look:

- **VRFs have no site at all.**
- **IP addresses have no site field** — an address's site is inferable only
  through the interface it is assigned to, and an unassigned address is
  inferable from nothing.
- **A prefix's scope may be a site, but may equally be a region, a location, or
  nothing** — container prefixes at the top of a hierarchy are commonly global.

So a site-filtered import produces a genuinely partial IPAM picture *by design*.
The report counts and names every IPAM object a site filter excluded, so the gap
is at least visible.

## ⚠️ Pruning against a partial import is data loss

`--prune` deletes every `gitops`-tagged object no longer declared in YAML. If an
import left objects behind — anything under "skipped" or a whole un-modelled
kind — and you later prune, those objects are deleted as orphans. **Read
`IMPORT-REPORT.md` before enabling `--prune`, and never prune against an import
you have not confirmed is complete for the kinds you manage.**

## The sandbox rehearsal

You can rehearse a full adoption against the real NetBox without touching a
single production object: import the estate rewritten onto a scratch site (and a
scratch VRF, with prefixed names), apply it, inspect it, then throw the scratch
site away and adopt for real.

Why the rewrite needs all three flags: rewriting `site_slug` only changes the
identity of *site-scoped* objects. A device is matched by name-within-site, so a
name prefix keeps a rewritten device from colliding with production. But a prefix
is identified by CIDR and an IP by its address — neither carries a site — so
without a **scratch VRF** a rewritten prefix would match and re-scope the
production prefix. Hence the guards: `--rewrite-site` requires `--name-prefix`
(or an explicit `--no-name-prefix`), and `--rewrite-site` with the network phase
requires `--rewrite-vrf`.

The rewrite flags are **flag-only** — never read from the environment — so a
stray `REWRITE_SITE` left in CI variables cannot silently rewrite every future
import.

```bash
# 1. Import a sandbox copy: every site -> "sandbox", names prefixed, all IPAM in
#    a scratch VRF, every object tagged "sandbox".
netbox-gitops import --data-dir sandbox/ \
  --rewrite-site '*=sandbox' --name-prefix 'sbx-' --rewrite-vrf 'sbx'

# 2. It must validate.
go run ./cmd/yamlcheck sandbox/definitions sandbox/inventory --strict

# 3. Preview with the destination guard armed. Confirm every create lands in the
#    sandbox site and the only out-of-site touches are shared definition objects.
netbox-gitops --data-dir sandbox/ --dry-run --assert-site sandbox --output json

# 4. Apply with the guard; a second apply must be a no-op.
netbox-gitops --data-dir sandbox/ --assert-site sandbox
netbox-gitops --data-dir sandbox/ --assert-site sandbox   # 0 created, 0 updated, 0 deleted

# 5. Inspect in the UI. Then clean up by pruning the sandbox dataset — empty the
#    inventory and prune, so prefixes and addresses go before the scratch VRF
#    (the reverse dependency order handles that; deleting the site by hand would
#    strand the VRF and its contents).

# 6. Only now, adopt for real (step 5 of "Adopting an existing NetBox" above).
```

The `--assert-site` guard resolves every planned create and update to a site
before writing anything and aborts (exit 3) if any lands — or already sits —
outside the allowed set, which is what catches a write about to move a
production object. Site-less shared objects (tags, roles, device types) are
allowed, since adopting them is the point; the run lists which it touched.
`--assert-site` and `--adopt` compose: a real multi-site first adoption wants
both — tags only, and only in the sites you named.

### What the rehearsal does and does not prove

It proves the **mechanism**: that import round-trips, that the phases order
correctly, that the guards hold. It does **not** predict the real adoption's
plan, because rewritten names and a scratch VRF mean the objects being matched
are different objects. A clean second apply in the sandbox is not evidence that
the real first apply will be clean. The evidence for that is a plain `import`
followed by `--dry-run --adopt`, read by a human.

A residue is expected and intended: the shared definition objects the rehearsal
created (device types, roles, tags) stay behind after you delete the scratch
site — they are what production will use too.
