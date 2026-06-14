# Operator-facing summary of what was provisioned. Nothing here feeds back into
# NetBox — the YAML is authoritative for everything NetBox tracks (the MAC, the
# one value Proxmox generates, is intentionally not modelled).
output "vms" {
  description = "Provisioned VMs: name => { vmid, node }."
  value = {
    for k, vm in proxmox_virtual_environment_vm.this : k => {
      vmid = vm.vm_id
      node = vm.node_name
    }
  }
}
