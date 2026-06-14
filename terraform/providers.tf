provider "proxmox" {
  endpoint  = var.proxmox_endpoint
  api_token = var.proxmox_api_token
  insecure  = var.proxmox_insecure

  # No ssh{} block: a full clone + cloud-init on the same node runs entirely over
  # the API (validated against the live cluster). SSH would only be needed for
  # snippet/file uploads or cross-node disk import, which this module doesn't do —
  # adding it would require an SSH agent in the CI runner for no benefit.
}
