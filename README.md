# NetBox GitOps Controller

This Python tool enables **declarative management** (Infrastructure as Code) for a NetBox instance. It synchronizes definitions from YAML files idempotently against the NetBox API.

## 🚀 Key Features

  * **Single Source of Truth:** The YAML files in this repository represent the desired state of the network inventory.
  * **Idempotency:** The script calculates differences and only applies necessary changes. Repeated executions result in "No-Ops" (no API calls) if the state is already correct.
  * **Safety (Shared Management):**
      * Objects created by this tool are automatically stamped with a **`gitops`** tag.
      * ⚠️ **Safe Pruning:** The tool only deletes or overwrites objects that possess this tag. Manually created objects in NetBox (without the tag) are ignored and preserved.
  * **Auto-Wiring:** Physical cabling and LAG (Link Aggregation) members are automatically configured based on the YAML definition.
  * **Type Safety:** All input data is validated using **Pydantic** models before interacting with the API to prevent bad requests.


-----

## 📂 Project Structure

```text
.
├── definitions/          # Global Definitions (Blueprints)
│   ├── sites.yaml        # Data Center Locations
│   ├── device_types.yaml # Hardware Models (incl. Interface Templates)
│   ├── roles.yaml        # Device Roles (e.g., Server, Leaf Switch)
│   ├── vlans.yaml        # Global VLAN Definitions
│   └── prefixes.yaml     # IP Subnets / Prefixes
├── inventory/            # Concrete Hardware (Instances)
│   └── hardware/
│       ├── active/       # Active Servers & Switches
│       └── passive/      # Patch Panels, PDUs
└── src/                  # Core Logic (Do not modify manually)
    ├── main.py           # Entry Point
    ├── base.py           # Core Logic (Idempotency, Caching, Tagging)
    └── syncers/          # Type-specific synchronization logic
```

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

### 2\. Install Dependencies

It is recommended to use a virtual environment.

```bash
python -m venv venv
source venv/bin/activate  # Linux/macOS
# venv\Scripts\activate   # Windows

pip install -r requirements.txt
```

*(Required packages: `pynetbox`, `pydantic`, `rich`, `python-dotenv`)*

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
python src/main.py --dry-run
```

### 2\. Apply Changes

Executes the synchronization against the NetBox API.

```bash
python src/main.py
```