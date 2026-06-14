# Resolve clone templates: tfgen gives us the template *name* (the platform
# slug); the bpg provider clones by numeric id, so build a name => id map from
# the templates Proxmox reports. Tag your templates with var.template_tags.
data "proxmox_virtual_environment_vms" "templates" {
  tags = var.template_tags
}

locals {
  template_ids = {
    for v in data.proxmox_virtual_environment_vms.templates.vms : v.name => v.vm_id
  }

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

  clone {
    vm_id = local.template_ids[each.value.platform]
    full  = true
  }

  cpu {
    cores = each.value.vcpus
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
  # an 802.1q tag via var.vlan_tags (unmapped names stay untagged).
  dynamic "network_device" {
    for_each = each.value.interfaces
    content {
      bridge  = var.network_bridge
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

  agent {
    enabled = true
  }

  # Fail with a clear message (rather than a cryptic map-index error) when a
  # VM's platform doesn't match any template discovered via var.template_tags.
  lifecycle {
    precondition {
      condition     = contains(keys(local.template_ids), each.value.platform)
      error_message = "VM ${each.value.name}: no Proxmox template named '${each.value.platform}' found among templates tagged ${join(", ", var.template_tags)}."
    }
  }
}
