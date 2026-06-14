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

Scaffold — written to be correct against bpg/proxmox ~0.66 but **not yet run
against a live Proxmox**. Validate with `terraform validate` and a `plan`
against your environment before applying.
