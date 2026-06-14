terraform {
  required_version = ">= 1.5"

  required_providers {
    proxmox = {
      source = "bpg/proxmox"
      # Pinned to the 0.109.x line (latest at time of writing). bpg is pre-1.0,
      # so minor bumps can break — review the changelog before widening this.
      version = "~> 0.109.0"
    }
  }

  # State is kept in GitLab's managed Terraform state backend. The address and
  # credentials are supplied at `terraform init` time via -backend-config in CI
  # (see .gitlab-ci.yml), so nothing environment-specific lives in git.
  backend "http" {}
}
