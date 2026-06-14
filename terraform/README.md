# Proxmox provisioning (Terraform)

This module provisions the VMs declared in `inventory/virtual/<env>/*.yaml` onto
Proxmox VE, using the [`bpg/proxmox`](https://registry.terraform.io/providers/bpg/proxmox)
provider. It is the **second consumer** of the single YAML source of truth — the
first being the NetBox controller. The two run independently (see
`docs/PLAN_YAML_VM_PIPELINE.md`); nothing flows back from Proxmox into NetBox.

## How the data gets here

```
inventory/virtual/<env>/*.yaml          (one file per VM; env = prod|stage|playground)
   └─ cmd/tfgen --group <env> ──▶ terraform/generated.<env>.tfvars.json   (var "vms")
                                     └─ terraform plan/apply ──▶ Proxmox   (state proxmox-<env>)
```

Each **environment** is a subfolder of `inventory/virtual/` and is provisioned
into its **own GitLab-managed Terraform state** (`proxmox-<env>`), so a change in
`playground` can never touch `prod`. Put **one YAML file per VM** inside the env
folder. The NetBox controller scans `inventory/virtual/` recursively, so every VM
in every environment is still documented in NetBox from the same files.

Never edit a `generated.<env>.tfvars.json` by hand — change the YAML and
regenerate (per environment):

```sh
go run ./cmd/tfgen --data-dir . --group prod --out terraform/generated.prod.tfvars.json
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

### Configuration via GitLab CI/CD variables (no tfvars file needed)

You do **not** maintain a `terraform.tfvars` file for CI. Terraform automatically
reads any pipeline variable named `TF_VAR_<name>` as the input variable `<name>`,
and the jobs only pass `-var-file=generated.tfvars.json` (the `vms` list — no
secrets). So set everything in **GitLab → Settings → CI/CD → Variables**:

| CI/CD variable             | Required | Example value                                   |
|----------------------------|----------|-------------------------------------------------|
| `ENABLE_PROXMOX`           | ✅       | `true` (turns the tf_* jobs on)                 |
| `TF_VAR_proxmox_endpoint`  | ✅       | `https://pve.example.com:8006/api2/json`        |
| `TF_VAR_proxmox_api_token` | ✅ 🔒   | `user@realm!tokenid=secret` (mark **Masked**)   |
| `TF_VAR_proxmox_insecure`  | –        | `true` (for a self-signed cert)                 |
| `TF_VAR_ci_username`       | –        | `cloud-user`                                 |
| `TF_VAR_ci_ssh_keys`       | –        | `["ssh-ed25519 AAAA... user@host"]`             |
| `TF_VAR_default_gateway`   | –        | `192.168.1.1`                                   |
| `TF_VAR_dns_servers`       | –        | `["192.168.1.53"]`                               |
| `TF_VAR_vlan_tags`         | –        | `{"Management":100}`                            |
| `TF_VAR_network_bridge`    | –        | `vmbr0`                                      |
| `TF_VAR_datastore_id`      | –        | `local-lvm`                                     |
| `TF_VAR_cpu_type`          | –        | `x86-64-v2-AES`                                 |
| `TF_VAR_vm_on_boot`        | –        | `true`                                          |

> **⚠️ Typing gotcha.** `string`/`bool`/`number` variables take a plain value,
> but **list and map** variables (`ci_ssh_keys`, `dns_servers`, `vlan_tags`) must
> be set as **JSON**: `["a","b"]` / `{"Management":100}`. A bare string there will
> fail the plan. Set the variable **Type** to *Variable* (not *File*).

> The `terraform.tfvars.example` in this folder is only a reference for the same
> values if you ever run the module locally; it is never used by CI, and a real
> `terraform.tfvars` is gitignored so secrets can't be committed.

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

## Environments & state isolation

The pipeline provisions one **environment per folder**, each into its own
GitLab-managed state:

| Folder                          | Terraform state    | CI `environment` |
|---------------------------------|--------------------|------------------|
| `inventory/virtual/prod/`       | `proxmox-prod`     | `proxmox-prod`   |
| `inventory/virtual/stage/`      | `proxmox-stage`    | `proxmox-stage`  |
| `inventory/virtual/playground/` | `proxmox-playground` | `proxmox-playground` |

`tf_plan`/`tf_apply` run as a GitLab `parallel:matrix` over these envs (see
`.gitlab-ci.yml`); `tf_apply` is a manual gate per env. The same Proxmox
credentials (`TF_VAR_*`) are shared across all environments.

**Why per-env, not per-VM state:** environments are a small fixed set, so the
blast radius that matters (prod vs. the rest) is isolated with a 3-line matrix.
A single VM can still be changed in isolation within its env state with
`terraform apply -target='proxmox_virtual_environment_vm.this["web-01"]'`.

**Add an environment:** create `inventory/virtual/<env>/`, then add `<env>` to
the single `.proxmox_envs` anchor in `.gitlab-ci.yml` — it drives the
`tf_generate`/`tf_plan`/`tf_apply` matrices, so there is no second list to edit.

## Status

Scaffold — written to be correct against bpg/proxmox ~0.109 but **not yet run
against a live Proxmox**. Validate with `terraform validate` and a `plan`
against your environment before applying.
