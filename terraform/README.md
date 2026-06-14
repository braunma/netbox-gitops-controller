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
   every VLAN name used by an interface with `var.vlan_tags`, e.g.
   `{ "Management" = 100 }`. An interface with an empty `vlan` is left untagged
   (native VLAN); a **named** VLAN that is missing from the map **fails the
   plan** rather than silently landing the NIC on the native VLAN.
3. **Guest agent.** `var.agent_enabled` (default `true`) turns on the QEMU guest
   agent integration. Keep it on **only if the cloned template runs
   qemu-guest-agent** — otherwise every apply waits for the create timeout and
   then fails. Set `TF_VAR_agent_enabled=false` for templates without it.

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
| `TF_VAR_ci_username`       | –        | `denbi-service`                                 |
| `TF_VAR_ci_ssh_keys`       | –        | `["ssh-ed25519 AAAA... user@host"]`             |
| `TF_VAR_default_gateway`   | –        | `10.57.196.1`                                   |
| `TF_VAR_dns_servers`       | –        | `["10.57.196.4"]`                               |
| `TF_VAR_vlan_tags`         | –        | `{"Management":100}`                            |
| `TF_VAR_network_bridge`    | –        | `vmbr1480`                                      |
| `TF_VAR_datastore_id`      | –        | `local-lvm`                                     |
| `TF_VAR_cpu_type`          | –        | `x86-64-v2-AES`                                 |
| `TF_VAR_vm_on_boot`        | –        | `true`                                          |
| `TF_VAR_agent_enabled`     | –        | `false` (template has no qemu-guest-agent)      |

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
`.gitlab-ci.yml`); `tf_apply` is a manual gate per env. `tf_plan` writes a saved
plan file (`tfplan.<env>`) and `tf_apply` applies **that exact file** rather than
re-planning, so an operator approves and applies precisely the reviewed change
set — and Terraform rejects the plan if the state has drifted since, so the job
fails safely instead of applying something unreviewed. The same Proxmox
credentials (`TF_VAR_*`) are shared across all environments.

**Why per-env, not per-VM state:** environments are a small fixed set, so the
blast radius that matters (prod vs. the rest) is isolated with a 3-line matrix.
A single VM can still be changed in isolation within its env state with
`terraform apply -target='proxmox_virtual_environment_vm.this["web-01"]'`.

**Add an environment:** create `inventory/virtual/<env>/`, then add `<env>` to
the single `.proxmox_envs` anchor in `.gitlab-ci.yml` — it drives the
`tf_generate`/`tf_plan`/`tf_apply` matrices, so there is no second list to edit.

## Safety

Because this is a GitOps module, the YAML is the desired state: **removing a VM
from `inventory/virtual/<env>/` (or making a change Proxmox can only satisfy by
recreating the VM) makes Terraform destroy a real, running VM.** That is
irreversible, so the pipeline defends against it in layers:

1. **Saved plan, not blind apply.** `tf_apply` applies the exact `tfplan.<env>`
   produced and reviewed in `tf_plan` — never a fresh re-plan. If the state has
   drifted since the plan, Terraform rejects the stale plan and the job fails
   safely.
2. **Manual gate.** `tf_apply` is `when: manual` on the default branch, one
   button per environment.
3. **Destroy confirmation (fail-closed).** Before applying, `tf_apply` counts
   the resources the plan would **destroy or replace**. If that count is > 0 it
   refuses to apply unless you re-run the job with the CI variable
   `TF_ALLOW_DESTROY=<env>` (e.g. `TF_ALLOW_DESTROY=prod`). A bare `true` does
   **not** match — you must name the exact environment, so confirming a
   `playground` teardown can never authorise a `prod` one. A missing plan
   artifact aborts rather than applying blind.
4. **Serialised applies.** `resource_group: proxmox-<env>` ensures only one
   apply per environment runs at a time across pipelines, on top of the
   backend's state lock (`lock-timeout=120s`).

### Recommended: commit the dependency lock file

`terraform/.terraform.lock.hcl` is **not** committed yet. For supply-chain
integrity and fully reproducible runs you should generate and commit it so every
`init` verifies the provider against pinned checksums for all CI/runner
platforms:

```sh
cd terraform
terraform init
terraform providers lock -platform=linux_amd64 -platform=darwin_arm64
git add .terraform.lock.hcl
```

Until it is committed, CI bridges the gap by passing the lock file resolved in
`tf_plan` to `tf_apply` as an artifact (see `.gitlab-ci.yml`), so a single
pipeline stays self-consistent — but a committed lock file is the durable fix.

## Status

Scaffold — written to be correct against bpg/proxmox ~0.109 but **not yet run
against a live Proxmox**. Validate with `terraform validate` and a `plan`
against your environment before applying.
