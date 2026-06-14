terraform {
  required_version = ">= 1.5, < 2.0"

  required_providers {
    proxmox = {
      source = "bpg/proxmox"
      # Pinned to the 0.109.x line. The resource schema this module uses (clone,
      # cpu, memory, network_device, initialization) is unchanged from the 0.83.x
      # line validated against the live cluster. bpg is pre-1.0, so minor bumps
      # can break the schema — review the changelog before widening this.
      version = "~> 0.109.0"
    }
  }

  # State is kept in GitLab's managed Terraform state backend. The address and
  # credentials are supplied at `tofu init` time via -backend-config in CI
  # (see .gitlab-ci.yml), so nothing environment-specific lives in git.
  backend "http" {}
}
