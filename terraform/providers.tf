provider "proxmox" {
  endpoint = var.proxmox_endpoint
  # bpg wants the token as one 'user@realm!tokenid=secret' string; we keep the
  # id and secret as separate CI variables and join them here.
  api_token = "${var.proxmox_api_token_id}=${var.proxmox_api_token_secret}"
  insecure  = var.proxmox_insecure

  # Cloning + cloud-init disk import use SSH on the target node. Provide an
  # agent or username/password via the PROXMOX_VE_SSH_* env vars in CI.
  ssh {
    agent = true
  }
}
