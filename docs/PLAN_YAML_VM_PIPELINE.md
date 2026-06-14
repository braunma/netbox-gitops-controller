# YAML → NetBox + Proxmox VM Pipeline — Design Record

**Goal:** use one VM YAML (`inventory/virtual/*.yaml`) as the single source of
truth to **both** describe VMs in NetBox **and** provision them in Proxmox via
Terraform — one repo, one pipeline.

**Status:** ✅ Implemented (scaffold). Done: `vmid`/`node` model fields, the
declarative `vmid` custom field (foundation phase), VM-payload wiring,
`cmd/tfgen` (YAML → `tfvars.json`, unit-tested), the `terraform/` Proxmox module
(`bpg/proxmox`), and opt-in `.gitlab-ci.yml` jobs (`tf_generate`/`tf_validate`/
`tf_plan`/`tf_apply`). The Terraform module is authored but **not yet run against
a live Proxmox** — validate with a real `plan` before applying. For the field
mapping and operational detail see [`terraform/README.md`](../terraform/README.md).

## Key decisions

1. **Stay a monorepo.** NetBox sync and Proxmox provisioning live in this repo as
   **two independent consumers of the same YAML**, not two repositories. Splitting
   the data would create two copies of the truth that drift. The consumers run in
   parallel with no dependency on each other; both are idempotent, so re-running
   either is safe.

2. **One-directional flow — MAC is not modelled.** There's no MAC-based DHCP,
   802.1X/NAC, or switch-port MAC correlation here, so Proxmox generates MACs
   freely and **nothing flows back into NetBox**. Data flows purely YAML → NetBox
   and YAML → Proxmox, with no reconcile/sync-back phase. What matters in NetBox —
   interface name + IP + VLAN — is already authored in YAML.

3. **Explicit `node:` per VM.** A Proxmox pool is organizational, not a scheduling
   target, so `cluster` does not choose a node. Each provisioned VM names its
   target node via `node:` (required by `tfgen`; the `cluster` maps to the Proxmox
   `pool_id`).

4. **`vmid` as a declarative custom field.** The VM model has no native VMID slot,
   so `vmid` is stored in a NetBox **custom field** declared in YAML under
   `definitions/custom_fields/` and reconciled in the foundation phase (before
   VMs). It is *not* under `definitions/extras/` — the loader scans recursively and
   `LoadTags` would otherwise mis-parse it as a tag. Custom-field objects are not
   taggable, so `Apply` skips managed-tag injection for the `custom-fields`
   endpoint and they are excluded from pruning.

## Components

- **`cmd/tfgen` / `pkg/tfgen`** — pure, deterministic, unit-tested. Reuses
  `pkg/loader.LoadVMs` so tfgen and the NetBox reconciler parse the same structs
  (no second YAML schema). Emits a single `vms` Terraform variable keyed by VM
  name. Skips VMs without a `vmid` (NetBox-only); errors when a `vmid` VM lacks a
  `node` or `platform`, or on duplicate VM names.
  Run: `go run ./cmd/tfgen --data-dir <dir> --out terraform/generated.tfvars.json`.
- **`terraform/`** — `for_each` over the generated VM map →
  `proxmox_virtual_environment_vm` (`bpg/proxmox`); `vm_id` from `vmid`,
  cloud-init static IP from the interface IP. No sync-back component.
- **NetBox side** — `VMID` on `VMConfig` sets `custom_fields.vmid`;
  `CustomField` model + `FoundationReconciler.ReconcileCustomFields`.

## Resolved questions

- **Template strategy:** Proxmox template name == `platform` slug; templates are
  discovered by tag (`var.template_tags`) and matched by name at plan time.
- **Secrets:** Proxmox + NetBox tokens as masked GitLab CI variables; Proxmox
  jobs gated behind `ENABLE_PROXMOX`.
- **Disk model:** YAML `disk` is a single size (omit to inherit the template's
  disk); multi-disk VMs would need a richer schema (out of scope for v1).
