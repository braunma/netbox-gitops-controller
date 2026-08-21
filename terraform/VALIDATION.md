# Validating the Proxmox module against a live cluster

The module has never been run against a live Proxmox at its current provider
pin. This is the sequence to change that, with the failure modes worth knowing
about before you meet them.

Work through it against a **throwaway VM in the playground environment**, not
against anything you would miss. The whole exercise takes about half an hour,
most of it waiting for a clone.

## Before you start

You need:

| | |
|---|---|
| A Proxmox API token | `user@realm!tokenid=secret`, with permission to clone, create and destroy VMs in the target pool |
| A template VM | Its numeric VMID (e.g. `800`), reachable from the node you will build on |
| A node name | e.g. `pve-01`, exactly as Proxmox spells it |
| A datastore | Default is `local-lvm`; it holds both the disk and the cloud-init drive |
| A VLAN-aware bridge | Default is `vmbr0` |

Set the connection variables locally rather than in CI for the first run, so
nothing is triggered by a push:

```bash
export TF_VAR_proxmox_endpoint='https://pve.example.com:8006/api2/json'
export TF_VAR_proxmox_api_token='user@realm!tokenid=secret'
# Only for a self-signed cluster certificate:
# export TF_VAR_proxmox_insecure=true
```

## 1. Declare one throwaway VM

`inventory/virtual/playground/` is empty today, so nothing is provisioned at
all — every environment currently generates `{"vms": {}}`. Add one file:

```yaml
# inventory/virtual/playground/tf-smoke-01.yaml
name: "tf-smoke-01"
provision: true            # without this it is documented in NetBox only
vmid: 999                  # must not collide with an existing VMID
vm_template_id: 800        # the template to clone
node: "pve-01"
site_slug: "berlin-dc"
role_slug: "server"
platform: "ubuntu-22-04"
status: "active"
vcpus: 2
memory: 2048
disk: 20
tags: ["gitops", "throwaway"]
interfaces:
  - name: "eth0"
    ip:
      address: "10.0.100.99/24"
      dns_name: "tf-smoke-01.example.com"
    address_role: "primary"
```

Then render it and read the result before Terraform sees it:

```bash
go run ./cmd/tfgen --data-dir . --group playground --out -
```

The keys it emits are exactly the fields `variable "vms"` declares — verified —
so a mismatch here means the YAML is wrong, not the module.

## 2. Initialise and check the module statically

```bash
cd terraform
tofu init -backend=false      # no state yet; just fetch the provider
tofu fmt -check -recursive
tofu validate
```

`tofu validate` is what CI's `tf_validate` job runs. It checks the module's own
consistency, not your cluster.

## 3. Commit the provider lock file

This is the durable fix for a problem the pipeline currently works around by
passing `tf_plan`'s resolved lock to `tf_apply` as an artifact:

```bash
tofu providers lock -platform=linux_amd64 -platform=darwin_arm64
git add .terraform.lock.hcl
```

Without it, a patch release inside `~> 0.109.0` appearing between the plan and
the apply makes `tofu apply <planfile>` reject the saved plan.

## 4. Plan

```bash
go run ../cmd/tfgen --data-dir .. --group playground --out generated.playground.tfvars.json
tofu plan -var-file=generated.playground.tfvars.json -out=tfplan.playground
```

**Read the plan.** These are the five things most likely to be wrong on a first
run, in the order you will hit them:

1. **`pool_id` is the NetBox cluster name.** The module passes the VM's
   `cluster` straight through as a Proxmox pool ID. Proxmox will not create the
   pool, and its IDs do not allow spaces — a NetBox cluster named
   `Berlin Prod Cluster` fails where `berlin-prod-cluster` works. Either name
   the NetBox cluster the way Proxmox spells the pool, or leave the VM's
   cluster empty (the module then sets no pool).
2. **Every named VLAN must be mapped.** An interface's `vlan` is a *name*; the
   module looks it up in `TF_VAR_vlan_tags`. A name that is not mapped fails
   the plan by precondition, deliberately — an unmapped name would otherwise
   land the NIC untagged on the bridge's native VLAN, which is usually the
   management network. Set `TF_VAR_vlan_tags='{"Management"=10,"Production"=20}'`,
   or set the interface's vlan to `""` to mean untagged on purpose.
3. **`vm_template_id` must be a real template.** The precondition only checks
   that it is greater than zero; Proxmox decides the rest.
4. **`datastore_id` must exist on that node** — it carries both the disk and
   the cloud-init drive.
5. **The disk can only grow.** `disk` is applied only when non-zero, because
   Proxmox can grow a disk but never shrink one. Declaring less than the
   template has will fail at apply.

## 5. Apply, then look at the VM

```bash
tofu apply tfplan.playground
```

**If the apply hangs and then times out**, the likely cause is
`var.agent_enabled` (default `true`) with a template that does not run
`qemu-guest-agent`. The provider waits for the agent to report an address until
its create timeout. Set `TF_VAR_agent_enabled=false` and re-apply, or install
the agent in the template.

Then check, in Proxmox:

- the VM exists with the VMID and name you declared, in the expected pool;
- its NIC carries the VLAN tag you mapped, on the expected bridge;
- cloud-init applied the static IP, and **only the primary interface has a
  gateway** — the module attaches `var.default_gateway` to exactly one NIC, so
  a multi-NIC VM gets one default route;
- the guest boots and is reachable.

## 6. Prove convergence, then clean up

```bash
tofu plan -var-file=generated.playground.tfvars.json     # must be "No changes"
```

A second plan showing changes means something in the module does not match what
Proxmox stored — note which attribute, that is the finding worth reporting.

```bash
tofu destroy -var-file=generated.playground.tfvars.json
```

Then remove `inventory/virtual/playground/tf-smoke-01.yaml`.

## 7. Turn it on in CI

Only after the above:

```
ENABLE_PROXMOX=true
TF_VAR_proxmox_endpoint=…
TF_VAR_proxmox_api_token=…      # masked
TF_VAR_vlan_tags=…
```

`tf_apply` is manual per environment and refuses any plan that destroys or
replaces a VM unless that environment is named in `TF_ALLOW_DESTROY`. Leave it
that way: removing a VM from the YAML makes OpenTofu destroy a real machine.

> **Note on the empty state.** Every environment currently generates
> `{"vms": {}}`. Against an empty state that is a no-op, which is safe. Against
> a state that already holds VMs it is a request to destroy all of them — which
> is exactly what the destroy gate exists to stop, and why it should stay on.

## What to update afterwards

- `terraform/README.md`'s **Status** section, which currently says the module
  has not been run against a live Proxmox.
- The comments in `versions.tf` and `providers.tf`, which say the module was
  "validated against the live cluster" on the **0.83.x** provider line. The pin
  is now `~> 0.109.0`; whether that validation carried over is exactly what this
  exercise settles.
