provider "proxmox" {
  endpoint  = var.proxmox_endpoint
  api_token = var.proxmox_api_token
  insecure  = var.proxmox_insecure

  # Cloning + cloud-init disk import use SSH on the target node. Provide an
  # agent or username/password via the PROXMOX_VE_SSH_* env vars in CI.
  ssh {
    agent = true
  }
}
