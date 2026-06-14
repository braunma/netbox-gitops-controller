provider "proxmox" {
  endpoint = var.proxmox_endpoint
  # bpg wants the token as one 'user@realm!tokenid=secret' string; we keep the
  # id and secret as separate CI variables and join them here.
  api_token = "${var.proxmox_api_token_id}=${var.proxmox_api_token_secret}"
  insecure  = var.proxmox_insecure

  # No ssh{} block: a full clone + cloud-init on the same node runs entirely over
  # the API (validated against the live cluster). SSH would only be needed for
  # snippet/file uploads or cross-node disk import, which this module doesn't do —
  # adding it would require an SSH agent in the CI runner for no benefit.
}
