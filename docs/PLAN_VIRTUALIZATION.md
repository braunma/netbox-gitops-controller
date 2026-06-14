# Virtualization Support — Design Record

**Status:** ✅ Implemented. NetBox's `virtualization` app is reconciled
declaratively — cluster types, cluster groups, clusters, virtual machines and VM
interfaces (with IP/VLAN/primary-IP assignment) — mirroring the DCIM device
flow. Platforms (`dcim/platforms`) and tenants/tenant groups (`tenancy`) are
managed in the **foundation phase** since both devices and VMs reference them.
Tracked as feature #6 in `docs/MISSING_FEATURES.md`.

Implementation: `pkg/reconciler/virtualization.go` (clusters/VMs/VM interfaces),
the platform/tenant methods in `pkg/reconciler/foundation.go`, the
`virtualization` phase + `--vm` flag in `cmd/netbox-gitops/main.go`. Everything
reuses the existing `client.Apply` idempotency, managed-tag injection, cache,
plan summary, dry-run and prune machinery — no new core infrastructure.

## Scope

Objects reconciled, in dependency order:

| Object        | Endpoint                          | Phase      | References                                   |
|---------------|-----------------------------------|------------|----------------------------------------------|
| Platform      | `dcim/platforms`                  | foundation | manufacturer (opt)                           |
| Tenant group  | `tenancy/tenant-groups`           | foundation | parent group (opt)                           |
| Tenant        | `tenancy/tenants`                 | foundation | tenant group (opt)                           |
| Cluster type  | `virtualization/cluster-types`    | virt       | — (global)                                   |
| Cluster group | `virtualization/cluster-groups`   | virt       | — (global)                                   |
| Cluster       | `virtualization/clusters`         | virt       | cluster type (req), group, site, tenant      |
| Virtual machine | `virtualization/virtual-machines` | virt     | cluster / site (≥1 req), role, platform, tenant |
| VM interface  | `virtualization/interfaces`       | virt       | VM (req), VLANs, parent                       |
| IP on VM iface | `ipam/ip-addresses`              | virt       | `assigned_object_type=virtualization.vminterface` |

VM interfaces reuse the device interface VLAN/IP/primary-IP semantics; setting a
VM's primary IP writes `primary_ip4`/`primary_ip6` on the VM. Platforms/tenants
are *managed* in foundation but only *referenced* (by slug, via cache) from
clusters/VMs. VMs and clusters are looked up live by name (+ `cluster_id`)
during reconcile, so they need no global cache entry; VM-interface VLANs stay
site-aware (`GetSiteID`), the VM's site being its cluster's site or its own.

Definitions live under `definitions/platforms`, `definitions/tenant_groups`,
`definitions/tenants`, `definitions/virtualization/{cluster_types,cluster_groups,clusters}`;
VMs under `inventory/virtual/`. See `EXAMPLES.md` for the YAML shape.

## Key decisions

1. **VM cluster/site rule:** a VM must declare **at least one** of `cluster` /
   `site_slug` (NetBox permits both simultaneously, so both is allowed).
2. **Manage platforms & tenants:** because devices reference them too, they are
   first-class declarable objects reconciled in the foundation phase.
3. **`--vm <name>` filter** added alongside `--device`; `--site` scopes by site.
   `--prune` is rejected together with `--vm` (a filtered run would delete the
   out-of-scope objects the filter excluded). `--site` matches a VM only by its
   own `site_slug` — a clustered VM (site inherited from its cluster) is not
   matched; use `--vm` to target it.
4. **Pruning order:** VM interfaces → VMs → clusters → groups → types are pruned
   after the device branch and before network/foundation, so the VLANs, sites
   and tenants they reference still exist. VM-interface IPs are covered by the
   single `ipam/ip-addresses` prune entry. Platforms/tenants prune under
   foundation (tenants before tenant groups).

## Known limitations

- **`vm_role` requirement.** NetBox only allows a VM to use a device role with
  `vm_role: true`; the example data ships a dedicated `vm` role.
- **Clustered-VM VLAN site cache.** VM-interface VLANs resolve against the VM's
  effective site (its own, or its cluster's). Site caches are warmed from the
  `site_slug` of the clusters and VMs declared in the current run. A VM
  referencing a cluster that exists only in NetBox (not in this run's YAML) will
  not resolve its VLANs — they are warned and skipped, never mis-assigned. The
  normal GitOps case (declaring clusters alongside their VMs) is unaffected.
- **VM disks** are a single VM-level `disk` size; individual `virtual-disks`
  objects are not modelled.
- **`mac_address`** is sent as an interface field; very recent NetBox versions
  that model MACs as separate objects may need revisiting for full idempotency.
- **`vcpus` decimal coercion.** Depending on the NetBox build's DRF
  `COERCE_DECIMAL_TO_STRING` setting, the API may return `vcpus` as a string
  (`"4.00"`); if so the VM is cosmetically re-PATCHed every run (not data
  corruption). Handle in `calculateDiff`/`valuesEqual` (string↔number) if seen.
- **IPv6 primary IP** in `setVMPrimaryIP` mirrors the device path but is not
  unit-tested (the in-process fake does not derive `family` from the address).
- Cables to VM interfaces are out of scope (NetBox does not cable VM interfaces).
