# YAML → NetBox + Proxmox VM Pipeline — Design Record

**Goal:** use one VM YAML (`inventory/virtual/<env>/*.yaml`, one file per VM) as
the single source of truth to **both** describe VMs in NetBox **and** provision
them in Proxmox via Terraform — one repo, one pipeline.

**Status:** ✅ Implemented (scaffold). Done: `provision`/`vmid`/`vm_template_id`/
`node` model fields, the declarative `vmid` and `vm_template_id` custom fields
(foundation phase), VM-payload wiring,
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

4. **`vmid`/`vm_template_id` as declarative custom fields.** The VM model has no
   native slot for either, so both are stored in NetBox **custom fields** declared
   in YAML under `definitions/custom_fields/` and reconciled in the foundation
   phase (before VMs). They are *not* under `definitions/extras/` — the loader
   scans recursively and `LoadTags` would otherwise mis-parse them as tags.
   Custom-field objects are not taggable, so `Apply` skips managed-tag injection
   for the `custom-fields` endpoint and they are excluded from pruning.

5. **`provision:` opts a VM into Proxmox.** Provisioning is decoupled from the
   `vmid`: only VMs with `provision: true` are emitted by tfgen, so a VM can be
   documented in NetBox (with a `vmid`/`vm_template_id` for reference) without
   being created in Proxmox. A provisioned VM clones strictly from
   `vm_template_id` (the template's Proxmox VMID); `platform` is documentation
   only and no longer selects the template.

6. **One state per environment, not per VM.** VMs live in per-env folders
   (`inventory/virtual/{prod,stage,playground}/`, one file per VM). tfgen
   `--group <env>` emits a per-env tfvars; the same Terraform module is applied
   once per env (`parallel:matrix`) into its own GitLab-managed state
   (`proxmox-<env>`). Environments are a small fixed set, so the blast radius
   that matters (prod vs. the rest) is isolated with a tiny matrix; a single VM
   can still be changed in isolation via `-target`. Per-VM state was rejected: it
   needs dynamic child pipelines and produces unbounded states/locks. The NetBox
   controller still scans `inventory/virtual/` recursively, so all environments
   are documented from the same files.

## Components

- **`cmd/tfgen` / `pkg/tfgen`** — pure, deterministic, unit-tested. Reuses
  `pkg/loader.LoadVMs` so tfgen and the NetBox reconciler parse the same structs
  (no second YAML schema). Emits a single `vms` Terraform variable keyed by VM
  name. Skips VMs without `provision: true` (NetBox-only); errors when a
  provisioned VM lacks a `node`, `vmid` or `vm_template_id`, or on duplicate VM
  names. `--group <env>` scopes the load to one environment subfolder.
  Run: `go run ./cmd/tfgen --data-dir <dir> --group prod --out terraform/generated.prod.tfvars.json`.
- **`terraform/`** — `for_each` over the generated VM map →
  `proxmox_virtual_environment_vm` (`bpg/proxmox`); `vm_id` from `vmid`,
  `clone.vm_id` from `vm_template_id`, cloud-init static IP from the interface IP.
  No sync-back component.
- **NetBox side** — `VMID`/`VMTemplateID` on `VMConfig` set
  `custom_fields.vmid`/`custom_fields.vm_template_id`; `CustomField` model +
  `FoundationReconciler.ReconcileCustomFields`.

## Resolved questions

- **Template strategy:** each provisioned VM names the template to clone directly
  via `vm_template_id` (the template's Proxmox VMID); no tag-based discovery.
- **Secrets:** Proxmox API token as a single masked `TF_VAR_proxmox_api_token`
  (`user@realm!tokenid=secret`); NetBox token likewise; Proxmox jobs gated behind
  `ENABLE_PROXMOX`.
- **Disk model:** YAML `disk` is a single size (omit to inherit the template's
  disk); multi-disk VMs would need a richer schema (out of scope for v1).
