terraform {
  required_version = ">= 1.5"

  required_providers {
    proxmox = {
      source  = "bpg/proxmox"
      version = "~> 0.66"
    }
  }

  # State is kept in GitLab's managed Terraform state backend. The address and
  # credentials are supplied at `terraform init` time via -backend-config in CI
  # (see .gitlab-ci.yml), so nothing environment-specific lives in git.
  backend "http" {}
}
