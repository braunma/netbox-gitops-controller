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

  disk {
    datastore_id = var.datastore_id
    interface    = "scsi0"
    size         = each.value.disk
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

    # Static IP via cloud-init for every interface that declares one. The order
    # matches the network_device order above (cloud-init ipconfig0, ipconfig1…).
    dynamic "ip_config" {
      for_each = each.value.interfaces
      content {
        ipv4 {
          address = ip_config.value.ip != "" ? ip_config.value.ip : "dhcp"
          gateway = ip_config.value.ip != "" && var.default_gateway != "" ? var.default_gateway : null
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
}
