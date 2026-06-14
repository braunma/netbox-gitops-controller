# Implementation Plan: YAML → NetBox + Proxmox VM Pipeline

**Goal:** Use the existing VM YAML (`inventory/virtual/vms.yaml`) as the single
source of truth to **both** (a) describe VMs in NetBox and (b) provision those
VMs in Proxmox via Terraform — all driven by one GitLab pipeline, one runner,
one repository.

**Status:** ✅ Implemented (scaffold). Architecture + decisions recorded
2026-06-13/14. Done: VMID + `node` model fields, declarative `vmid` custom field
(foundation phase), VM-payload wiring, `cmd/tfgen` (YAML → tfvars.json) with
tests, the `terraform/` Proxmox module (`bpg/proxmox`), and opt-in
`.gitlab-ci.yml` jobs (`tf_generate`/`tf_validate`/`tf_plan`/`tf_apply`). The
Terraform module is authored but **not yet run against a live Proxmox** —
validate with a real `plan` before applying.

**Node placement decision (2026-06-14):** explicit `node:` per VM. A Proxmox
pool is organizational, not a scheduling target, so `cluster → pool_id` does not
choose a node; each VM names its target node via `node:` (required by tfgen).

---

## 1. Repository decision: stay a monorepo

**Decision: do _not_ start a second repository.** Keep NetBox sync and Proxmox
provisioning in this repo.

The whole point of this project is *one YAML file = one source of truth*.
Splitting the data across two repos would create two copies of the truth that
drift, and force two pipelines to share state and ordering. Instead, NetBox and
Proxmox become **two independent consumers of the same YAML**:

```
netbox-gitops-controller/        (this repo)
├── inventory/virtual/vms.yaml            ← single source of truth
├── definitions/virtualization/…          ← clusters, cluster types (exists)
├── cmd/netbox-gitops/                     ← consumer 1: YAML → NetBox (exists)
├── cmd/tfgen/                             ← NEW: YAML → terraform vars
└── terraform/                             ← NEW: consumer 2: vars → Proxmox
    ├── main.tf       (bpg/proxmox provider + backend)
    ├── variables.tf
    └── vms.tf        (for_each over generated VM map)
```

---

## 2. Source of truth: everything is authored in YAML

**Decision (2026-06-14): the MAC address is not modelled.** It is not useful
for this setup — there's no MAC-based DHCP, no 802.1X/NAC, and no switch-port
MAC correlation. What matters in NetBox is the **interface name + IP + VLAN**,
all of which are already authored in YAML. So Proxmox can generate MACs freely
and **nothing needs to flow back from Proxmox into NetBox.**

That makes the data flow purely one-directional: YAML → (NetBox) and
YAML → (Proxmox), with no reconcile/sync-back phase at all.

The YAML carries everything both consumers need:

| YAML field            | → NetBox                       | → Proxmox / Terraform                   |
|-----------------------|--------------------------------|-----------------------------------------|
| `name`                | VM name                        | VM name                                 |
| `vmid` (NEW field)    | `vmid` custom field            | `vm_id` (deterministic, no auto-assign) |
| `cluster`             | NetBox cluster                 | target Proxmox cluster / node           |
| `platform`            | Platform (slug)                | clone template (e.g. `ubuntu-22-04`)    |
| `vcpus`               | VM vcpus                       | `cpu.cores`                             |
| `memory` (MB)         | VM memory                      | `memory.dedicated`                      |
| `disk` (GB)           | VM disk                        | `disk.size`                             |
| `interfaces[].name`   | VM interface name (`eth0`)     | NIC ordering / cloud-init iface         |
| `interfaces[].untagged_vlan` | VM iface VLAN           | bridge + `vlan_id` on the NIC           |
| `interfaces[].ip.address` | VM iface IP + `primary_ip4` | cloud-init `ip_config` (static)     |
| `interfaces[].ip.dns_name` | DNS name on the IP        | cloud-init / DNS (optional)             |

MAC is intentionally absent from both columns: Proxmox auto-generates it and
NetBox doesn't track it.

> **New model field + declarative custom field (implemented).** `VMConfig` now
> has `VMID int yaml:"vmid"`. It maps to Terraform `vm_id` (via `cmd/tfgen`) and
> is stored in NetBox as a **custom field** (the VM model has no native VMID
> slot). The `vmid` custom field is itself declared in YAML and reconciled in
> the **foundation phase** from `definitions/custom_fields/` — note: *not* under
> `definitions/extras/`, because the loader scans folders recursively and
> `LoadTags("definitions/extras")` would otherwise mis-parse the custom field as
> a tag. Custom-field objects are not taggable, so `Apply` skips managed-tag
> injection for the `custom-fields` endpoint (`constants.UntaggableEndpoints`)
> and they are intentionally excluded from pruning.

---

## 3. Provider: `bpg/proxmox`

Target the [`bpg/proxmox`](https://registry.terraform.io/providers/bpg/proxmox)
provider (actively maintained; good clone + cloud-init support).

Key resource: `proxmox_virtual_environment_vm`.
- `vm_id = each.value.vmid` — VMID from YAML; Proxmox does not auto-assign.
- Clone from a template per `platform`.
- `network_device { bridge = …, vlan_id = … }` — MAC left to Proxmox (not read).
- `initialization { ip_config { ipv4 { address = "<YAML ip>", gateway = … } } }`
  — static IP from YAML, injected via cloud-init.
- No outputs need to be consumed downstream (nothing flows back to NetBox).

---

## 4. Pipeline shape: two parallel consumers, no reconcile

Both consumers read the same YAML and run **independently in parallel**. There
is no third phase. Extends the existing `.gitlab-ci.yml` (which already gates
plan on MRs and apply on `main`, manual).

```
stages: [test, build, validate, plan, apply]

validate ── yamlcheck (exists) + tfgen + terraform validate

plan      (MR preview, parallel)
  ├─ netbox_plan      ./netbox-gitops --dry-run                 (exists)
  └─ terraform_plan   tfgen → terraform plan

apply     (main, manual, parallel)
  ├─ netbox_apply     ./netbox-gitops                            (exists)
  │                   VM + interface + IP/primary_ip4 + VMID custom field
  └─ terraform_apply  tfgen → terraform apply
```

`netbox_apply` and `terraform_apply` have no dependency on each other — they are
two views of the same declared state, applied to two systems. If one fails, the
other is unaffected and re-running is safe (both are idempotent).

**State backend:** use the GitLab-managed Terraform state backend
(`http` backend against `${CI_API_V4_URL}/projects/${CI_PROJECT_ID}/terraform/
state/<name>`). No state in git.

---

## 5. New components to build

### 5a. `cmd/tfgen` — YAML → Terraform vars (implemented)
- Logic lives in `pkg/tfgen` (pure, deterministic, unit-tested); `cmd/tfgen` is
  a thin CLI. Reuses `pkg/loader.LoadVMs` so tfgen and the NetBox reconciler
  parse the **same** structs — no second YAML schema.
- Emits a single Terraform variable `vms` (tfvars.json) keyed by VM name with
  `vmid`, sizing, template (from `platform`), cluster/site, and NICs (name +
  `vlan` + static `ip` + `dns_name` + `primary`). MAC is intentionally absent.
- Errors if any VM lacks a positive `vmid`, or on duplicate VM names.
- Run: `go run ./cmd/tfgen --data-dir <dir> --out terraform/generated.tfvars.json`
  (`--out -` writes to stdout).

### 5b. `terraform/` — Proxmox provisioning
- `for_each` over the generated VM map → `proxmox_virtual_environment_vm`.
- `vm_id` from YAML `vmid`; `initialization.ip_config` carries the static IP.
- No sync-back component exists — Proxmox is a leaf consumer.

### 5c. NetBox side — VMID custom field (implemented, declarative)
- `VMID` added to `VMConfig`; the VM payload sets
  `custom_fields: {vmid: <n>}` (`pkg/reconciler/virtualization.go`).
- `CustomField` model + `FoundationReconciler.ReconcileCustomFields`, loaded
  from `definitions/custom_fields/` and reconciled in the foundation phase
  before VMs. The field definition ships in
  `example/definitions/custom_fields/custom_fields.yaml`.

---

## 6. Open questions to resolve before implementation

1. **Template strategy:** how do `platform` slugs map to Proxmox templates — by
   convention (template name == slug), or an explicit map in a definitions file?
2. **Node placement:** does `cluster` pick a Proxmox *cluster* (provider targets
   the cluster, Proxmox schedules the node) or do we need a node hint per VM?
3. **VMID custom field:** ✅ resolved — managed declaratively from
   `definitions/custom_fields/`. Caveat: depends on NetBox 4.x `object_types`
   (older releases use `content_types`); verify against the target instance.
4. **Secrets:** Proxmox API token + NetBox token as masked GitLab CI variables
   (`PROXMOX_*`, existing `NETBOX_*`). Confirm the runner can reach Proxmox.
5. **Disk model:** YAML `disk` is a single size today; multi-disk VMs would need
   a richer schema (out of scope for v1, matches the NetBox plan's stance).

---

## 7. Suggested build order (independently shippable)

0. ✅ **Model + custom field:** `VMID` on `VMConfig` (+ validation, loader),
   mapped to the NetBox `vmid` custom field, with the field declared
   declaratively (`CustomField` model + foundation reconciler).
1. ✅ `cmd/tfgen` / `pkg/tfgen` + unit tests (YAML → tfvars.json), no Proxmox.
2. ✅ `terraform/` module (`bpg/proxmox`, `for_each` over `vms`, clone by
   `platform`, `node_name`/`pool_id`, static cloud-init IP) + `tf_validate`.
3. ✅ `tf_generate`/`tf_plan` (MR) and `tf_apply` (main, manual) in
   `.gitlab-ci.yml` with the GitLab http state backend, gated on `ENABLE_PROXMOX`.
4. ✅ Docs: `terraform/README.md` + README feature note (this plan kept current).

No sync-back / reconcile phase is built — the MAC decision (§2) removed it.
