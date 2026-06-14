# Implementation Plan: YAML → NetBox + Proxmox VM Pipeline

**Goal:** Use the existing VM YAML (`inventory/virtual/vms.yaml`) as the single
source of truth to **both** (a) describe VMs in NetBox and (b) provision those
VMs in Proxmox via Terraform — all driven by one GitLab pipeline, one runner,
one repository.

**Status:** 📐 Design. No code yet. Records the architecture and the decisions
made on 2026-06-13/14.

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

> **Requires one new model field.** `VMConfig` has no `vmid` today (see
> `pkg/models/virtualization.go`). Add `VMID int yaml:"vmid"`, validate it, map
> it to Terraform `vm_id`, and store it in NetBox as a **custom field** (NetBox's
> VM model has no native VMID slot — define a `vmid` custom field, e.g. via a
> new `definitions/extras/custom_fields.yaml`). The VMID custom field is the only
> NetBox-side addition needed.

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

### 5a. `cmd/tfgen` — YAML → Terraform vars
- Reuse `pkg/loader.LoadVMs` + `LoadClusters` so tfgen and the NetBox reconciler
  parse the **same** structs — no second YAML schema.
- Emit `terraform/generated.tfvars.json`: a map keyed by VM name with `vmid`,
  sizing, template (from `platform`), node/cluster, NICs (name + bridge + VLAN),
  and the **static IP/gateway** for cloud-init `ip_config`.
- Pure, deterministic, unit-testable; no network calls.

### 5b. `terraform/` — Proxmox provisioning
- `for_each` over the generated VM map → `proxmox_virtual_environment_vm`.
- `vm_id` from YAML `vmid`; `initialization.ip_config` carries the static IP.
- No sync-back component exists — Proxmox is a leaf consumer.

### 5c. NetBox side — VMID custom field
- Add `VMID` to `VMConfig` and map it onto a NetBox `vmid` custom field in the
  VM payload (`pkg/reconciler/virtualization.go`).
- Manage the custom-field definition declaratively (new
  `definitions/extras/custom_fields.yaml` + a small reconciler), or create it
  once in NetBox by hand if you prefer to keep scope tight for v1.

---

## 6. Open questions to resolve before implementation

1. **Template strategy:** how do `platform` slugs map to Proxmox templates — by
   convention (template name == slug), or an explicit map in a definitions file?
2. **Node placement:** does `cluster` pick a Proxmox *cluster* (provider targets
   the cluster, Proxmox schedules the node) or do we need a node hint per VM?
3. **VMID custom field:** manage it declaratively (build a small custom-field
   reconciler) or create it once by hand in NetBox? Affects scope of step 0.
4. **Secrets:** Proxmox API token + NetBox token as masked GitLab CI variables
   (`PROXMOX_*`, existing `NETBOX_*`). Confirm the runner can reach Proxmox.
5. **Disk model:** YAML `disk` is a single size today; multi-disk VMs would need
   a richer schema (out of scope for v1, matches the NetBox plan's stance).

---

## 7. Suggested build order (independently shippable)

0. **Model:** add `VMID` to `VMConfig` (+ validation, loader), map it to the
   NetBox `vmid` custom field, and define that custom field (declaratively or by
   hand — see open question 3).
1. `cmd/tfgen` + unit tests (YAML → tfvars.json), no Proxmox needed.
2. `terraform/` skeleton (provider, one `for_each` VM) + `terraform validate`
   in the `validate` stage.
3. Wire `terraform_plan` (MR) and `terraform_apply` (main, manual) into
   `.gitlab-ci.yml` with the GitLab http state backend.
4. Docs: update `README.md` and `EXAMPLES.md` with the end-to-end flow.

No sync-back / reconcile phase is built — the MAC decision (§2) removed it.
