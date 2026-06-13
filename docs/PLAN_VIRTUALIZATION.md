# Implementation Plan: Virtualization Support

**Feature:** Manage NetBox's `virtualization` app declaratively — cluster
types, cluster groups, clusters, virtual machines, and VM interfaces (with IP
and VLAN assignment) — the same way the tool already manages DCIM devices.
Per decision §11.3, this also brings **platforms** (`dcim/platforms`) and
**tenants/tenant groups** (`tenancy`) under management, reconciled in the
foundation phase since devices and VMs both reference them.

**Status:** Planned. Tracked as feature #6 in `docs/MISSING_FEATURES.md`.

**Guiding principle:** mirror the existing device/interface patterns
(`pkg/reconciler/devices.go`, `pkg/reconciler/network.go`). Everything below
reuses the established `client.Apply` idempotency, managed-tag injection, cache,
plan summary, dry-run, and prune machinery — no new core infrastructure.

---

## 1. Scope

Objects to reconcile, in dependency order. The first three rows are the
cross-cutting **platform/tenant** objects (decision §11.3), reconciled in the
**foundation phase**; the rest are the `virtualization` app, reconciled in the
new virtualization phase.

| Order | Object | Endpoint | Phase | References |
|-------|--------|----------|-------|------------|
| F1 | Platform | `dcim/platforms` | foundation | manufacturer (opt) |
| F2 | Tenant group | `tenancy/tenant-groups` | foundation | parent group (opt) |
| F3 | Tenant | `tenancy/tenants` | foundation | tenant group (opt) |
| 1 | Cluster type | `virtualization/cluster-types` | virt | — (global) |
| 2 | Cluster group | `virtualization/cluster-groups` | virt | — (global) |
| 3 | Cluster | `virtualization/clusters` | virt | cluster type (req), cluster group, site, tenant |
| 4 | Virtual machine | `virtualization/virtual-machines` | virt | cluster / site (one req), role, platform, tenant, status |
| 5 | VM interface | `virtualization/interfaces` | virt | virtual machine (req), VLANs, parent/bridge |
| 6 | IP on VM interface | `ipam/ip-addresses` | virt | `assigned_object_type=virtualization.vminterface` |

VM interfaces reuse the same VLAN/IP/primary-IP semantics as device interfaces.
Setting a VM's primary IP writes `primary_ip4`/`primary_ip6` on the VM, exactly
like `setPrimaryIP` does for devices today. Platforms and tenants are *managed*
in foundation but only *referenced* (by slug, via cache) from clusters/VMs.

### Out of scope (future work)
- VM disks as first-class objects (NetBox 3.7+ `virtual-disks`); the VM-level
  `disk` field is supported, individual disks are not.
- Cables to VM interfaces (NetBox does not cable VM interfaces).
- Wiring platform/tenant references onto *devices* (the device reconciler is
  unchanged here); only VMs/clusters consume them in this feature.

---

## 2. Data model & directory layout

New definition folders under `definitions/` (and mirrored in `example/`):

```
definitions/
  platforms/            # *.yaml  → []models.Platform      (foundation phase)
  tenant_groups/        # *.yaml  → []models.TenantGroup    (foundation phase)
  tenants/              # *.yaml  → []models.Tenant         (foundation phase)
  virtualization/
    cluster_types/      # *.yaml  → []models.ClusterType
    cluster_groups/     # *.yaml  → []models.ClusterGroup
    clusters/           # *.yaml  → []models.Cluster
inventory/
  virtual/              # *.yaml  → []models.VMConfig   (VMs live with inventory)
```

VMs live under `inventory/virtual/` to parallel `inventory/hardware/{active,passive}`,
since they are instance-level inventory rather than foundation definitions.

Example VM YAML (mirrors the device interface/IP/VLAN shape):

```yaml
- name: web-01
  cluster: prod-cluster        # required (or site for non-clustered VMs)
  role_slug: server
  status: active
  vcpus: 4
  memory: 8192                 # MB
  disk: 100                    # GB
  platform: ubuntu-22-04       # optional, managed in foundation, referenced by slug
  tenant: acme-corp            # optional, managed in foundation, referenced by slug
  interfaces:
    - name: eth0
      enabled: true
      mtu: 1500
      mode: access
      untagged_vlan: mgmt
      ip:
        address: 10.0.0.10/24
        vrf: prod
      address_role: primary    # → sets VM primary_ip4/6
```

---

## 3. New Go types

Follow the struct-tag conventions in `pkg/models/devices.go` (`yaml`/`json`
tags, `omitempty`, `validate:"required"` for documentation/parity).

### 3a. Foundation types — `pkg/models/foundation.go` (extend existing file)

```go
type Platform struct {
    Name         string  // required
    Slug         string  // required
    Manufacturer string  `yaml:"manufacturer,omitempty"` // optional, by slug
    Description  string
}

type TenantGroup struct {
    Name        string  // required
    Slug        string  // required
    ParentSlug  string  `yaml:"parent_slug,omitempty"`
    Description string
}

type Tenant struct {
    Name        string  // required
    Slug        string  // required
    GroupSlug   string  `yaml:"group_slug,omitempty"`
    Description string
    Tags        []string
}
```

### 3b. Virtualization types — `pkg/models/virtualization.go`

```go
type ClusterType  struct { Name, Slug, Description string }
type ClusterGroup struct { Name, Slug, Description string }

type Cluster struct {
    Name        string  // required
    TypeSlug    string  `yaml:"type_slug"`   // required → cluster type
    GroupSlug   string  `yaml:"group_slug,omitempty"`
    SiteSlug    string  `yaml:"site_slug,omitempty"`
    Tenant      string  `yaml:"tenant,omitempty"`  // tenant slug, looked up via cache
    Status      string
    Description string
    Tags        []string
}

type VMInterfaceConfig struct {
    Name, Description, Mode string
    Enabled      bool
    MTU          int
    MACAddress   string   `yaml:"mac_address,omitempty"`
    UntaggedVLAN string   `yaml:"untagged_vlan,omitempty"`
    TaggedVLANs  []string `yaml:"tagged_vlans,omitempty"`
    Parent       string   `yaml:"parent,omitempty"`  // parent VM interface name
    IP           *IPConfig
    AddressRole  string   `yaml:"address_role,omitempty"`
    Tags         []string
}

type VMConfig struct {
    Name        string  // required
    Cluster     string  // at least one of Cluster / SiteSlug (decision §11.2)
    SiteSlug    string  `yaml:"site_slug,omitempty"`
    RoleSlug    string  `yaml:"role_slug,omitempty"`
    Platform    string  `yaml:"platform,omitempty"` // platform slug, via cache
    Tenant      string  `yaml:"tenant,omitempty"`   // tenant slug, via cache
    Status      string
    VCPUs       int
    Memory      int
    Disk        int
    Tags        []string
    Interfaces  []VMInterfaceConfig
}
```

Reuse the existing `IPConfig` and `LinkConfig`-style helpers from
`pkg/models/devices.go`. Add a `Slug()` helper on `VMConfig` if needed.

### Validation — extend `pkg/models/validate.go`
Add a `Validate() error` to each new type (every model already has one;
the loader calls it via `validateItems`). Cross-field rules to enforce:
- `Platform`/`Tenant`/`TenantGroup`: `Name` and `Slug` required.
- `Cluster.TypeSlug` required; `Cluster.Name` required.
- `VMConfig`: require **at least one** of `Cluster` / `SiteSlug` (decision
  §11.2 — a VM belongs to a cluster or a site; NetBox permits both).
- `VMInterfaceConfig.Name` required; `IPConfig.Address` required when `IP` set.

Add tests in `pkg/models/validate_test.go`.

---

## 4. Loader — `pkg/loader/loader.go`

Add public helpers mirroring `LoadDevices`: `LoadPlatforms`, `LoadTenantGroups`,
`LoadTenants`, `LoadClusterTypes`, `LoadClusterGroups`, `LoadClusters`,
`LoadVMs`.

**Note the existing tech debt:** `loadFile` uses an explicit `switch t :=
target.(type)` with one case per model type. Each new slice type
(`*[]*models.Platform`, `*[]*models.TenantGroup`, `*[]*models.Tenant`,
`*[]*models.ClusterType`, `*[]*models.ClusterGroup`, `*[]*models.Cluster`,
`*[]*models.VMConfig` — 7 cases) needs its own block, each calling
`validateItems` then appending — copy the existing block verbatim. (This is
exactly the duplication the §4.2 generic-reconciler refactor in
`AUDIT_AND_ROADMAP.md` would remove; do not block this feature on that
refactor.)

---

## 5. Cache — `pkg/client/cache.go` + `internal/constants/constants.go`

Platform, tenant, and cluster lookups are all **global** (not site-scoped), so
add them to `LoadGlobal`'s `resources` map:

```go
"platforms":      "dcim/platforms",
"tenant_groups":  "tenancy/tenant-groups",
"tenants":        "tenancy/tenants",
"cluster_types":  "virtualization/cluster-types",
"cluster_groups": "virtualization/cluster-groups",
"clusters":       "virtualization/clusters",
```

Also add these to `constants.CacheResourceTypes`. Because foundation now
reconciles platforms/tenants and virtualization reads them from cache, the
relevant `LoadGlobal` entries must be populated before those phases — they
already are, since `LoadGlobal` runs once up front in `runSync`. (Clusters
created earlier in the virtualization phase are looked up live by name +
`cluster_id` during VM reconcile, so a stale cluster cache is not a problem.)

VM interface VLAN lookups must stay **site-aware** (`GetSiteID("vlans", siteID,
…)`), identical to `reconcileInterfaces`. The VM's site is resolved as: the
cluster's `site` if set, else the VM's own `SiteSlug`. The reconciler loads the
relevant site caches via `Cache().LoadSite(slug)` before reconciling VM
interfaces (same as `runDevices` does today).

VMs themselves are looked up live by `name` + `cluster_id` during reconcile
(like devices use `device_id`), so they do not need a global cache entry.

---

## 6. Reconcilers

### 6a. Foundation: platforms & tenants — extend `pkg/reconciler/foundation.go`

Add `ReconcilePlatforms`, `ReconcileTenantGroups`, `ReconcileTenants` to the
existing `FoundationReconciler` — each a trivial `Apply` loop copied from
`ReconcileRoles`/`ReconcileTags`, with `lookup={slug}`:
- platforms → `Apply("dcim", "platforms", …)`; resolve optional `manufacturer`
  via `GetGlobalID("manufacturers", …)`.
- tenant groups → `Apply("tenancy", "tenant-groups", …)`; optional `parent`.
- tenants → `Apply("tenancy", "tenants", …)`; resolve optional `group` via
  cache. Reconcile groups before tenants.

Wire these into `runFoundation` (`cmd/netbox-gitops/main.go`) after roles/tags,
loading them via the new loader helpers.

### 6b. Virtualization — new `pkg/reconciler/virtualization.go`

New `VirtualizationReconciler` struct (constructor `NewVirtualizationReconciler(c)`),
following `FoundationReconciler`/`DeviceReconciler` shape. Methods:

- `ReconcileClusterTypes([]*models.ClusterType)` — trivial `Apply` loop (copy
  `ReconcileTags`), `lookup={slug}`, app=`virtualization` endpoint=`cluster-types`.
- `ReconcileClusterGroups(...)` — same shape, endpoint `cluster-groups`.
- `ReconcileClusters(...)` — resolve `type`/`group`/`site` IDs from cache
  (`GetGlobalID`), build payload, `Apply` with `lookup={name}`. Mirror
  `ReconcileVRFs`/`ReconcileVLANGroups`. Use `MarkReconcileIncomplete` when a
  referenced type/site is missing, so prune stays safe.
- `ReconcileVMs([]*models.VMConfig)` — for each VM:
  - resolve `cluster` (cache `clusters`), `role` (cache `roles`), `platform`
    (cache `platforms`), `tenant` (cache `tenants`), `site` — all optional
    except the cluster/site requirement from §11.2.
  - `Apply("virtualization", "virtual-machines", lookup, payload)` with
    `lookup={name, cluster_id}`.
  - then `reconcileVMInterfaces(vmID, vm)` — a near-copy of
    `reconcileInterfaces` but: `payload["virtual_machine"]=vmID` (no `device`),
    endpoint `virtualization/interfaces`, no `link`/cable queueing.
  - IP assignment via a `reconcileVMIP` helper modelled on
    `reconcileIPAddress`, with `assigned_object_type=virtualization.vminterface`.
  - primary IP via a `setVMPrimaryIP` helper modelled on `setPrimaryIP`,
    patching `virtual-machines` instead of `devices`.

To keep `Apply` idempotency working, VLAN/IP lookups and `lookup` keys must
match how NetBox returns them (the existing diff logic in `client.go` already
handles nested objects and ID-set list fields like `tagged_vlans`).

---

## 7. CLI / phase wiring — `cmd/netbox-gitops/main.go`

1. Add `"virtualization"` to `validPhases` (after `"devices"`):
   `{"foundation", "network", "device-types", "devices", "virtualization"}`.
2. Add a **Phase 4: Virtualization** block after the devices phase, gated on
   `phases["virtualization"]`, calling a new `runVirtualization(c, dataLoader,
   logger)`.
3. `runVirtualization` (model it on `runNetwork` + `runDevices`):
   - load cluster types/groups/clusters and reconcile (foundation-style).
   - add a `--vm <name>` flag (decision §11.1): a package-level `vmFilter`
     string registered in `main()` next to `--device`, and a `filterVMs`
     helper mirroring `filterDevices` (match on `SiteSlug` via `--site` and
     `Name` via `--vm`). Like `--prune`+`--site/--device`, reject
     `--prune` + `--vm`.
   - load VMs from `inventory/virtual`; apply `--site`/`--vm`.
   - load site caches for the sites referenced by VMs/clusters (copy the
     `uniqueSites` loop from `runDevices`; a clustered VM's site comes from its
     cluster — see §5), then `ReconcileVMs`.

Virtualization depends on foundation (sites/roles) and network (VLANs), so it
must run after both. It does **not** depend on device-types or the device phase.

---

## 8. Pruning — `pruneTargets` in `main.go` + `pkg/client/prune.go`

Add a `phases["virtualization"]` branch to `pruneTargets`, in reverse
dependency order (children before parents). **Placement matters:** because the
`targets` slice is consumed in order and VM interfaces reference VLANs while
clusters reference sites/tenants, the virtualization branch must be appended
**after the `devices` branch and before `network`/`foundation`**, so VMs and
clusters are deleted before the VLANs/sites/tenants they point at.

IP addresses are already pruned under the `devices` branch
(`ipam/ip-addresses`); to avoid double-listing, prune IPs once — keep the
existing entry and rely on it covering VM-interface IPs too (they carry the
`gitops` tag and the same endpoint). VM-specific targets:

```go
if phases["virtualization"] {
    targets = append(targets,
        client.PruneTarget{App: "virtualization", Endpoint: "interfaces"},
        client.PruneTarget{App: "virtualization", Endpoint: "virtual-machines"},
        client.PruneTarget{App: "virtualization", Endpoint: "clusters"},
        client.PruneTarget{App: "virtualization", Endpoint: "cluster-groups"},
        client.PruneTarget{App: "virtualization", Endpoint: "cluster-types"},
    )
}
```

Platforms and tenants are pruned under the **foundation** branch (after
racks/roles/sites, before tags), in reverse dependency order — tenants before
tenant groups, platforms independently:

```go
// within the existing phases["foundation"] branch
client.PruneTarget{App: "dcim",    Endpoint: "platforms"},
client.PruneTarget{App: "tenancy", Endpoint: "tenants"},
client.PruneTarget{App: "tenancy", Endpoint: "tenant-groups"},
```

⚠️ **Ordering caveat:** clusters/VMs reference tenants, and devices may too.
Pruning a tenant still referenced by a non-managed object will 409. Since the
virtualization phase reconciles *after* foundation but prune runs *after all
phases*, the reverse-dependency order across phases already deletes VMs/clusters
before tenants. Confirm with the prune test (§10).

Verify `prune.go` lists by `tag=gitops` per endpoint and that
`virtualization/interfaces` and `tenancy/tenants` accept the tag filter (they
do in NetBox).

---

## 9. Constants — `internal/constants/constants.go`

- Add `TerminationVMInterface = "virtualization.vminterface"` (parallels the
  existing `TerminationInterface`), used as `assigned_object_type` for VM IPs.
- Add the cluster cache resource names to `CacheResourceTypes`.

---

## 10. Tests

- **Unit (primary):** `pkg/reconciler/virtualization_reconcile_test.go` driven
  by the in-process fake in `pkg/reconciler/fakenetbox_test.go`. Extend the fake
  to serve the new `virtualization/*` endpoints (it is endpoint-generic; confirm
  it does not hardcode DCIM paths). Cover:
  - create + idempotent re-apply (second run = no-op) for each object type
  - VM interface with untagged + tagged VLANs (site-scoped lookup)
  - VM IP assignment + primary IP set on the VM
  - missing cluster/site → `MarkReconcileIncomplete`, no panic
- **Foundation:** extend `foundation_reconcile_test.go` for platforms, tenant
  groups, and tenants (create + idempotent re-apply; optional group/manufacturer
  resolution).
- **Model:** validation cases in `pkg/models/validate_test.go` (incl. the
  exactly-one-of cluster/site rule and platform/tenant required fields).
- **Prune:** extend `prune_reconcile_test.go` to assert an orphaned VM is
  deleted and a non-gitops VM is left untouched, and that a tenant still
  referenced by a managed VM is only removed after the VM (cross-phase order).

Target: keep package coverage at/above the current ~73% CI threshold.

---

## 11. Decisions (resolved 2026-06-13)

1. **VM name filtering flag → add `--vm` now.** A dedicated `--vm <name>` flag
   is added alongside the existing `--device`, scoping VM reconciliation to one
   VM. `--site` continues to scope by site. (See §7.)
2. **Non-clustered VMs → support both.** A VM may belong to a cluster **or**
   just a site. `VMConfig.Validate()` requires at least one of `Cluster` /
   `SiteSlug` to be set (NetBox permits both simultaneously, so both is allowed).
3. **Platforms & tenants → manage them.** Platforms (`dcim/platforms`) and
   tenants + tenant groups (`tenancy/tenants`, `tenancy/tenant-groups`) become
   first-class declarable, reconciled objects. Because devices reference them
   too, they are reconciled in the **foundation phase** (before devices and
   virtualization) and merely *referenced* by slug from VMs/devices. This adds
   scope — see the platform/tenant rows threaded through §§1, 3–6, 8, 12.

---

## 12. Suggested implementation order (PR-sized chunks)

1. **Models + validation + loader** for *all* new types (foundation:
   `Platform`, `TenantGroup`, `Tenant`; virtualization: cluster types/groups,
   `Cluster`, `VMConfig`) in `foundation.go`/`virtualization.go`, `validate.go`,
   `loader.go`, with `validate_test.go`. Compiles and loads YAML; no behavior
   wired yet.
2. **Cache + constants** — global platform/tenant/cluster lookups +
   `CacheResourceTypes`.
3. **Foundation: platforms & tenants** — `ReconcilePlatforms`,
   `ReconcileTenantGroups`, `ReconcileTenants` + wire into `runFoundation`. Add
   foundation reconcile tests.
4. **Virtualization: clusters** (types/groups/clusters) + phase wiring in
   `main.go` (`runVirtualization`, add `virtualization` to `validPhases`). Add
   cluster reconcile test.
5. **Virtualization: VMs + VM interfaces + IPs/primary IP** + `--vm` flag +
   tests (incl. cluster-site VLAN path and `tagged_vlans` idempotency).
6. **Pruning** — foundation (platforms/tenants) + virtualization branches, with
   the cross-phase ordering from §8, + prune test.
7. **Example data** under `example/` + docs: update `README.md` feature list,
   `EXAMPLES.md`, and flip feature #6 to ✅ in `docs/MISSING_FEATURES.md`.

Each chunk is independently shippable and verifiable with `--dry-run` against a
live NetBox: a second run must produce a zero-change plan (idempotency).

---

## 13. Risks

- **Idempotency drift on VM interface list fields** (`tagged_vlans`) — covered
  by the existing `idSetEqual` logic in `client.go`; add an explicit test.
- **Site resolution for VM VLANs** — a clustered VM's site comes from the
  cluster, not the VM; get this wrong and VLAN lookups silently miss. Test the
  cluster-site path explicitly.
- **Fake-NetBox endpoint coverage** — if `fakenetbox_test.go` hardcodes DCIM
  paths, it needs generalizing first; check before chunk 3.
- **Loader type-switch duplication** grows by 4 cases. Accepted as existing
  debt; do not refactor here.
</content>
</invoke>
