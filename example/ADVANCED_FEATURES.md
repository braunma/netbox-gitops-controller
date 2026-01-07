# Advanced Features Examples

This directory contains examples demonstrating advanced NetBox GitOps features.

## Features

### 1. Self-Healing Device Bays

Device bays are automatically created on devices based on their device type templates.

**Example:** See `definitions/device_types/example-chassis.yaml`

```yaml
# Device type with bay templates
device_bays:
  - name: "Blade-1"
    label: "Blade Slot 1"
  - name: "Blade-2"
    label: "Blade Slot 2"
```

When a device of this type is created, the controller automatically creates these bays.

### 2. Parent Device and Device Bay Installation

Child devices can be installed into parent device bays (e.g., blade servers in a chassis).

**Example:** See `inventory/hardware/active/chassis.yaml`

```yaml
# Parent chassis
- name: "example-chassis-01"
  device_type_slug: "example-blade-chassis"
  rack_slug: "rack-a01"
  position: 20

# Child blade server
- name: "example-blade-01"
  device_type_slug: "example-blade-server"
  parent_device: "example-chassis-01"  # References parent
  device_bay: "Blade-1"                # Bay to install into
```

**Important Notes:**
- Child device types **must** have `u_height: 0` and `subdevice_role: "child"`
- The child device inherits the rack from its parent
- The controller handles the two-step installation process automatically

### 3. Module Installation with Managed Tags

Modules are installed into module bays and automatically tagged with the GitOps managed tag.

**Example:** See `inventory/hardware/active/gpu-servers.yaml`

```yaml
modules:
  - name: "GPU-1"
    module_type_slug: "ex-gpu-a100"
    status: "active"
    serial: "GPU-CARD-001"
    description: "Primary GPU"
```

**Features:**
- Modules are automatically tagged with `gitops:managed`
- Serial field defaults to empty string if not provided (matches NetBox requirements)
- Module bays are auto-created from device type templates

### 4. Rack/Face/Position Handling

The controller properly handles rack, position, and face fields based on device context:

- **Rack devices:** Can have rack, position, and face
- **Child devices:** Cannot have position or face (installed in bays)
- **Rackless devices:** Cannot have rack, position, or face

This prevents NetBox API errors like "Cannot select a rack face without assigning a rack."

## File Organization

```
example/
├── definitions/
│   ├── device_types/
│   │   ├── example-chassis.yaml      # Chassis with device bays
│   │   ├── example-blade.yaml        # Blade server (child)
│   │   └── example-gpu-server.yaml   # Server with module bays
│   └── module_types/
│       └── module_types.yaml          # GPU module types
└── inventory/
    └── hardware/
        └── active/
            ├── chassis.yaml           # Chassis + blade examples
            └── gpu-servers.yaml       # GPU server with modules
```

## Testing

To test these features:

1. Ensure you have device types defined:
   ```bash
   ./netbox-gitops --data-dir example
   ```

2. The controller will:
   - Create device bay templates on device types
   - Create devices and their bays
   - Install child devices into parent bays
   - Install modules with managed tags

3. Verify in NetBox:
   - Check device bays are auto-created
   - Check blade servers are installed in chassis
   - Check modules have the `gitops:managed` tag

## Python Compatibility

All features in this Go implementation match the Python version's behavior:
- Module serial handling (lines 382-386)
- Managed tag on modules (lines 388-390)
- Device bay self-healing (lines 88-139)
- Parent device/bay installation (lines 155-258)
- Rack/face/position logic (lines 190-198)
