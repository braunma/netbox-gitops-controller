# Implementation Plan: Virtualization Support

**Feature:** Manage NetBox's `virtualization` app declaratively — cluster
types, cluster groups, clusters, virtual machines, and VM interfaces (with IP
and VLAN assignment) — the same way the tool already manages DCIM devices.

**Status:** Planned. Tracked as feature #6 in `docs/MISSING_FEATURES.md`.

**Guiding principle:** mirror the existing device/interface patterns
(`pkg/reconciler/devices.go`, `pkg/reconciler/network.go`). Everything below
reuses the established `client.Apply` idempotency, managed-tag injection, cache,
plan summary, dry-run, and prune machinery — no new core infrastructure.

---

## 1. Scope

NetBox objects to reconcile (NetBox `virtualization` app), in dependency order:

| Order | Object | Endpoint | References |
|-------|--------|----------|------------|
| 1 | Cluster type | `virtualization/cluster-types` | — (global) |
| 2 | Cluster group | `virtualization/cluster-groups` | — (global) |
| 3 | Cluster | `virtualization/clusters` | cluster type (req), cluster group, site |
| 4 | Virtual machine | `virtualization/virtual-machines` | cluster (req) / site, role, platform, status |
| 5 | VM interface | `virtualization/interfaces` | virtual machine (req), VLANs, parent/bridge |
| 6 | IP on VM interface | `ipam/ip-addresses` | `assigned_object_type=virtualization.vminterface` |

VM interfaces reuse the same VLAN/IP/primary-IP semantics as device interfaces.
Setting a VM's primary IP writes `primary_ip4`/`primary_ip6` on the VM, exactly
like `setPrimaryIP` does for devices today.

### Out of scope (future work)
- Platforms and tenants as *managed* objects — accepted by slug and looked up
  live/cached, but their own reconciliation is a separate feature.
- VM disks as first-class objects (NetBox 3.7+ `virtual-disks`); the VM-level
  `disk` field is supported, individual disks are not.
- Cables to VM interfaces (NetBox does not cable VM interfaces).

---

## 2. Data model & directory layout

New definition folders under `definitions/` (and mirrored in `example/`):

```
definitions/
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
  platform: ubuntu-22-04       # optional, looked up live
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

## 3. New Go types — `pkg/models/virtualization.go`

Follow the struct-tag conventions in `pkg/models/devices.go` (`yaml`/`json`
tags, `omitempty`, `validate:"required"` for documentation/parity).

```go
type ClusterType  struct { Name, Slug, Description string }
type ClusterGroup struct { Name, Slug, Description string }

type Cluster struct {
    Name        string  // required
    TypeSlug    string  `yaml:"type_slug"`   // required → cluster type
    GroupSlug   string  `yaml:"group_slug,omitempty"`
    SiteSlug    string  `yaml:"site_slug,omitempty"`
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
    Cluster     string  // required unless SiteSlug set
    SiteSlug    string  `yaml:"site_slug,omitempty"`
    RoleSlug    string  `yaml:"role_slug,omitempty"`
    Platform    string  `yaml:"platform,omitempty"`
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
- `Cluster.TypeSlug` required; `Cluster.Name` required.
- `VMConfig`: require `Cluster` **or** `SiteSlug` (a VM must belong to one).
- `VMInterfaceConfig.Name` required; `IPConfig.Address` required when `IP` set.

Add tests in `pkg/models/validate_test.go`.

---

## 4. Loader — `pkg/loader/loader.go`

Add public helpers mirroring `LoadDevices`:
`LoadClusterTypes`, `LoadClusterGroups`, `LoadClusters`, `LoadVMs`.

**Note the existing tech debt:** `loadFile` uses an explicit `switch t :=
target.(type)` with one case per model type. Each new slice type
(`*[]*models.ClusterType`, `*[]*models.ClusterGroup`, `*[]*models.Cluster`,
`*[]*models.VMConfig`) needs its own case, each calling `validateItems` then
appending — copy the existing block verbatim. (This is exactly the duplication
the §4.2 generic-reconciler refactor in `AUDIT_AND_ROADMAP.md` would remove; do
not block this feature on that refactor.)

---

## 5. Cache — `pkg/client/cache.go` + `internal/constants/constants.go`

Cluster lookups are **global** (not site-scoped), so add them to `LoadGlobal`'s
`resources` map:

```go
"cluster_types":  "virtualization/cluster-types",
"cluster_groups": "virtualization/cluster-groups",
"clusters":       "virtualization/clusters",
```

Also add these to `constants.CacheResourceTypes`.

VM interface VLAN lookups must stay **site-aware** (`GetSiteID("vlans", siteID,
…)`), identical to `reconcileInterfaces`. The VM's site is resolved as: the
cluster's `site` if set, else the VM's own `SiteSlug`. The reconciler loads the
relevant site caches via `Cache().LoadSite(slug)` before reconciling VM
interfaces (same as `runDevices` does today).

VMs themselves are looked up live by `name` + `cluster_id` during reconcile
(like devices use `device_id`), so they do not need a global cache entry.

---

## 6. Reconciler — new `pkg/reconciler/virtualization.go`

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
  - resolve `cluster` (cache `clusters`), `role` (`device-roles` / cache
    `roles`), `platform` (live `Filter` by slug — optional), `site`.
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
   - load VMs from `inventory/virtual`; apply the existing `--site`/`--device`
     filters (rename concept: `--device` already filters by name — reuse
     `siteFilter`; VM name filtering can reuse `deviceFilter` or a follow-up
     `--vm` flag — **decision needed, see §11**).
   - load site caches for the sites referenced by VMs/clusters (copy the
     `uniqueSites` loop from `runDevices`), then `ReconcileVMs`.

Virtualization depends on foundation (sites/roles) and network (VLANs), so it
must run after both. It does **not** depend on device-types or the device phase.

---

## 8. Pruning — `pruneTargets` in `main.go` + `pkg/client/prune.go`

Add a `phases["virtualization"]` branch to `pruneTargets`, in reverse
dependency order (children before parents), consistent with the existing device
ordering. IP addresses are already pruned under the `devices` branch
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

Verify `prune.go` lists by `tag=gitops` per endpoint and that
`virtualization/interfaces` accepts the tag filter (it does in NetBox).

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
- **Model:** validation cases in `pkg/models/validate_test.go`.
- **Prune:** extend `prune_reconcile_test.go` to assert an orphaned VM is
  deleted and a non-gitops VM is left untouched.

Target: keep package coverage at/above the current ~73% CI threshold.

---

## 11. Open decisions (resolve before/while coding)

1. **VM name filtering flag.** Reuse `--device` for VM names too, or add a
   dedicated `--vm` filter? Reusing is cheaper; a separate flag is clearer.
   *Recommendation:* reuse `--site` for scoping and add `--vm` later if needed.
2. **Non-clustered VMs.** NetBox allows a VM with only a site (no cluster).
   Support from day one (validation already allows `Cluster` OR `SiteSlug`), or
   require a cluster initially? *Recommendation:* allow both; it's free.
3. **Platforms/tenants.** Look up live by slug (no management) for now, or model
   them? *Recommendation:* live lookup now; manage later if requested.

---

## 12. Suggested implementation order (PR-sized chunks)

1. **Models + validation + loader** (`virtualization.go`, `validate.go`,
   `loader.go`) with `validate_test.go`. No behavior wired yet — compiles and
   loads YAML.
2. **Cache + constants** — global cluster lookups + `CacheResourceTypes`.
3. **Reconciler: clusters** (`virtualization.go` types/groups/clusters) + phase
   wiring in `main.go` (`runVirtualization`, `validPhases`). Add reconcile test
   for clusters.
4. **Reconciler: VMs + VM interfaces + IPs/primary IP** + tests.
5. **Pruning** branch + prune test.
6. **Example data** under `example/` + docs: update `README.md` feature list,
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
