# Example Data for Testing

This directory contains example definitions and inventory files used by the test suite.

## Purpose

The example data serves two purposes:

1. **Test Data**: Used by automated tests to verify the GitOps controller functionality
2. **Documentation**: Demonstrates the structure and format of definition and inventory files

It is also the dataset the end-to-end suites run against, including
`tests/e2e/ingest.sh`, which walks the whole fact-ingestion path over a
throwaway copy of it.

## Structure

```
example/
├── definitions/               # NetBox object definitions
│   ├── extras/                # Tags
│   ├── custom_fields/         # vmid, vm_template_id, and the 17 hw_* facts
│   ├── roles/                 # Device and VM roles
│   ├── platforms/             # Platforms / operating systems
│   ├── tenant_groups/         # Tenant groupings
│   ├── tenants/               # Tenants
│   ├── sites/                 # Data center locations
│   ├── racks/                 # Rack definitions
│   ├── vrfs/                  # Virtual Routing and Forwarding instances
│   ├── vlan_groups/           # VLAN groupings
│   ├── vlans/                 # VLAN definitions
│   ├── prefixes/              # IP prefixes
│   ├── device_types/          # Device type templates (native format)
│   ├── device_type_library/   # The same, in the community library layout
│   ├── module_types/          # Module type templates
│   ├── module_type_library/   # The same, in the community library layout
│   └── virtualization/        # Cluster types, cluster groups, and clusters
└── inventory/                 # Hardware and VM inventory
    ├── hardware/
    │   ├── active/            # Active devices (servers, switches, etc.)
    │   └── passive/           # Passive devices (patch panels, PDUs, etc.)
    └── virtual/               # Virtual machines (per-env folders, one per VM)
```

A real data directory grows one more folder that this example does not ship:
`inventory/discovered/`, where `netbox-gitops collect` and `ingest` write what
they found. It is left out here because its contents are produced by a scan of
live infrastructure, and a committed example of that would go stale the moment
anyone looked at it. See [`docs/INGEST.md`](../docs/INGEST.md).

## Using Your Own Data

This repository is designed to work with your private definitions and inventory:

1. Create your own `definitions/` and `inventory/` directories in the repository root
2. These directories are excluded from version control (see `.gitignore`)
3. The test suite uses the `example/` directory, so your private data remains separate
4. The application will use your actual `definitions/` and `inventory/` directories when run

## Format

All files use YAML format. Each file contains a list of objects with the appropriate fields for that object type. Device inventory files may alternatively use the grouped form — a `defaults:` block whose fields are merged into every entry of a `devices:` list (see `inventory/hardware/active/servers.yaml`). See the example files in this directory for reference.

## Testing

The test suite validates:
- YAML syntax correctness
- Required field presence
- Data type consistency
- Referential integrity (e.g., devices reference valid sites, racks, etc.)

To run tests:
```bash
go test ./...
```
