# Go Interface Definitions for NetBox GitOps Controller

This document provides complete Go interface definitions to facilitate the Python → Go migration.

## Package Structure

```
netbox-gitops-controller/
├── cmd/
│   └── netbox-gitops/
│       └── main.go                  # CLI entry point
├── pkg/
│   ├── client/                      # NetBox API client
│   │   ├── client.go               # Main client implementation
│   │   ├── cache.go                # Cache manager
│   │   ├── tags.go                 # Tag manager
│   │   └── objects.go              # Object manager
│   ├── models/                      # Data models
│   │   ├── foundation.go           # Sites, Racks, Roles, Tags
│   │   ├── network.go              # VLANs, VRFs, Prefixes
│   │   ├── device_types.go         # Device and Module Types
│   │   └── devices.go              # Device configurations
│   ├── reconciler/                  # Reconciliation engine
│   │   ├── reconciler.go           # Base reconciler interface
│   │   ├── foundation.go           # Foundation reconcilers
│   │   ├── network.go              # Network reconcilers
│   │   └── device.go               # Device reconciler
│   ├── loader/                      # YAML loader
│   │   └── loader.go               # Data loader
│   └── utils/                       # Utilities
│       ├── logging.go              # Logging utilities
│       ├── color.go                # Color normalization
│       └── helpers.go              # Helper functions
└── go.mod
```

---

## 1. Client Package Interfaces

### 1.1 NetBox Client

```go
package client

import (
    "context"
    "time"
)

// NetBoxClient is the main interface for NetBox API operations
type NetBoxClient interface {
    // Core CRUD Operations
    Get(ctx context.Context, app, endpoint string, id int) (Object, error)
    Filter(ctx context.Context, app, endpoint string, filters map[string]interface{}) ([]Object, error)
    Create(ctx context.Context, app, endpoint string, data map[string]interface{}) (Object, error)
    Update(ctx context.Context, app, endpoint string, id int, data map[string]interface{}) error
    Delete(ctx context.Context, app, endpoint string, id int) error

    // High-level Operations
    Ensure(ctx context.Context, req EnsureRequest) (Object, error)

    // Component Access
    GetComponents(ctx context.Context, deviceID int, endpoint string) ([]Object, error)
    GetTermination(ctx context.Context, deviceName, portName string) (Object, TerminationType, error)

    // Device-specific
    UpdateDevicePrimaryIP(ctx context.Context, deviceID, ipID int) error

    // Managers
    Cache() CacheManager
    Tags() TagManager
    Objects() ObjectManager

    // Configuration
    SetDryRun(enabled bool)
    IsDryRun() bool
}

// Object represents a generic NetBox object
type Object map[string]interface{}

// EnsureRequest defines parameters for create-or-update operations
type EnsureRequest struct {
    App        string
    Endpoint   string
    Lookup     map[string]interface{}
    Payload    map[string]interface{}
    DiffUpdate bool  // Enable smart diff-based updates
}

// TerminationType represents the type of cable termination
type TerminationType string

const (
    TerminationInterface TerminationType = "dcim.interface"
    TerminationFrontPort TerminationType = "dcim.frontport"
    TerminationRearPort  TerminationType = "dcim.rearport"
)
```

### 1.2 Cache Manager

```go
package client

import "context"

// CacheManager handles NetBox object caching
type CacheManager interface {
    // Loading
    LoadGlobal(ctx context.Context) error
    LoadSite(ctx context.Context, siteSlug string) error

    // Access
    GetID(resource, identifier string) (int, bool)

    // Bulk Loading
    LoadResource(ctx context.Context, resource string, filters map[string]interface{}) error

    // Invalidation
    Invalidate(resource string)
    InvalidateAll()

    // Inspection
    Resources() []string
    Size(resource string) int
}

// CacheStrategy defines the loading strategy
type CacheStrategy string

const (
    CacheStrategyLazy  CacheStrategy = "lazy"   // Load on first access
    CacheStrategyEager CacheStrategy = "eager"  // Pre-load all data
)

// CacheConfig configures cache behavior
type CacheConfig struct {
    Strategy     CacheStrategy
    TTL          time.Duration
    MaxSize      int
    PreloadSites bool
}
```

### 1.3 Tag Manager

```go
package client

import "context"

// TagManager handles managed tag operations
type TagManager interface {
    // Ensure tag exists
    Ensure(ctx context.Context, slug string) (int, error)

    // Get tag ID
    GetID(ctx context.Context, slug string) (int, error)

    // Check if object is managed
    IsManaged(obj Object, managedTagID int) bool

    // Inject managed tag into payload
    InjectTag(payload map[string]interface{}, tagID int) map[string]interface{}

    // Extract tag IDs from object
    ExtractTagIDs(tags []interface{}) []int
}

// TagConfig defines tag creation parameters
type TagConfig struct {
    Slug        string
    Name        string
    Color       string
    Description string
}
```

### 1.4 Object Manager

```go
package client

import "context"

// ObjectManager handles object lifecycle operations
type ObjectManager interface {
    // Create or update with diff detection
    Ensure(ctx context.Context, req EnsureRequest) (Object, error)

    // Create children (templates, ports, etc.)
    EnsureChildren(ctx context.Context, req ChildrenRequest) error

    // Delete with safety checks
    SafeDelete(ctx context.Context, app, endpoint string, id int, force bool) error

    // Diff calculation
    CalculateDiff(existing Object, desired map[string]interface{}) map[string]interface{}
}

// ChildrenRequest defines parameters for syncing child objects
type ChildrenRequest struct {
    App          string
    Endpoint     string
    ParentFilter map[string]interface{}
    Children     []map[string]interface{}
    KeyField     string  // Unique identifier field (default: "name")
}
```

---

## 2. Reconciler Package Interfaces

### 2.1 Base Reconciler

```go
package reconciler

import "context"

// Reconciler is the base interface for all reconcilers
type Reconciler interface {
    // Reconcile a single object to desired state
    Reconcile(ctx context.Context, desired interface{}) error

    // Reconcile multiple objects
    ReconcileAll(ctx context.Context, desired []interface{}) error

    // Get reconciler name for logging
    Name() string

    // Set dry-run mode
    SetDryRun(enabled bool)
}

// ReconcilerConfig provides common configuration
type ReconcilerConfig struct {
    Client      client.NetBoxClient
    DryRun      bool
    Logger      Logger
    Concurrency int  // Number of concurrent reconciliations
}

// ReconcileResult represents the outcome of a reconciliation
type ReconcileResult struct {
    ObjectName string
    Action     ReconcileAction
    Changes    map[string]interface{}
    Error      error
}

// ReconcileAction represents what action was taken
type ReconcileAction string

const (
    ActionCreated ReconcileAction = "created"
    ActionUpdated ReconcileAction = "updated"
    ActionSkipped ReconcileAction = "skipped"
    ActionFailed  ReconcileAction = "failed"
)
```

### 2.2 Specialized Reconcilers

```go
package reconciler

import "context"

// SiteReconciler handles site synchronization
type SiteReconciler interface {
    Reconciler
    ReconcileSite(ctx context.Context, site *models.Site) error
}

// RackReconciler handles rack synchronization
type RackReconciler interface {
    Reconciler
    ReconcileRack(ctx context.Context, rack *models.Rack) error
}

// VLANReconciler handles VLAN synchronization
type VLANReconciler interface {
    Reconciler
    ReconcileVLAN(ctx context.Context, vlan *models.VLAN) error
}

// DeviceReconciler handles device and cable synchronization
type DeviceReconciler interface {
    Reconciler
    ReconcileDevice(ctx context.Context, device *models.DeviceConfig) error
    ReconcileCables(ctx context.Context, device *models.DeviceConfig) error
}

// DeviceTypeReconciler handles device type synchronization
type DeviceTypeReconciler interface {
    Reconciler
    ReconcileDeviceType(ctx context.Context, deviceType *models.DeviceType) error
    ReconcileTemplates(ctx context.Context, deviceTypeID int, templates interface{}) error
}
```

---

## 3. Models Package

### 3.1 Foundation Models

```go
package models

import "time"

// Site represents a NetBox site
type Site struct {
    Name        string   `yaml:"name" json:"name"`
    Slug        string   `yaml:"slug" json:"slug"`
    Status      string   `yaml:"status,omitempty" json:"status,omitempty"`
    Region      string   `yaml:"region,omitempty" json:"region,omitempty"`
    Timezone    string   `yaml:"timezone,omitempty" json:"timezone,omitempty"`
    Description string   `yaml:"description,omitempty" json:"description,omitempty"`
    Comments    string   `yaml:"comments,omitempty" json:"comments,omitempty"`
    Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Rack represents a NetBox rack
type Rack struct {
    Name        string  `yaml:"name" json:"name"`
    SiteSlug    string  `yaml:"site_slug" json:"site_slug"`
    Status      string  `yaml:"status,omitempty" json:"status,omitempty"`
    Width       int     `yaml:"width,omitempty" json:"width,omitempty"`
    UHeight     int     `yaml:"u_height,omitempty" json:"u_height,omitempty"`
    Description string  `yaml:"description,omitempty" json:"description,omitempty"`
    Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Role represents a device role
type Role struct {
    Name        string `yaml:"name" json:"name"`
    Slug        string `yaml:"slug" json:"slug"`
    Color       string `yaml:"color" json:"color"`
    VMRole      bool   `yaml:"vm_role,omitempty" json:"vm_role,omitempty"`
    Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// Tag represents a NetBox tag
type Tag struct {
    Name        string `yaml:"name" json:"name"`
    Slug        string `yaml:"slug" json:"slug"`
    Color       string `yaml:"color" json:"color"`
    Description string `yaml:"description,omitempty" json:"description,omitempty"`
}
```

### 3.2 Network Models

```go
package models

// VLAN represents a NetBox VLAN
type VLAN struct {
    Name        string   `yaml:"name" json:"name"`
    VID         int      `yaml:"vid" json:"vid"`
    SiteSlug    string   `yaml:"site_slug" json:"site_slug"`
    GroupSlug   string   `yaml:"group_slug,omitempty" json:"group_slug,omitempty"`
    Status      string   `yaml:"status,omitempty" json:"status,omitempty"`
    Role        string   `yaml:"role,omitempty" json:"role,omitempty"`
    Description string   `yaml:"description,omitempty" json:"description,omitempty"`
    Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// VRF represents a NetBox VRF
type VRF struct {
    Name        string `yaml:"name" json:"name"`
    RD          string `yaml:"rd,omitempty" json:"rd,omitempty"`
    Description string `yaml:"description,omitempty" json:"description,omitempty"`
    Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Slug generates a slug from the VRF name
func (v *VRF) Slug() string {
    return slugify(v.Name)
}

// Prefix represents an IP prefix
type Prefix struct {
    Prefix      string   `yaml:"prefix" json:"prefix"`
    SiteSlug    string   `yaml:"site_slug,omitempty" json:"site_slug,omitempty"`
    VRFName     string   `yaml:"vrf_name,omitempty" json:"vrf_name,omitempty"`
    VLANName    string   `yaml:"vlan_name,omitempty" json:"vlan_name,omitempty"`
    Status      string   `yaml:"status,omitempty" json:"status,omitempty"`
    Role        string   `yaml:"role,omitempty" json:"role,omitempty"`
    Description string   `yaml:"description,omitempty" json:"description,omitempty"`
    Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}
```

### 3.3 Device Configuration Models

```go
package models

// DeviceConfig represents a device with all its components
type DeviceConfig struct {
    // Basic Info
    Name            string  `yaml:"name" json:"name"`
    DeviceTypeSlug  string  `yaml:"device_type_slug" json:"device_type_slug"`
    RoleSlug        string  `yaml:"role_slug" json:"role_slug"`
    SiteSlug        string  `yaml:"site_slug" json:"site_slug"`

    // Placement
    RackSlug        string  `yaml:"rack_slug,omitempty" json:"rack_slug,omitempty"`
    Position        int     `yaml:"position,omitempty" json:"position,omitempty"`
    Face            string  `yaml:"face,omitempty" json:"face,omitempty"`

    // Blade/Node Installation
    ParentDevice    string  `yaml:"parent_device,omitempty" json:"parent_device,omitempty"`
    DeviceBay       string  `yaml:"device_bay,omitempty" json:"device_bay,omitempty"`

    // Metadata
    Serial          string  `yaml:"serial,omitempty" json:"serial,omitempty"`
    AssetTag        string  `yaml:"asset_tag,omitempty" json:"asset_tag,omitempty"`
    Status          string  `yaml:"status,omitempty" json:"status,omitempty"`
    Description     string  `yaml:"description,omitempty" json:"description,omitempty"`
    Comments        string  `yaml:"comments,omitempty" json:"comments,omitempty"`

    // Components
    Interfaces      []InterfaceConfig  `yaml:"interfaces,omitempty" json:"interfaces,omitempty"`
    FrontPorts      []FrontPortConfig  `yaml:"front_ports,omitempty" json:"front_ports,omitempty"`
    RearPorts       []RearPortConfig   `yaml:"rear_ports,omitempty" json:"rear_ports,omitempty"`
    Modules         []ModuleConfig     `yaml:"modules,omitempty" json:"modules,omitempty"`
}

// InterfaceConfig represents a network interface
type InterfaceConfig struct {
    Name           string   `yaml:"name" json:"name"`
    Type           string   `yaml:"type" json:"type"`
    Enabled        bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
    Speed          int      `yaml:"speed,omitempty" json:"speed,omitempty"`
    MTU            int      `yaml:"mtu,omitempty" json:"mtu,omitempty"`
    Description    string   `yaml:"description,omitempty" json:"description,omitempty"`

    // VLAN Configuration
    Mode           string   `yaml:"mode,omitempty" json:"mode,omitempty"`
    UntaggedVLAN   string   `yaml:"untagged_vlan,omitempty" json:"untagged_vlan,omitempty"`
    TaggedVLANs    []string `yaml:"tagged_vlans,omitempty" json:"tagged_vlans,omitempty"`

    // LAG Configuration
    LAG            string   `yaml:"lag,omitempty" json:"lag,omitempty"`
    Members        []string `yaml:"members,omitempty" json:"members,omitempty"`

    // IP Configuration
    IP             string   `yaml:"ip,omitempty" json:"ip,omitempty"`
    AddressRole    string   `yaml:"address_role,omitempty" json:"address_role,omitempty"`

    // Cabling
    Link           *LinkConfig `yaml:"link,omitempty" json:"link,omitempty"`
}

// LinkConfig represents a cable connection
type LinkConfig struct {
    PeerDevice  string `yaml:"peer_device" json:"peer_device"`
    PeerPort    string `yaml:"peer_port" json:"peer_port"`
    CableType   string `yaml:"cable_type,omitempty" json:"cable_type,omitempty"`
    Color       string `yaml:"color,omitempty" json:"color,omitempty"`
    Length      int    `yaml:"length,omitempty" json:"length,omitempty"`
}

// ModuleConfig represents an installed module (e.g., GPU)
type ModuleConfig struct {
    Name            string `yaml:"name" json:"name"`
    ModuleTypeSlug  string `yaml:"module_type_slug" json:"module_type_slug"`
    Status          string `yaml:"status,omitempty" json:"status,omitempty"`
    Serial          string `yaml:"serial,omitempty" json:"serial,omitempty"`
    AssetTag        string `yaml:"asset_tag,omitempty" json:"asset_tag,omitempty"`
    Description     string `yaml:"description,omitempty" json:"description,omitempty"`
}
```

---

## 4. Loader Package

```go
package loader

import (
    "context"
    "io"
)

// DataLoader loads and validates YAML files
type DataLoader interface {
    // Load objects from a folder
    LoadFolder(ctx context.Context, path string, model interface{}) error

    // Load a single file
    LoadFile(ctx context.Context, path string, model interface{}) error

    // Load from reader
    Load(ctx context.Context, reader io.Reader, model interface{}) error
}

// LoaderConfig configures the loader
type LoaderConfig struct {
    BasePath    string
    Recursive   bool
    Validate    bool
    StrictMode  bool
}
```

---

## 5. Utilities

### 5.1 Logging

```go
package utils

// Logger provides structured logging
type Logger interface {
    Debug(msg string, fields ...Field)
    Info(msg string, fields ...Field)
    Warn(msg string, fields ...Field)
    Error(msg string, err error, fields ...Field)
    Success(msg string, fields ...Field)
    DryRun(action, msg string, fields ...Field)
}

// Field represents a log field
type Field struct {
    Key   string
    Value interface{}
}
```

### 5.2 Color Utilities

```go
package utils

// NormalizeColor converts various color formats to NetBox format
func NormalizeColor(input string) string {
    // Implementation
}

// ColorMap returns default colors for cable types
func ColorMap() map[string]string {
    return map[string]string{
        "cat6":   "f44336",
        "cat6a":  "ffeb3b",
        "dac":    "000000",
        "fiber":  "00bcd4",
        // ...
    }
}
```

---

## 6. Main Application

```go
package main

import (
    "context"
    "os"

    "github.com/spf13/cobra"
)

var (
    dryRun bool
    config string
)

func main() {
    rootCmd := &cobra.Command{
        Use:   "netbox-gitops",
        Short: "NetBox GitOps Controller",
        Long:  `Declarative infrastructure management for NetBox`,
        RunE:  runSync,
    }

    rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Simulate changes without applying")
    rootCmd.Flags().StringVar(&config, "config", ".env", "Configuration file")

    if err := rootCmd.Execute(); err != nil {
        os.Exit(1)
    }
}

func runSync(cmd *cobra.Command, args []string) error {
    ctx := context.Background()

    // 1. Load configuration
    cfg, err := loadConfig(config)
    if err != nil {
        return err
    }

    // 2. Initialize client
    client := client.New(cfg.NetBoxURL, cfg.NetBoxToken, client.Options{
        DryRun: dryRun,
        Cache:  client.CacheConfig{Strategy: client.CacheStrategyEager},
    })

    // 3. Load data
    loader := loader.New(loader.LoaderConfig{BasePath: ".", Recursive: true})

    // 4. Run 3-phase sync
    orchestrator := NewOrchestrator(client, loader, dryRun)
    return orchestrator.Run(ctx)
}
```

---

## 7. Testing Interfaces

```go
package client_test

import "testing"

// MockNetBoxClient for testing
type MockNetBoxClient struct {
    GetFunc    func(ctx context.Context, app, endpoint string, id int) (Object, error)
    CreateFunc func(ctx context.Context, app, endpoint string, data map[string]interface{}) (Object, error)
    // ... other methods
}

func (m *MockNetBoxClient) Get(ctx context.Context, app, endpoint string, id int) (Object, error) {
    if m.GetFunc != nil {
        return m.GetFunc(ctx, app, endpoint, id)
    }
    return nil, nil
}

// Example test
func TestSiteReconciler(t *testing.T) {
    mockClient := &MockNetBoxClient{
        CreateFunc: func(ctx context.Context, app, endpoint string, data map[string]interface{}) (Object, error) {
            return Object{"id": 1, "name": "test-site"}, nil
        },
    }

    reconciler := reconciler.NewSiteReconciler(mockClient)
    site := &models.Site{Name: "test-site", Slug: "test-site"}

    err := reconciler.Reconcile(context.Background(), site)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
}
```

---

## Summary

This interface design provides:

✅ **Clear separation of concerns**
- Client layer handles API communication
- Reconciler layer handles business logic
- Models define data structures

✅ **Testability**
- All interfaces can be mocked
- Easy unit testing
- Integration tests with real NetBox instance

✅ **Concurrency-ready**
- Context-aware APIs
- Safe for goroutines
- Concurrent reconciliation support

✅ **Migration-friendly**
- Maps directly to existing Python code
- Preserves all current functionality
- Allows incremental migration

✅ **Go idioms**
- Interface-driven design
- Error handling via return values
- Context propagation
- Structured logging

**Next Steps:**
1. Initialize Go module
2. Implement client package
3. Implement models with validation
4. Implement reconcilers
5. Write comprehensive tests
6. Parallel run with Python version
