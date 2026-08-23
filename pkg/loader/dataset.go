// SPDX-License-Identifier: Apache-2.0

package loader

import (
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
	Sites       []*models.Site
	Racks       []*models.Rack
	Roles       []*models.Role
	VRFs        []*models.VRF
	VLANs       []*models.VLAN
	Prefixes    []*models.Prefix
	DeviceTypes []*models.DeviceType
	ModuleTypes []*models.ModuleType
	Platforms   []*models.Platform
	Tenants     []*models.Tenant
	Clusters    []*models.Cluster
	Devices     []*models.DeviceConfig
	VMs         []*models.VMConfig
}

// DatasetOptions carries the paths that are resolved outside the loader.
type DatasetOptions struct {
	// DeviceTypeLibrary and ModuleTypeLibrary are the community-format library
	// roots. A root that does not exist is skipped, so the conventional path
	// can always be passed.
	DeviceTypeLibrary string
	ModuleTypeLibrary string
}

// LoadDataset reads every definition and inventory folder of a data directory
// through the typed loader, which model-validates each object as it goes.
// Missing folders are skipped, so a partial layout loads fine.
//
// The device and module types are merged with their community-format libraries
// exactly as the reconciler merges them, so a reference to a vendored type
// resolves against the same set the sync will see.
func (dl *DataLoader) LoadDataset(opts DatasetOptions) (Dataset, error) {
	var ds Dataset

	var err error
	load := func(fn func() error) {
		if err == nil {
			err = fn()
		}
	}

	load(func() (e error) { ds.Sites, e = dl.LoadSites("definitions/sites"); return })
	load(func() (e error) { ds.Racks, e = dl.LoadRacks("definitions/racks"); return })
	load(func() (e error) { ds.Roles, e = dl.LoadRoles("definitions/roles"); return })
	load(func() (e error) { _, e = dl.LoadTags("definitions/extras"); return })
	load(func() (e error) { _, e = dl.LoadCustomFields("definitions/custom_fields"); return })
	load(func() (e error) { ds.VRFs, e = dl.LoadVRFs("definitions/vrfs"); return })
	load(func() (e error) { _, e = dl.LoadVLANGroups("definitions/vlan_groups"); return })
	load(func() (e error) { ds.VLANs, e = dl.LoadVLANs("definitions/vlans"); return })
	load(func() (e error) { ds.Prefixes, e = dl.LoadPrefixes("definitions/prefixes"); return })
	load(func() (e error) { ds.Platforms, e = dl.LoadPlatforms("definitions/platforms"); return })
	load(func() (e error) { _, e = dl.LoadTenantGroups("definitions/tenant_groups"); return })
	load(func() (e error) { ds.Tenants, e = dl.LoadTenants("definitions/tenants"); return })
	load(func() (e error) { _, e = dl.LoadClusterTypes("definitions/virtualization/cluster_types"); return })
	load(func() (e error) { _, e = dl.LoadClusterGroups("definitions/virtualization/cluster_groups"); return })
	load(func() (e error) { ds.Clusters, e = dl.LoadClusters("definitions/virtualization/clusters"); return })

	var nativeModuleTypes, libraryModuleTypes []*models.ModuleType
	load(func() (e error) { nativeModuleTypes, e = dl.LoadModuleTypes("definitions/module_types"); return })
	load(func() (e error) { libraryModuleTypes, e = dl.LoadModuleTypeLibrary(opts.ModuleTypeLibrary); return })

	var nativeDeviceTypes, libraryDeviceTypes []*models.DeviceType
	load(func() (e error) { nativeDeviceTypes, e = dl.LoadDeviceTypes("definitions/device_types"); return })
	load(func() (e error) { libraryDeviceTypes, e = dl.LoadDeviceTypeLibrary(opts.DeviceTypeLibrary); return })

	// The reconciler's device folders, named explicitly so VM definitions are
	// not mis-parsed as devices by a recursive scan.
	var devices []*models.DeviceConfig
	for _, folder := range DeviceFolders {
		load(func() error {
			found, e := dl.LoadDevices(folder)
			devices = append(devices, found...)
			return e
		})
	}
	for _, folder := range VMFolders {
		load(func() error {
			found, e := dl.LoadVMs(folder)
			ds.VMs = append(ds.VMs, found...)
			return e
		})
	}

	if err != nil {
		return Dataset{}, err
	}

	ds.ModuleTypes = MergeModuleTypes(nativeModuleTypes, libraryModuleTypes, dl.logger)
	ds.DeviceTypes = MergeDeviceTypes(nativeDeviceTypes, libraryDeviceTypes, dl.logger)
	ds.Devices = devices
	return ds, nil
}
