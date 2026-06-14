# Proxmox provisioning (Terraform)

This module provisions the VMs declared in `inventory/virtual/*.yaml` onto
Proxmox VE, using the [`bpg/proxmox`](https://registry.terraform.io/providers/bpg/proxmox)
provider. It is the **second consumer** of the single YAML source of truth — the
first being the NetBox controller. The two run independently (see
`docs/PLAN_YAML_VM_PIPELINE.md`); nothing flows back from Proxmox into NetBox.

## How the data gets here

```
inventory/virtual/*.yaml
   └─ cmd/tfgen ──▶ terraform/generated.tfvars.json   (var "vms")
                       └─ terraform plan/apply ──▶ Proxmox
```

Never edit `generated.tfvars.json` by hand — change the YAML and regenerate:

```sh
go run ./cmd/tfgen --data-dir . --out terraform/generated.tfvars.json
```

## Field mapping (YAML → Proxmox)

| YAML            | Proxmox (bpg)                         |
|-----------------|---------------------------------------|
| `vmid`          | `vm_id` (no auto-assign)              |
| `node`          | `node_name` (which of your nodes)     |
| `cluster`       | `pool_id` (organizational pool)       |
| `platform`      | clone source template (by name → id)  |
| `vcpus`         | `cpu.cores`                           |
| `memory`        | `memory.dedicated`                    |
| `disk`          | `disk.size` on `scsi0`                |
| `interfaces[].ip`   | cloud-init static `ip_config`     |
| `interfaces[].vlan` | NIC `vlan_id` via `var.vlan_tags` |
| `tags`          | `tags` (always includes `gitops`)     |

tfgen always stamps the `gitops` tag (same `ManagedTagSlug` the NetBox
controller uses), so every provisioned VM is identifiable as GitOps-managed in
both NetBox and Proxmox.

## Full lifecycle (day-2 updates)

The module is the desired state, so editing the YAML and re-running the pipeline
reconciles existing VMs — not just first-time creation:

- **More CPU/RAM/disk:** change `vcpus`/`memory`/`disk`; Terraform updates the
  running VM in place (a larger `disk` grows the volume — Proxmox can't shrink a
  disk, so reducing the number is a no-op/error, not a rebuild).
- **Add a second NIC:** append an entry to `interfaces`; a new `network_device`
  (and matching cloud-init `ip_config`) is added to the VM.
- **Re-IP / change VLAN:** edit the interface; cloud-init config updates.

No `lifecycle`/`ignore_changes` guards are set, so these are genuine in-place
updates. Some (NIC add, cloud-init change) take effect on the next VM reboot, per
Proxmox/cloud-init behaviour.

## Environment-specific setup

Two mappings can't come from NetBox semantics and must be configured here:

1. **Templates.** tfgen passes the `platform` slug as the template *name*; the
   provider clones by numeric id. Templates are discovered from Proxmox by tag
   (`var.template_tags`, default `["template"]`) and matched by name. Tag your
   templates accordingly, and ensure each `platform` slug matches a template
   name exactly.
2. **VLANs.** NetBox names VLANs; Proxmox NICs need a numeric 802.1q tag. Map
   them with `var.vlan_tags`, e.g. `{ "Management" = 100 }`. Unmapped names are
   left untagged.

Connection + credentials come from CI variables (see `.gitlab-ci.yml`):
`TF_VAR_proxmox_endpoint`, `TF_VAR_proxmox_api_token`, and optionally
`TF_VAR_default_gateway`, `TF_VAR_vlan_tags`, `TF_VAR_ci_ssh_keys`.

## Status

Scaffold — written to be correct against bpg/proxmox ~0.109 but **not yet run
against a live Proxmox**. Validate with `terraform validate` and a `plan`
against your environment before applying.
