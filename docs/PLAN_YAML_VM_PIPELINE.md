# Implementation Plan: YAML → NetBox + Proxmox VM Pipeline

**Goal:** Use the existing VM YAML (`inventory/virtual/vms.yaml`) as the single
source of truth to **both** (a) describe VMs in NetBox and (b) provision those
VMs in Proxmox via Terraform — all driven by one GitLab pipeline, one runner,
one repository.

**Status:** 📐 Design. No code yet. This document records the architecture and
the decisions made on 2026-06-13.

---

## 1. Repository decision: stay a monorepo

**Decision: do _not_ start a second repository.** Keep NetBox sync and Proxmox
provisioning in this repo.

The whole point of this project is *one YAML file = one source of truth*.
Splitting the data across two repos would create two copies of the truth that
drift, and force two pipelines to share state and ordering. Instead, NetBox and
Proxmox become **two consumers of the same YAML**:

```
netbox-gitops-controller/        (this repo)
├── inventory/virtual/vms.yaml            ← single source of truth
├── definitions/virtualization/…          ← clusters, cluster types (exists)
├── cmd/netbox-gitops/                     ← consumer 1: YAML → NetBox (exists)
├── cmd/tfgen/                             ← NEW: YAML → terraform vars
├── cmd/runtime-sync/  (or a flag on netbox-gitops)
│                                          ← NEW: Proxmox facts → NetBox
└── terraform/                             ← NEW: consumer 2: vars → Proxmox
    ├── main.tf       (bpg/proxmox provider + backend)
    ├── variables.tf
    ├── vms.tf        (for_each over generated VM map)
    └── outputs.tf    (per-VM: vmid, mac, ip)
```

The YAML already carries nearly everything Terraform needs:

| YAML field            | NetBox use            | Proxmox / Terraform use                 |
|-----------------------|-----------------------|-----------------------------------------|
| `name`                | VM name               | VM name                                 |
| `cluster`             | NetBox cluster        | Target Proxmox cluster / node selection |
| `platform`            | Platform (slug)       | Clone template (e.g. `ubuntu-22-04`)    |
| `vcpus`               | VM vcpus              | `cpu.cores`                             |
| `memory` (MB)         | VM memory             | `memory.dedicated`                      |
| `disk` (GB)           | VM disk               | `disk.size`                             |
| `interfaces[].untagged_vlan` | VM iface VLAN  | bridge + `vlan_id` on the NIC           |
| `interfaces[].ip.address` | VM iface IP + `primary_ip4` | cloud-init `ip_config` (static)  |
| `interfaces[].ip.dns_name` | DNS name on the IP    | cloud-init / DNS (optional)             |
| `vmid` (NEW field)    | VMID custom field     | `vm_id` (deterministic, no auto-assign) |

---

## 2. Source-of-truth split (IP + VMID in YAML; only MAC observed)

We deliberately **split ownership** of fields between Git and the running
infrastructure. With **static IPs via cloud-init** and a **VMID declared in
YAML**, nearly everything is Git-owned. The single value Proxmox generates is
the NIC **MAC address**.

- **YAML owns (desired state):** which VMs exist, sizing (`vcpus`/`memory`/
  `disk`), template/`platform`, cluster→node placement, which bridge/VLAN each
  NIC attaches to, the static IP (`interfaces[].ip.address`), **and the VMID**.
  Terraform realises all of this in Proxmox (IP via cloud-init `ip_config`,
  VMID via `vm_id` so Proxmox does not auto-assign).
- **Proxmox generates (observed state):** only the NIC **MAC address**. It is
  read from `terraform output` and written into NetBox by the sync-back step.

Consequences:
- **IP, `primary_ip4`, and VMID** in NetBox come straight from YAML via the
  normal `netbox apply` path — no discovery, no guest agent, no DHCP timing.
- Sync-back patches exactly one thing: the interface **`mac_address`**. The
  author leaves MAC blank in YAML.
- The MAC is known from `terraform apply` output immediately — no boot/poll wait.

> **Requires a new model field.** `VMConfig` has no `vmid` today (see
> `pkg/models/virtualization.go`). Add `VMID int yaml:"vmid"`, validate it, map
> it to Terraform `vm_id`, and store it in NetBox as a **custom field** (NetBox's
> VM model has no native VMID — create a `vmid` custom field, e.g. via a
> `definitions/extras/custom_fields.yaml`, since the VM API has no built-in slot).

> **Optional simplification — eliminate sync-back entirely.** MAC is now the
> *only* reason the reconcile phase exists. If you also **pin the MAC in YAML**
> and have Terraform force it onto the NIC (`network_device.mac_address`), then
> NetBox gets the MAC from `netbox apply` like every other field, sync-back goes
> away, and the two apply jobs become **fully independent parallel** with no
> third phase. Trade-off: you must allocate MACs yourself (e.g. from a
> locally-administered `02:…` range). Recommended if you want the simplest,
> fully-declarative pipeline. Decide this before building §5c.

---

## 3. Provider: `bpg/proxmox`

Target the [`bpg/proxmox`](https://registry.terraform.io/providers/bpg/proxmox)
provider (actively maintained; good clone + cloud-init + guest-agent support).

Key resource: `proxmox_virtual_environment_vm`.
- `vm_id = each.value.vmid` — VMID from YAML; Proxmox does not auto-assign.
- Clone from a template per `platform`.
- `network_device { bridge = …, vlan_id = … }` — MAC generated by Proxmox (or
  pinned, see §2 simplification), exposed as `network_device[*].mac_address`.
- `initialization { ip_config { ipv4 { address = "<YAML ip>", gateway = … } } }`
  — static IP from YAML, injected via cloud-init.
- Outputs expose, per VM: `mac_addresses` (only — VMID/IP are authored in YAML,
  so the guest agent is optional).

---

## 4. Pipeline shape (decision: parallel create, then reconcile)

Creation in NetBox and Proxmox runs **in parallel**; a third **reconcile** phase
then pulls Proxmox-generated facts back into NetBox. Extends the existing
`.gitlab-ci.yml` (which already gates plan on MRs and apply on `main`, manual).

```
stages: [test, build, validate, plan, apply, reconcile]

validate ── yamlcheck (exists) + tfgen + terraform validate

plan      (MR preview, parallel)
  ├─ netbox_plan      ./netbox-gitops --dry-run                 (exists)
  └─ terraform_plan   tfgen → terraform plan

apply     (main, manual, parallel)
  ├─ netbox_apply     ./netbox-gitops                            (exists)
  │                   creates VM + interface + IP/primary_ip4 + VMID
  │                   custom field from YAML; MAC left blank
  └─ terraform_apply  tfgen → terraform apply → terraform output -json

reconcile (main, after BOTH applies)   ← only needed if MAC is NOT pinned in YAML
  └─ runtime_sync     read terraform output → PATCH NetBox: interface.mac_address
```

Why `netbox_apply` must still run before `reconcile`: sync-back *patches* the VM
interface that `netbox_apply` creates from YAML. So `reconcile` needs both
`netbox_apply` (interface exists) and `terraform_apply` (MAC exists) — but the
two applies don't depend on each other. Everything except MAC is already set by
`netbox_apply` from YAML; reconcile only adds the MAC.

**If you pin MACs in YAML (§2 simplification), drop the `reconcile` stage
entirely** — the two applies are then fully independent and parallel.

**State backend:** use the GitLab-managed Terraform state backend
(`http` backend against `${CI_API_V4_URL}/projects/${CI_PROJECT_ID}/terraform/
state/<name>`). No state in git.

---

## 5. New components to build

### 5a. `cmd/tfgen` — YAML → Terraform vars
- Reuse `pkg/loader.LoadVMs` + `LoadClusters` (already exist) so tfgen and the
  NetBox reconciler parse the **same** structs — no second YAML schema.
- Reuse `pkg/loader.LoadVMs` + `LoadClusters` so tfgen and the NetBox reconciler
  parse the **same** structs — no second YAML schema.
- Emit `terraform/generated.tfvars.json`: a map keyed by VM name with `vmid`,
  sizing, template (from `platform`), node/cluster, NICs (bridge + VLAN), and the
  **static IP/gateway** for cloud-init `ip_config`.
- Pure, deterministic, unit-testable; no network calls.

### 5b. `terraform/` — Proxmox provisioning
- `for_each` over the generated VM map → `proxmox_virtual_environment_vm`.
- `vm_id` from YAML `vmid`; `initialization.ip_config` carries the static IP.
- `outputs.tf` exposes `{ vm_name = { mac_addresses } }`.

### 5c. Runtime sync-back — Proxmox MAC → NetBox  (skip if MAC pinned in YAML)
- Only needed if Proxmox generates the MAC. If MACs are pinned in YAML (§2),
  **this component is not built at all** — `netbox apply` carries the MAC.
- Otherwise: new subcommand (e.g. `netbox-gitops --import-runtime
  terraform-output.json`) **or** a small `cmd/runtime-sync`. Keep all NetBox
  writes in the Go tool — do **not** add a second NetBox writer (e.g. a TF NetBox
  provider) alongside it.
- For each VM in the TF output: look up the VM + interface in NetBox and PATCH
  the interface **`mac_address`** only. IP, `primary_ip4`, and VMID are already
  handled by `netbox apply` from YAML, so sync-back touches nothing else. Reuse
  the existing `client.Apply` idempotency and managed-tag injection from
  `pkg/reconciler/virtualization.go`.
- Idempotent: a second reconcile with unchanged MACs is a zero-change no-op.

---

## 6. Open questions to resolve before implementation

1. **Template strategy:** how do `platform` slugs map to Proxmox templates — by
   convention (template name == slug), or an explicit map in a definitions file?
2. **Node placement:** does `cluster` pick a Proxmox *cluster* (provider targets
   the cluster and Proxmox schedules the node) or do we need a node hint per VM?
3. **Pin MAC in YAML, or sync it back?** (the §2 decision) — pinning eliminates
   the `reconcile` stage and `cmd/runtime-sync` entirely; generating means
   building both. Resolve this first; it removes/keeps build steps 4–5.
   Related: NetBox needs a `vmid` (and possibly `mac`) **custom field** defining.
4. **Secrets:** Proxmox API token + NetBox token as masked GitLab CI variables
   (`PROXMOX_*`, existing `NETBOX_*`). Confirm the runner can reach Proxmox.
5. **Disk model:** YAML `disk` is a single size today; multi-disk VMs would need
   a richer schema (out of scope for v1, matches the NetBox plan's stance).

---

## 7. Suggested build order (independently shippable)

0. **Model + custom fields:** add `VMID` to `VMConfig` (+ validation, loader,
   reconciler mapping to a NetBox `vmid` custom field). If pinning MACs, add a
   `MACAddress` to the VM interface path too (the struct already has the field).
1. `cmd/tfgen` + unit tests (YAML → tfvars.json), no Proxmox needed.
2. `terraform/` skeleton (provider, one `for_each` VM, outputs) + `terraform
   validate` in the `validate` stage.
3. Wire `terraform_plan` (MR) and `terraform_apply` (main, manual) into
   `.gitlab-ci.yml` with the GitLab http state backend.
4. **Only if MAC is generated, not pinned:** runtime sync-back command + tests
   (mock TF output → NetBox MAC PATCH), then wire the `reconcile` stage and
   confirm a second run is a zero-change no-op. If MACs are pinned in YAML, skip
   this step entirely.
5. Docs: update `README.md` and `EXAMPLES.md` with the end-to-end flow.
