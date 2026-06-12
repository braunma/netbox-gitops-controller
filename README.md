# NetBox GitOps Controller

This Go tool enables **declarative management** (Infrastructure as Code) for a NetBox instance. It synchronizes definitions from YAML files idempotently against the NetBox API.

## 🚀 Key Features

  * **Single Source of Truth:** The YAML files in this repository represent the desired state of the network inventory.
  * **Idempotency:** The script calculates differences and only applies necessary changes. Repeated executions result in "No-Ops" (no API calls) if the state is already correct.
  * **Safety (Shared Management):**
      * Objects created by this tool are automatically stamped with a **`gitops`** tag.
      * ⚠️ **Safe Pruning:** The tool only deletes or overwrites objects that possess this tag. Manually created objects in NetBox (without the tag) are ignored and preserved.
  * **Auto-Wiring:** Physical cabling and LAG (Link Aggregation) members are automatically configured based on the YAML definition.
  * **Type Safety:** All input data is validated against typed Go models before interacting with the API to prevent bad requests.


-----

## 📂 Project Structure

```text
.
├── definitions/          # Your Private Definitions (gitignored)
│   ├── sites/           # Data Center Locations
│   ├── device_types/    # Hardware Models (incl. Interface Templates)
│   ├── roles/           # Device Roles (e.g., Server, Leaf Switch)
│   ├── vlans/           # VLAN Definitions
│   ├── prefixes/        # IP Subnets / Prefixes
│   └── ...              # Other NetBox object types
├── inventory/           # Your Private Hardware Inventory (gitignored)
│   └── hardware/
│       ├── active/      # Active Servers & Switches
│       └── passive/     # Patch Panels, PDUs
├── example/             # Public Example Data for Tests
│   ├── definitions/     # Example definitions (for learning/testing)
│   └── inventory/       # Example inventory (for learning/testing)
├── pkg/                 # Go Implementation (Core Logic)
│   ├── client/          # NetBox API Client
│   ├── loader/          # YAML Data Loader
│   ├── models/          # Data Models
│   ├── reconciler/      # Synchronization Logic
│   └── utils/           # Utilities
└── cmd/                 # Command-Line Interface
    └── netbox-gitops/   # Main Entry Point
```

### 🔒 Private Data vs. Public Examples

**Important**: This repository separates your private data from public examples:

- **`definitions/` and `inventory/`**: Your actual private data (excluded from git via `.gitignore`)
- **`example/`**: Public example data used for tests and documentation (committed to git)

When you run the application, it uses your private `definitions/` and `inventory/` directories.
When you run tests, they use the `example/` directory.

-----

## 📝 Workflow: How to Add New Hardware

### Step 1: Define a Device Type (if new)

File: `definitions/device_types.yaml`

Here we define the "blueprint" including all physical ports. NetBox copies these ports *once* when a device is instantiated.

```yaml
- model: "Dell PowerEdge R640"
  slug: "dell-r640"
  manufacturer: "Dell"
  u_height: 1
  is_full_depth: true
  interfaces:
    - name: "idrac"
      type: "1000base-t"
      mgmt_only: true
    - name: "eth0"
      type: "25gbase-x-sfp28"
  # Optional: Front/Rear Ports for Patch Panels
```

### Step 2: Create a Device Instance (Server/Switch)

File: `inventory/hardware/active/servers.yaml`

Here we define the actual server.

```yaml
- name: "srv-web-01"
  site_slug: "berlin"
  role_slug: "server"
  device_type_slug: "dell-r640"
  rack_slug: "rack-a01"
  status: "active"
  
  # Interface Configuration (L2/L3 & Cabling)
  interfaces:
    - name: "idrac"
      ip: "10.0.10.50/24"
      
    - name: "eth0"
      ip: "10.0.20.50/24"
      address_role: "primary"  # Sets the Primary IP on the Device object
      link:
        peer_device: "sw-leaf-01"
        peer_port: "Eth1/1"
        cable_type: "cat6a"    # Optional
```

### Step 3: Configure Switch Ports & VLANs

File: `inventory/hardware/active/switches.yaml`

```yaml
- name: "sw-leaf-01"
  # ... (Header params like site, role...) ...
  interfaces:
    - name: "Eth1/1"
      mode: "access"
      untagged_vlan: "Server-Vlan"
      # OR for Trunks:
      # mode: "tagged"
      # tagged_vlans: ["Vlan10", "Vlan20"]
```

-----

## ⚠️ Important Concepts & Troubleshooting

### The `gitops` Tag

  * The script automatically tags every object it creates with `GitOps Managed` (slug: `gitops`).
  * **Safety Logic:** If you remove a device from the YAML file, the script checks NetBox.
      * If the object has the `gitops` tag -\> **DELETE** (Cleanup).
      * If the object has NO tag (created manually) -\> **IGNORE** (Protect manual data).

### Common Errors

**Error: "400 Bad Request: {'type': ['This field may not be blank.']}"**

  * **Cause:** A device interface or template is missing the `type` definition in the YAML.
  * **Solution:** Ensure every interface in `definitions/device_types.yaml` has a valid type (e.g., `1000base-t`, `virtual`, `lag`).

**Cables are "flapping" (Deleting... Creating... on every run)**

  * **Cause:** You likely assigned two different devices to the same peer port.
  * **Solution:** Check your `link:` definitions. A port can only support one cable connection.

**Changes to Device Type (e.g., adding a port) do not appear on existing servers**

  * **Cause:** This is standard NetBox behavior. Modifying the "Blueprint" (Device Type) does not automatically update already created "Instances" (Devices).
  * **Solution:** Either recreate the device (Delete + Sync) or manually update the components in NetBox using the "Sync components" button on the Device Type page. Global attributes like `u_height` update immediately.

  

## 🛠 Local Development

### 1\. Clone Repository

```bash
git clone <repo-url>
cd netbox-gitops
```

### 2\. Build the Binary

Requires Go 1.24 or later. Dependencies are managed via Go modules and downloaded automatically.

```bash
go build -o netbox-gitops ./cmd/netbox-gitops/
```

### 3\. Environment Configuration

Create a `.env` file in the root directory:

```ini
NETBOX_URL=https://netbox.example.com
NETBOX_TOKEN=your_api_token_here
# Optional: Disable SSL verification (Dev environments only)
# IGNORE_SSL_ERRORS=True
```

## ▶️ Usage

### 1\. Dry-Run (Simulation)

Shows exactly what changes *would* be applied without actually touching NetBox. **Always run this first\!**

```bash
./netbox-gitops --dry-run
```

### 2\. Apply Changes

Executes the synchronization against the NetBox API.

```bash
./netbox-gitops
```

## 📚 Example Files

This repository includes comprehensive **example inventory and definition files** that demonstrate all major features of the GitOps controller.

### What's Included

✅ **4 example sites** (Berlin DC, Frankfurt DC, Munich Lab, Hamburg DR)
✅ **8 device roles** (Server, Switch, Storage, Patch Panel, etc.)
✅ **12 IP prefixes** with VRF and VLAN mappings
✅ **8 VLANs** across multiple sites
✅ **6 racks** in different locations
✅ **9 device instances** (servers, switches, storage, patch panels)
✅ **Complete cabling examples** (auto-wiring demonstrations)
✅ **AI/ML infrastructure** (GPU-capable servers, high-speed networking)
✅ **Structured cabling** (patch panels with front/rear port mappings)

### Getting Started with Examples

See **[EXAMPLES.md](./EXAMPLES.md)** for:
- Detailed explanation of each example file
- Key concepts demonstrated (VRFs, VLANs, cabling, etc.)
- How to customize examples for your environment
- Common scenarios and troubleshooting

### Quick Test

To see the examples in action:

```bash
# Preview what would be created (uses the example data)
./netbox-gitops --dry-run --data-dir example

# Apply to your NetBox instance (requires .env configuration)
./netbox-gitops --data-dir example
```

**Note**: The examples create a complete test infrastructure suitable for learning and development. For production use, customize the files to match your actual environment.