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

Only VMs that set `provision: true` are provisioned here; every other VM is
NetBox-only and is skipped by `tfgen` (it reports how many) — even one that
carries a `vmid`/`vm_template_id` for documentation. A provisioned VM must also
declare a `node`, a `vmid` (the Proxmox VMID to create) and a `vm_template_id`
(the template VMID to clone) — `tfgen` rejects the input otherwise, so the error
surfaces before Terraform runs.

## Field mapping (YAML → Proxmox)

| YAML            | Proxmox (bpg)                         |
|-----------------|---------------------------------------|
| `provision`     | (gate) only `true` VMs are emitted   |
| `vmid`          | `vm_id` (no auto-assign)              |
| `vm_template_id`| `clone.vm_id` (template to clone)     |
| `node`          | `node_name` (which of your nodes)     |
| `cluster`       | `pool_id` (organizational pool)       |
| `platform`      | documentation only (not used to clone) |
| `vcpus`         | `cpu.cores`                           |
| `memory`        | `memory.dedicated`                    |
| `disk`          | `disk.size` on `scsi0` (omit = inherit template) |
| `interfaces[].ip`   | cloud-init static `ip_config`     |
| `interfaces[].vlan` | NIC `vlan_id` via `var.vlan_tags` |
| `interfaces[].primary` | interface that carries `var.default_gateway` |
| `tags`          | `tags` (always includes `gitops`)     |

tfgen always stamps the `gitops` tag (same `ManagedTagSlug` the NetBox
controller uses), so every provisioned VM is identifiable as GitOps-managed in
both NetBox and Proxmox.

## Full lifecycle (day-2 updates)

The module is the desired state, so editing the YAML and re-running the pipeline
reconciles existing VMs — not just first-time creation:

- **More CPU/RAM/disk:** change `vcpus`/`memory`/`disk`; Terraform updates the
  running VM in place. Omitting `disk` (or `0`) inherits the template's disk; a
  larger value grows the volume — Proxmox can't shrink a disk, so lowering it is
  rejected, not a rebuild.
- **Add a second NIC:** append an entry to `interfaces`; a new `network_device`
  (and matching cloud-init `ip_config`) is added to the VM.
- **Re-IP / change VLAN:** edit the interface; cloud-init config updates.

No `lifecycle`/`ignore_changes` guards are set, so these are genuine in-place
updates. Some (NIC add, cloud-init change) take effect on the next VM reboot, per
Proxmox/cloud-init behaviour.

## Environment-specific setup

1. **Templates.** Each provisioned VM names the template to clone directly via
   `vm_template_id` (the template's Proxmox VMID, e.g. `800`). No tag-based
   discovery is involved — the id from the YAML is passed straight to
   `clone.vm_id`. `platform` is kept only for NetBox documentation.
2. **VLANs.** NetBox names VLANs; Proxmox NICs need a numeric 802.1q tag. Map
   them with `var.vlan_tags`, e.g. `{ "Management" = 100 }`. Unmapped names are
   left untagged.

### Credentials (set as masked GitLab CI variables)

| CI variable                        | Purpose                                        |
|------------------------------------|------------------------------------------------|
| `TF_VAR_proxmox_endpoint`          | API URL, e.g. `https://pve.example.com:8006/`  |
| `TF_VAR_proxmox_api_token_id`      | token id, e.g. `gitops@pve!terraform`          |
| `TF_VAR_proxmox_api_token_secret`  | token secret (the UUID); mark **masked**       |
| `TF_VAR_ci_ssh_keys`               | SSH public key(s) injected via cloud-init      |

The API token is supplied as two variables so the id and secret can be rotated
independently; `providers.tf` joins them into the `id=secret` string the bpg
provider expects (`token_id` is `user@realm!tokenid`, the secret is the UUID).
For a self-signed Proxmox cert set `TF_VAR_proxmox_insecure=true`. Optional:
`TF_VAR_default_gateway`, `TF_VAR_vlan_tags`, `TF_VAR_dns_servers`,
`TF_VAR_ci_username`, `TF_VAR_cpu_type`, `TF_VAR_vm_on_boot`.

> **No SSH needed.** A full clone + cloud-init on the same node runs entirely
> over the API, so the provider has no `ssh{}` block and the CI runner needs no
> SSH agent. (SSH would only be required for snippet/file uploads or cross-node
> disk import, which this module doesn't perform.)

> **Provider version.** Pinned to `~> 0.109.0`. The resource schema used here is
> unchanged from the 0.83.x line validated against the live cluster. bpg is
> pre-1.0; review its changelog before bumping.

> **CPU type.** Every VM is cloned with `cpu.type = var.cpu_type` (default
> `x86-64-v2-AES`) — a cluster-portable type so VMs can later live-migrate
> between nodes. Don't use `host` if your nodes aren't identical.

## Status

Scaffold — written to be correct against bpg/proxmox ~0.109 but **not yet run
against a live Proxmox**. Validate with `terraform validate` and a `plan`
against your environment before applying.
