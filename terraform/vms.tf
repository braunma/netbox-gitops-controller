locals {
  # A VM has a single default route, so the gateway is applied to exactly one
  # NIC: the interface flagged primary, else the first with a static IP. -1
  # means no eligible interface, in which case no gateway is set at all.
  gateway_iface = {
    for k, vm in var.vms : k => try(
      index(vm.interfaces[*].primary, true),
      index([for i in vm.interfaces : i.ip != ""], true),
      -1
    )
  }
}

resource "proxmox_virtual_environment_vm" "this" {
  for_each = var.vms

  name      = each.value.name
  vm_id     = each.value.vmid
  node_name = each.value.node
  pool_id   = each.value.cluster != "" ? each.value.cluster : null
  tags      = each.value.tags
  on_boot   = var.vm_on_boot

  clone {
    vm_id = each.value.vm_template_id
    full  = true
  }

  cpu {
    cores = each.value.vcpus
    # Cluster-portable CPU type so cloned VMs can live-migrate between nodes.
    type = var.cpu_type
  }

  memory {
    dedicated = each.value.memory
  }

  # Only override the disk when a size is declared; otherwise the clone inherits
  # the template's disk (Proxmox can grow a disk but never shrink it).
  dynamic "disk" {
    for_each = each.value.disk > 0 ? [each.value.disk] : []
    content {
      datastore_id = var.datastore_id
      interface    = "scsi0"
      size         = disk.value
    }
  }

  # One Proxmox NIC per declared interface, in order. The VLAN name is mapped to
  # an 802.1q tag via var.vlan_tags. An interface with an empty vlan is left
  # untagged (native VLAN); a *named* vlan must be present in var.vlan_tags — the
  # precondition below rejects an unmapped name rather than silently landing the
  # NIC on the native VLAN.
  dynamic "network_device" {
    for_each = each.value.interfaces
    content {
      bridge = var.network_bridge
      # Total expression (never errors): an unmapped name yields null here, but
      # the vlan precondition below fails the plan before that can be applied, so
      # a typo'd VLAN can't silently end up untagged.
      vlan_id = lookup(var.vlan_tags, network_device.value.vlan, null)
    }
  }

  initialization {
    datastore_id = var.datastore_id

    # One cloud-init ip_config per interface, in network_device order
    # (ipconfig0, ipconfig1…): the declared static IP, or DHCP when none is set.
    # The gateway is attached only to the primary interface (local.gateway_iface)
    # so a multi-NIC VM gets a single default route.
    dynamic "ip_config" {
      for_each = each.value.interfaces
      content {
        ipv4 {
          address = ip_config.value.ip != "" ? ip_config.value.ip : "dhcp"
          gateway = (ip_config.value.ip != "" && var.default_gateway != "" && ip_config.key == local.gateway_iface[each.key]) ? var.default_gateway : null
        }
      }
    }

    dynamic "dns" {
      for_each = length(var.dns_servers) > 0 ? [1] : []
      content {
        servers = var.dns_servers
      }
    }

    dynamic "user_account" {
      for_each = var.ci_username != "" || length(var.ci_ssh_keys) > 0 ? [1] : []
      content {
        username = var.ci_username != "" ? var.ci_username : null
        keys     = var.ci_ssh_keys
      }
    }
  }

  # The QEMU guest agent lets Proxmox/Terraform read the VM's IPs and shut it
  # down cleanly. Keep it enabled ONLY if the cloned template actually runs
  # qemu-guest-agent: with it enabled and the agent absent, every `terraform
  # apply` blocks until the provider's create timeout and then fails. Toggle via
  # var.agent_enabled for templates without the agent.
  agent {
    enabled = var.agent_enabled
  }

  lifecycle {
    # Fail with a clear message (rather than a cryptic provider error) when a VM
    # is missing its clone template id. tfgen already enforces this, so this only
    # trips for hand-edited tfvars.
    precondition {
      condition     = each.value.vm_template_id > 0
      error_message = "VM ${each.value.name}: vm_template_id is required (the Proxmox VMID of the template to clone, e.g. 800)."
    }

    # A named VLAN that isn't mapped in var.vlan_tags would otherwise be left
    # untagged and silently place the NIC on the bridge's native VLAN — a
    # security risk (wrong/often management network). Fail loudly instead; set
    # the interface vlan to "" to intend untagged.
    precondition {
      condition = alltrue([
        for i in each.value.interfaces : i.vlan == "" || contains(keys(var.vlan_tags), i.vlan)
      ])
      error_message = "VM ${each.value.name}: interface VLAN(s) not mapped in var.vlan_tags (have ${jsonencode(keys(var.vlan_tags))}). Add the VLAN name => 802.1q tag, or set the interface vlan to \"\" for untagged."
    }
  }
}
