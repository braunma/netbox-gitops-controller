// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"errors"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// Dataset is a whole data directory, loaded and model-validated: the
// definitions that declare what may be referenced, and the inventory that
// references it.
//
// Any field may be empty. A kind that is not declared at all means "this
// repository does not manage that kind", not "this repository is full of
// broken references" — the checkers rely on that distinction.
type Dataset struct {
	Sites         []*models.Site
	Racks         []*models.Rack
	Roles         []*models.Role
	VRFs          []*models.VRF
	VLANGroups    []*models.VLANGroup
	VLANs         []*models.VLAN
	Prefixes      []*models.Prefix
	DeviceTypes   []*models.DeviceType
	ModuleTypes   []*models.ModuleType
	Platforms     []*models.Platform
	TenantGroups  []*models.TenantGroup
	Tenants       []*models.Tenant
	ClusterTypes  []*models.ClusterType
	ClusterGroups []*models.ClusterGroup
	Clusters      []*models.Cluster
	Devices       []*models.DeviceConfig
	VMs           []*models.VMConfig
	// CustomFields are the custom field definitions this repository declares.
	// A device may set values into them, so a checker needs the set of names
	// that exist to tell a real field from a typo.
	CustomFields []*models.CustomField
	// Tags are the tag definitions, so a declared `tags:` entry can be told
	// apart from a typo.
	Tags []*models.Tag
	// CablePaths lists every `link.via` path, expanded, one line per path
	// with every port along it. The cables themselves are already on the
	// devices; this is for the log and the checks.
	CablePaths []string
}

// DatasetOptions carries the paths that are resolved outside the loader, and
// which part of the directory to read.
type DatasetOptions struct {
	// DeviceTypeLibrary and ModuleTypeLibrary are the community-format library
	// roots. A root that does not exist is skipped, so the conventional path
	// can always be passed.
	DeviceTypeLibrary string
	ModuleTypeLibrary string
	// Phases limits the load to the folders the sync's selected phases read
	// (see Phases); the kinds of an unselected phase stay empty. Nil loads
	// everything, which is what every checker wants.
	Phases map[string]bool
}

// The sync's phases, as --only names them. Each kind of the data directory
// belongs to exactly one, so a sync restricted to some phases can read, and
// be stopped by, only the files those phases apply.
const (
	PhaseFoundation     = "foundation"
	PhaseNetwork        = "network"
	PhaseDeviceTypes    = "device-types"
	PhaseDevices        = "devices"
	PhaseVirtualization = "virtualization"
)

// Phases lists the sync's phases in the order they run.
var Phases = []string{PhaseFoundation, PhaseNetwork, PhaseDeviceTypes, PhaseDevices, PhaseVirtualization}

// LoadDataset reads every definition and inventory folder of a data directory
// through the typed loader, which model-validates each object as it goes.
// Missing folders are skipped, so a partial layout loads fine.
//
// The device and module types are merged with their community-format libraries
// exactly as the reconciler merges them, so a reference to a vendored type
// resolves against the same set the sync will see.
func (dl *DataLoader) LoadDataset(opts DatasetOptions) (Dataset, error) {
	var ds Dataset

	// Every kind is loaded even after one fails, so a single run reports the
	// problems of all of them. Each load names its phase, so a sync that
	// runs only some phases reads only their files.
	var errs []error
	load := func(phase string, fn func() error) {
		if opts.Phases != nil && !opts.Phases[phase] {
			return
		}
		if e := fn(); e != nil {
			errs = append(errs, e)
		}
	}

	// In the order the foundation phase applies them.
	load(PhaseFoundation, func() (e error) { ds.Tags, e = dl.LoadTags("definitions/extras"); return })
	load(PhaseFoundation, func() (e error) { ds.Roles, e = dl.LoadRoles("definitions/roles"); return })
	load(PhaseFoundation, func() (e error) { ds.CustomFields, e = dl.LoadCustomFields("definitions/custom_fields"); return })
	load(PhaseFoundation, func() (e error) { ds.Platforms, e = dl.LoadPlatforms("definitions/platforms"); return })
	load(PhaseFoundation, func() (e error) { ds.TenantGroups, e = dl.LoadTenantGroups("definitions/tenant_groups"); return })
	load(PhaseFoundation, func() (e error) { ds.Tenants, e = dl.LoadTenants("definitions/tenants"); return })
	load(PhaseFoundation, func() (e error) { ds.Sites, e = dl.LoadSites("definitions/sites"); return })
	load(PhaseFoundation, func() (e error) { ds.Racks, e = dl.LoadRacks("definitions/racks"); return })

	load(PhaseNetwork, func() (e error) { ds.VRFs, e = dl.LoadVRFs("definitions/vrfs"); return })
	load(PhaseNetwork, func() (e error) { ds.VLANGroups, e = dl.LoadVLANGroups("definitions/vlan_groups"); return })
	load(PhaseNetwork, func() (e error) { ds.VLANs, e = dl.LoadVLANs("definitions/vlans"); return })
	load(PhaseNetwork, func() (e error) { ds.Prefixes, e = dl.LoadPrefixes("definitions/prefixes"); return })

	var nativeModuleTypes, libraryModuleTypes []*models.ModuleType
	load(PhaseDeviceTypes, func() (e error) { nativeModuleTypes, e = dl.LoadModuleTypes("definitions/module_types"); return })
	load(PhaseDeviceTypes, func() (e error) { libraryModuleTypes, e = dl.LoadModuleTypeLibrary(opts.ModuleTypeLibrary); return })

	var nativeDeviceTypes, libraryDeviceTypes []*models.DeviceType
	load(PhaseDeviceTypes, func() (e error) { nativeDeviceTypes, e = dl.LoadDeviceTypes("definitions/device_types"); return })
	load(PhaseDeviceTypes, func() (e error) { libraryDeviceTypes, e = dl.LoadDeviceTypeLibrary(opts.DeviceTypeLibrary); return })

	// The reconciler's device folders, named explicitly so VM definitions are
	// not mis-parsed as devices by a recursive scan.
	var devices []*models.DeviceConfig
	for _, folder := range DeviceFolders {
		load(PhaseDevices, func() error {
			found, e := dl.LoadDevices(folder)
			devices = append(devices, found...)
			return e
		})
	}

	load(PhaseVirtualization, func() (e error) {
		ds.ClusterTypes, e = dl.LoadClusterTypes("definitions/virtualization/cluster_types")
		return
	})
	load(PhaseVirtualization, func() (e error) {
		ds.ClusterGroups, e = dl.LoadClusterGroups("definitions/virtualization/cluster_groups")
		return
	})
	load(PhaseVirtualization, func() (e error) { ds.Clusters, e = dl.LoadClusters("definitions/virtualization/clusters"); return })
	for _, folder := range VMFolders {
		load(PhaseVirtualization, func() error {
			found, e := dl.LoadVMs(folder)
			ds.VMs = append(ds.VMs, found...)
			return e
		})
	}

	if err := errors.Join(errs...); err != nil {
		return Dataset{}, err
	}

	ds.ModuleTypes = MergeModuleTypes(nativeModuleTypes, libraryModuleTypes, dl.logger)
	ds.DeviceTypes = MergeDeviceTypes(nativeDeviceTypes, libraryDeviceTypes, dl.logger)
	ds.Devices = devices

	// Paths through patch panels become their cables here, once everything
	// they refer to (the panels, their device types) is loaded.
	paths, err := ExpandCablePaths(&ds)
	if err != nil {
		return Dataset{}, err
	}
	ds.CablePaths = paths
	for _, p := range paths {
		dl.logger.Info("Cable path: %s", p)
	}
	return ds, nil
}
