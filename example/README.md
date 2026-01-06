# Example Data for Testing

This directory contains example definitions and inventory files used by the test suite.

## Purpose

The example data serves two purposes:

1. **Test Data**: Used by automated tests to verify the GitOps controller functionality
2. **Documentation**: Demonstrates the structure and format of definition and inventory files

## Structure

```
example/
├── definitions/          # NetBox object definitions
│   ├── extras/          # Tags and custom fields
│   ├── roles/           # Device and VM roles
│   ├── sites/           # Data center locations
│   ├── racks/           # Rack definitions
│   ├── vrfs/            # Virtual Routing and Forwarding instances
│   ├── vlan_groups/     # VLAN groupings
│   ├── vlans/           # VLAN definitions
│   ├── prefixes/        # IP prefixes
│   ├── device_types/    # Device type templates
│   └── module_types/    # Module type templates
└── inventory/           # Hardware inventory
    └── hardware/
        ├── active/      # Active devices (servers, switches, etc.)
        └── passive/     # Passive devices (patch panels, PDUs, etc.)
```

## Using Your Own Data

This repository is designed to work with your private definitions and inventory:

1. Create your own `definitions/` and `inventory/` directories in the repository root
2. These directories are excluded from version control (see `.gitignore`)
3. The test suite uses the `example/` directory, so your private data remains separate
4. The application will use your actual `definitions/` and `inventory/` directories when run

## Format

All files use YAML format. Each file contains a list of objects with the appropriate fields for that object type. See the example files in this directory for reference.

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
