// SPDX-License-Identifier: Apache-2.0

// Package loader reads the YAML data directory into the typed models. Every
// kind is decoded through one node-based path that records provenance (file
// and node) for each object, which is the prerequisite for in-place fact
// editing of further kinds later (e.g. DNS names on IP addresses).
package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// DefaultIgnorePatterns are the filename globs skipped unless the caller asks
// for them. The underscore prefix is a parking convention: a file that is kept
// in the repository for reference, or is owned by another system, can be taken
// out of the sync without deleting or moving it.
var DefaultIgnorePatterns = []string{"_*.yaml", "_*.yml"}

// DataLoader handles loading and validating YAML configuration files
type DataLoader struct {
	basePath string
	logger   *utils.Logger

	// ignorePatterns are matched against each file's basename. Empty means
	// nothing is skipped.
	ignorePatterns []string

	// ignored accumulates the files skipped by those patterns, so a caller can
	// report them before doing something destructive. Loading is sequential
	// today; the mutex keeps that an implementation detail.
	ignoredMu sync.Mutex
	ignored   []string
}

// NewDataLoader creates a new data loader applying DefaultIgnorePatterns.
func NewDataLoader(basePath string, logger *utils.Logger) *DataLoader {
	return &DataLoader{
		basePath:       basePath,
		logger:         logger,
		ignorePatterns: DefaultIgnorePatterns,
	}
}

// SetIgnorePatterns replaces the filename globs that are skipped while
// loading. Passing no patterns disables skipping entirely, so every file that
// would otherwise be parked is loaded.
func (dl *DataLoader) SetIgnorePatterns(patterns []string) {
	dl.ignorePatterns = patterns
}

// ValidateIgnorePatterns reports the first pattern that is not a valid glob,
// so a typo in configuration fails at startup rather than silently matching
// nothing.
func ValidateIgnorePatterns(patterns []string) error {
	for _, p := range patterns {
		if _, err := filepath.Match(p, "probe"); err != nil {
			return fmt.Errorf("invalid ignore pattern %q: %w", p, err)
		}
	}
	return nil
}

// isIgnored reports whether a file's basename matches any ignore pattern.
func (dl *DataLoader) isIgnored(path string) bool {
	base := filepath.Base(path)
	for _, pattern := range dl.ignorePatterns {
		// A malformed pattern is rejected by ValidateIgnorePatterns at
		// startup; here a match error can only mean "no match".
		if ok, err := filepath.Match(pattern, base); err == nil && ok {
			return true
		}
	}
	return false
}

// filterIgnored drops the files matching an ignore pattern, reporting each one
// so a parked file never goes unnoticed.
func (dl *DataLoader) filterIgnored(files []string) []string {
	if len(dl.ignorePatterns) == 0 {
		return files
	}

	kept := make([]string, 0, len(files))
	for _, f := range files {
		if dl.isIgnored(f) {
			dl.logger.Info("Ignoring %s (matches an ignore pattern; use --include-ignored-files to load it)", f)
			dl.ignoredMu.Lock()
			dl.ignored = append(dl.ignored, f)
			dl.ignoredMu.Unlock()
			continue
		}
		kept = append(kept, f)
	}
	return kept
}

// IgnoredFiles returns the files skipped so far by an ignore pattern. It lets
// the caller warn before a destructive operation: objects declared only in a
// parked file are invisible to this run, so pruning would treat any that this
// controller previously created as orphans.
func (dl *DataLoader) IgnoredFiles() []string {
	dl.ignoredMu.Lock()
	defer dl.ignoredMu.Unlock()
	return append([]string(nil), dl.ignored...)
}

// LoadSites loads site definitions from a folder
func (dl *DataLoader) LoadSites(folder string) ([]*models.Site, error) {
	sites, err := loadKind[*models.Site](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d sites from %s", len(sites), folder)
	return sites, nil
}

// LoadRacks loads rack definitions from a folder
func (dl *DataLoader) LoadRacks(folder string) ([]*models.Rack, error) {
	racks, err := loadKind[*models.Rack](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d racks from %s", len(racks), folder)
	return racks, nil
}

// LoadRoles loads role definitions from a folder
func (dl *DataLoader) LoadRoles(folder string) ([]*models.Role, error) {
	roles, err := loadKind[*models.Role](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d roles from %s", len(roles), folder)
	return roles, nil
}

// LoadTags loads tag definitions from a folder
func (dl *DataLoader) LoadTags(folder string) ([]*models.Tag, error) {
	tags, err := loadKind[*models.Tag](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d tags from %s", len(tags), folder)
	return tags, nil
}

// LoadVLANs loads VLAN definitions from a folder
func (dl *DataLoader) LoadVLANs(folder string) ([]*models.VLAN, error) {
	vlans, err := loadKind[*models.VLAN](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d VLANs from %s", len(vlans), folder)
	return vlans, nil
}

// LoadVLANGroups loads VLAN group definitions from a folder
func (dl *DataLoader) LoadVLANGroups(folder string) ([]*models.VLANGroup, error) {
	groups, err := loadKind[*models.VLANGroup](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d VLAN groups from %s", len(groups), folder)
	return groups, nil
}

// LoadVRFs loads VRF definitions from a folder
func (dl *DataLoader) LoadVRFs(folder string) ([]*models.VRF, error) {
	vrfs, err := loadKind[*models.VRF](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d VRFs from %s", len(vrfs), folder)
	return vrfs, nil
}

// LoadPrefixes loads prefix definitions from a folder
func (dl *DataLoader) LoadPrefixes(folder string) ([]*models.Prefix, error) {
	prefixes, err := loadKind[*models.Prefix](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d prefixes from %s", len(prefixes), folder)
	return prefixes, nil
}

// LoadDeviceTypes loads device type definitions from a folder
func (dl *DataLoader) LoadDeviceTypes(folder string) ([]*models.DeviceType, error) {
	deviceTypes, err := loadKind[*models.DeviceType](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d device types from %s", len(deviceTypes), folder)
	return deviceTypes, nil
}

// LoadModuleTypes loads module type definitions from a folder
func (dl *DataLoader) LoadModuleTypes(folder string) ([]*models.ModuleType, error) {
	moduleTypes, err := loadKind[*models.ModuleType](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d module types from %s", len(moduleTypes), folder)
	return moduleTypes, nil
}

// LoadDevices loads device configurations from a folder
func (dl *DataLoader) LoadDevices(folder string) ([]*models.DeviceConfig, error) {
	devices, err := loadKind[*models.DeviceConfig](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d devices from %s", len(devices), folder)
	return devices, nil
}

// LoadCustomFields loads custom field definitions from a folder
func (dl *DataLoader) LoadCustomFields(folder string) ([]*models.CustomField, error) {
	fields, err := loadKind[*models.CustomField](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d custom fields from %s", len(fields), folder)
	return fields, nil
}

// LoadPlatforms loads platform definitions from a folder
func (dl *DataLoader) LoadPlatforms(folder string) ([]*models.Platform, error) {
	platforms, err := loadKind[*models.Platform](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d platforms from %s", len(platforms), folder)
	return platforms, nil
}

// LoadTenantGroups loads tenant group definitions from a folder
func (dl *DataLoader) LoadTenantGroups(folder string) ([]*models.TenantGroup, error) {
	groups, err := loadKind[*models.TenantGroup](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d tenant groups from %s", len(groups), folder)
	return groups, nil
}

// LoadTenants loads tenant definitions from a folder
func (dl *DataLoader) LoadTenants(folder string) ([]*models.Tenant, error) {
	tenants, err := loadKind[*models.Tenant](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d tenants from %s", len(tenants), folder)
	return tenants, nil
}

// LoadClusterTypes loads cluster type definitions from a folder
func (dl *DataLoader) LoadClusterTypes(folder string) ([]*models.ClusterType, error) {
	clusterTypes, err := loadKind[*models.ClusterType](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d cluster types from %s", len(clusterTypes), folder)
	return clusterTypes, nil
}

// LoadClusterGroups loads cluster group definitions from a folder
func (dl *DataLoader) LoadClusterGroups(folder string) ([]*models.ClusterGroup, error) {
	clusterGroups, err := loadKind[*models.ClusterGroup](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d cluster groups from %s", len(clusterGroups), folder)
	return clusterGroups, nil
}

// LoadClusters loads cluster definitions from a folder
func (dl *DataLoader) LoadClusters(folder string) ([]*models.Cluster, error) {
	clusters, err := loadKind[*models.Cluster](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d clusters from %s", len(clusters), folder)
	return clusters, nil
}

// LoadVMs loads virtual machine configurations from a folder
func (dl *DataLoader) LoadVMs(folder string) ([]*models.VMConfig, error) {
	vms, err := loadKind[*models.VMConfig](dl, folder)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d virtual machines from %s", len(vms), folder)
	return vms, nil
}

// loadKind loads every YAML file of one kind under a folder through the
// node-based decoder (see declaredFromFile) and unwraps the objects, so call
// sites that only need the models are untouched by provenance. Files matching
// an ignore pattern are skipped and recorded, unlike the ingest path, which
// loads them as parked.
func loadKind[T models.Validator](dl *DataLoader, folder string) ([]T, error) {
	targetDir := filepath.Join(dl.basePath, folder)

	// Check if directory exists
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		dl.logger.Warning("Folder %s not found, skipping", folder)
		return nil, nil
	}

	// Find all YAML files recursively
	yamlFiles, err := dl.findYAMLFiles(targetDir)
	if err != nil {
		return nil, fmt.Errorf("failed to find YAML files in %s: %w", targetDir, err)
	}
	yamlFiles = dl.filterIgnored(yamlFiles)

	if len(yamlFiles) == 0 {
		dl.logger.Warning("No YAML files found in %s", folder)
		return nil, nil
	}

	var objects []T
	for _, file := range yamlFiles {
		declared, err := declaredFromFile[T](file, false)
		if err != nil {
			return nil, fmt.Errorf("failed to load %s: %w", file, err)
		}
		for _, d := range declared {
			objects = append(objects, d.Object)
		}
	}
	return objects, nil
}

// mergeDefaults applies file-level defaults to a single item mapping. Item
// values take precedence over defaults; `tags` from both are unioned so a
// device can add tags without dropping the shared ones.
func mergeDefaults(defaults, item map[string]interface{}) map[string]interface{} {
	if len(defaults) == 0 {
		return item
	}
	merged := make(map[string]interface{}, len(defaults)+len(item))
	for key, value := range defaults {
		merged[key] = value
	}
	for key, value := range item {
		if key == "tags" {
			merged[key] = unionTags(defaults[key], value)
			continue
		}
		merged[key] = value
	}
	return merged
}

// unionTags combines two YAML tag lists, keeping default tags first and
// dropping duplicates. Non-list values fall back to the item value.
func unionTags(defaultTags, itemTags interface{}) interface{} {
	defaultList, dOK := defaultTags.([]interface{})
	itemList, iOK := itemTags.([]interface{})
	if !dOK || !iOK {
		return itemTags
	}
	seen := make(map[string]bool, len(defaultList)+len(itemList))
	union := make([]interface{}, 0, len(defaultList)+len(itemList))
	for _, tag := range append(append([]interface{}{}, defaultList...), itemList...) {
		key := fmt.Sprintf("%v", tag)
		if !seen[key] {
			seen[key] = true
			union = append(union, tag)
		}
	}
	return union
}

// findYAMLFiles recursively finds all YAML files in a directory
func (dl *DataLoader) findYAMLFiles(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			ext := filepath.Ext(path)
			if ext == ".yaml" || ext == ".yml" {
				files = append(files, path)
			}
		}

		return nil
	})

	return files, err
}
