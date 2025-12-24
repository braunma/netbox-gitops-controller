# Cable Reconciliation Enhancements

## Overview

This document describes the comprehensive cable reconciliation enhancements implemented for the NetBox GitOps Controller. These changes add full cable management support with idempotency guarantees, enhanced diff visualization, and extensive debug logging.

## Changes Summary

### 1. **NEW: Cable Reconciliation Module** (`pkg/reconciler/cables.go`)

A dedicated cable reconciler that provides:

#### Idempotency Guarantees
- **Bidirectional cable detection**: A→B equals B→A (cables are stored with canonical ordering)
- **Duplicate prevention**: Processed pairs are tracked to prevent creating the same cable twice
- **Smart updates**: Existing cables are verified and only updated if configuration changes
- **Proper lookup**: Searches both cable directions to find existing connections

#### Features
- Support for all port types: `dcim.interface`, `dcim.frontport`, `dcim.rearport`
- Cable attributes: type, color, length, length_unit
- Comprehensive debug output with visual separators
- Dry-run support

#### Key Methods
```go
ReconcileCable(aEnd, bEnd *CableEndpoint, link *LinkConfig) error
```
- Creates or updates a cable between two endpoints
- Automatically handles bidirectional matching
- Idempotent: running multiple times produces the same result

### 2. **ENHANCED: Diff Visualization** (`pkg/client/client.go`)

The Apply method now provides rich visual output for pipeline console visibility:

#### CREATE Operations
```
  ✓ Creating interfaces: name=eth0
    ┌─ Changes ────────────────────
    │ + type: "1000base-t"
    │ + enabled: true
    │ + device: {id: 123}
    └──────────────────────────────
```

#### UPDATE Operations
```
  ⟳ Updating interfaces (ID: 456): name=eth0
    ┌─ Changes ────────────────────
    │ ~ enabled:
    │   - false
    │   + true
    │ ~ mtu:
    │   - 1500
    │   + 9000
    └──────────────────────────────
  ✓ Update complete
```

#### Benefits
- **Pipeline visibility**: Clear output in CI/CD console logs
- **Change tracking**: See exactly what changed (old → new)
- **Visual hierarchy**: Box drawing characters for structure
- **Color coding**: Success (green), warnings (yellow), info (cyan)

### 3. **ENHANCED: Device Reconciliation** (`pkg/reconciler/devices.go`)

#### Two-Phase Reconciliation
The device reconciler now operates in two phases to ensure all endpoints exist before creating cables:

**Phase 1: Devices and Ports**
- Create/update all devices
- Create/update all interfaces, front ports, rear ports
- Queue cable definitions for later processing

**Phase 2: Cables**
- Process all queued cables
- Both endpoints guaranteed to exist
- Idempotent cable creation/updates

#### New Port Support
- **Front Ports**: Full reconciliation with rear port linkage
- **Rear Ports**: Support for passive infrastructure (patch panels)
- **Cable Queueing**: All port types can define cable links

#### Debug Logging
Extensive debug output at every level:
```
──── Device 1/3: berlin-leaf-01 ────
  Reconciling interfaces for berlin-leaf-01...
    Interface 1/5: Eth1/1
      Untagged VLAN: Server-Production (ID: 42)
      Cable: Eth1/1 → berlin-srv-web-01[eth0]
  Reconciling front ports for berlin-leaf-01...
    Front Port 1/2: Port1
      Cable: Port1 → berlin-pp-01[1]
  Reconciling rear ports for berlin-leaf-01...
    Rear Port 1/1: Backbone1
      Cable: Backbone1 → frankfurt-pp-01[1]
```

### 4. **Port Lookup System**

Smart port discovery across all port types:

```go
findPort(deviceName, portName string) *portInfo
```

Searches in order:
1. Interfaces (`dcim.interface`)
2. Front ports (`dcim.frontport`)
3. Rear ports (`dcim.rearport`)

Returns the correct object type and ID for cable termination.

## Idempotency Details

### What Makes This Idempotent?

1. **Canonical Cable Pairing**
   - Cables are identified by sorted endpoint IDs
   - `A→B` and `B→A` resolve to the same canonical pair
   - Prevents duplicate cables for the same connection

2. **Processed Pair Tracking**
   - Each cable pair is marked as processed
   - Subsequent reconciliation runs skip already-processed pairs
   - Safe to run multiple times

3. **Existing Cable Detection**
   - Queries NetBox for cables matching both endpoints
   - Checks both directions (A→B and B→A)
   - Updates only if configuration differs

4. **Apply Method Idempotency**
   - All resources use lookup-based creation
   - Existing resources are updated only if changed
   - No-op if resource matches desired state

### Testing Idempotency

Run the reconciliation multiple times:
```bash
./netbox-gitops  # First run: creates resources
./netbox-gitops  # Second run: no changes (idempotent)
./netbox-gitops  # Third run: still no changes
```

Expected output on subsequent runs:
```
  = No changes for interfaces (ID: 123)
  = No changes for cables (ID: 456)
```

## Debug Logging

### Log Levels

**INFO (Cyan)**: High-level operations
```
Reconciling 5 devices...
Reconciling 12 pending cable connections...
```

**SUCCESS (Green)**: Successful operations
```
✓ Creating interfaces: name=eth0
✓ Cable created successfully
```

**DEBUG (Dim/Gray)**: Detailed trace information
```
→ Applying interfaces with lookup: {device_id=123 name=eth0}
= No changes for interfaces (ID: 456)
┌─ Cable Reconciliation ─────────────────────────
│ A-End: berlin-leaf-01 [Eth1/1] → dcim.interface (ID: 789)
│ B-End: berlin-srv-web-01 [eth0] → dcim.interface (ID: 790)
│ Status: Cable exists (ID: 42)
│ Action: No changes needed
└────────────────────────────────────────────────
```

**WARNING (Yellow)**: Non-fatal issues
```
⚠ Peer port not found: frankfurt-srv-99::eth0
⚠ Rear port Backbone1 not found, skipping front port
```

**ERROR (Red)**: Fatal errors
```
✗ Failed to reconcile device berlin-leaf-01: site not found
```

### Enabling Debug Output

Debug logs are always enabled. To see more detail in CI/CD:
- Check pipeline console output
- Debug messages use dim/faint formatting
- Visual separators (`┌─`, `│`, `└─`) make structure clear

## Pipeline Console Output

The enhancements are specifically designed for pipeline visibility:

### Before (Minimal Output)
```
Reconciling 5 devices...
Done
```

### After (Rich Output)
```
═══════════════════════════════════════════════════════
Phase 1: Foundation
═══════════════════════════════════════════════════════
Reconciling 3 tags...
  ✓ Creating tags: slug=gitops
  = No changes for tags (ID: 1)

═══════════════════════════════════════════════════════
Phase 3: Devices
═══════════════════════════════════════════════════════
Reconciling 5 devices...
──── Device 1/5: berlin-leaf-01 ────
  → Applying devices with lookup: {name=berlin-leaf-01 site_id=1}
  = No changes for devices (ID: 42)
  Reconciling interfaces for berlin-leaf-01...
    Interface 1/3: Eth1/1
      ✓ Creating interfaces: name=Eth1/1
      Cable: Eth1/1 → berlin-srv-web-01[eth0]

═══ Phase 2: Cables ═══
Reconciling 8 pending cable connections...
┌─ Cable Reconciliation ─────────────────────────
│ A-End: berlin-leaf-01 [Eth1/1] → dcim.interface (ID: 100)
│ B-End: berlin-srv-web-01 [eth0] → dcim.interface (ID: 101)
│ Action: Creating new cable
│   Type: dac-active
│ Result: Cable created successfully
└────────────────────────────────────────────────
```

## Usage Examples

### Interface with Cable
```yaml
interfaces:
  - name: "Eth1/1"
    type: "25gbase-x-sfp28"
    enabled: true
    link:
      peer_device: "berlin-srv-web-01"
      peer_port: "eth0"
      cable_type: "dac-active"
      color: "blue"
      length: 2.5
      length_unit: "m"
```

### Front Port with Cable (Patch Panel)
```yaml
front_ports:
  - name: "1"
    type: "lc"
    rear_port: "1"
    rear_port_position: 1
    link:
      peer_device: "berlin-pp-02"
      peer_port: "1"
      cable_type: "smf"
      length: 30
      length_unit: "m"
```

### Rear Port with Cable (Backbone)
```yaml
rear_ports:
  - name: "Backbone1"
    type: "lc"
    positions: 1
    link:
      peer_device: "frankfurt-pp-01"
      peer_port: "Backbone1"
      cable_type: "smf-os2"
      length: 500
      length_unit: "m"
```

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    DeviceReconciler                     │
│                                                          │
│  ┌──────────────────────────────────────────────────┐  │
│  │ Phase 1: Devices & Ports                         │  │
│  │  • Create/update devices                         │  │
│  │  • Create/update interfaces → queue cables       │  │
│  │  • Create/update front ports → queue cables      │  │
│  │  • Create/update rear ports → queue cables       │  │
│  └──────────────────────────────────────────────────┘  │
│                         ↓                               │
│  ┌──────────────────────────────────────────────────┐  │
│  │ Phase 2: Cables                                  │  │
│  │  • Build port lookup table                       │  │
│  │  • Resolve peer ports                            │  │
│  │  • Call CableReconciler for each pair            │  │
│  └──────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
                          ↓
         ┌────────────────────────────────────┐
         │     CableReconciler                │
         │  • Check if already processed      │
         │  • Find existing cable (both dirs) │
         │  • Create or update cable          │
         │  • Verify idempotency              │
         └────────────────────────────────────┘
```

## GitOps Tag Integration

All cables created by the reconciler are automatically tagged with the managed GitOps tag:
- Cables created: Tagged with `gitops` tag
- Enables tracking of GitOps-managed resources
- Supports future cleanup/pruning operations

## Backward Compatibility

- Existing configurations without cables continue to work
- Cable definitions are optional (`link` field is omitempty)
- No breaking changes to existing YAML schemas
- Dry-run mode fully supported

## Future Enhancements

Potential areas for future work:
1. Cable color validation against NetBox choices
2. Cable length validation
3. Automatic cable type inference from port types
4. Cable label/description support
5. Pruning of cables not in GitOps definitions
6. Support for multi-conductor cables
7. Cable status management (connected/planned/etc.)

## Testing

### Manual Testing
```bash
# Dry run to see what would happen
./netbox-gitops --dry-run

# Apply changes
./netbox-gitops

# Verify idempotency
./netbox-gitops  # Should show "No changes"
```

### Validation
1. Check NetBox UI for created cables
2. Verify cable attributes (type, color, length)
3. Confirm bidirectional linkage
4. Run multiple times to verify idempotency

## Summary

These enhancements transform the NetBox GitOps Controller into a comprehensive infrastructure-as-code tool with:

✅ **Complete cable management** - Interfaces, front ports, rear ports
✅ **Full idempotency** - Safe to run multiple times
✅ **Pipeline visibility** - Rich console output with diffs
✅ **Extensive debugging** - Trace every operation
✅ **Production-ready** - Handles edge cases and errors gracefully

The implementation follows GitOps principles: declarative, idempotent, and version-controlled.
