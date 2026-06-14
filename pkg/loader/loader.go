package loader

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// DataLoader handles loading and validating YAML configuration files
type DataLoader struct {
	basePath string
	logger   *utils.Logger
}

// NewDataLoader creates a new data loader
func NewDataLoader(basePath string, logger *utils.Logger) *DataLoader {
	return &DataLoader{
		basePath: basePath,
		logger:   logger,
	}
}

// LoadSites loads site definitions from a folder
func (dl *DataLoader) LoadSites(folder string) ([]*models.Site, error) {
	var sites []*models.Site
	err := dl.loadFromFolder(folder, &sites)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d sites from %s", len(sites), folder)
	return sites, nil
}

// LoadRacks loads rack definitions from a folder
func (dl *DataLoader) LoadRacks(folder string) ([]*models.Rack, error) {
	var racks []*models.Rack
	err := dl.loadFromFolder(folder, &racks)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d racks from %s", len(racks), folder)
	return racks, nil
}

// LoadRoles loads role definitions from a folder
func (dl *DataLoader) LoadRoles(folder string) ([]*models.Role, error) {
	var roles []*models.Role
	err := dl.loadFromFolder(folder, &roles)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d roles from %s", len(roles), folder)
	return roles, nil
}

// LoadTags loads tag definitions from a folder
func (dl *DataLoader) LoadTags(folder string) ([]*models.Tag, error) {
	var tags []*models.Tag
	err := dl.loadFromFolder(folder, &tags)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d tags from %s", len(tags), folder)
	return tags, nil
}

// LoadVLANs loads VLAN definitions from a folder
func (dl *DataLoader) LoadVLANs(folder string) ([]*models.VLAN, error) {
	var vlans []*models.VLAN
	err := dl.loadFromFolder(folder, &vlans)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d VLANs from %s", len(vlans), folder)
	return vlans, nil
}

// LoadVLANGroups loads VLAN group definitions from a folder
func (dl *DataLoader) LoadVLANGroups(folder string) ([]*models.VLANGroup, error) {
	var groups []*models.VLANGroup
	err := dl.loadFromFolder(folder, &groups)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d VLAN groups from %s", len(groups), folder)
	return groups, nil
}

// LoadVRFs loads VRF definitions from a folder
func (dl *DataLoader) LoadVRFs(folder string) ([]*models.VRF, error) {
	var vrfs []*models.VRF
	err := dl.loadFromFolder(folder, &vrfs)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d VRFs from %s", len(vrfs), folder)
	return vrfs, nil
}

// LoadPrefixes loads prefix definitions from a folder
func (dl *DataLoader) LoadPrefixes(folder string) ([]*models.Prefix, error) {
	var prefixes []*models.Prefix
	err := dl.loadFromFolder(folder, &prefixes)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d prefixes from %s", len(prefixes), folder)
	return prefixes, nil
}

// LoadDeviceTypes loads device type definitions from a folder
func (dl *DataLoader) LoadDeviceTypes(folder string) ([]*models.DeviceType, error) {
	var deviceTypes []*models.DeviceType
	err := dl.loadFromFolder(folder, &deviceTypes)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d device types from %s", len(deviceTypes), folder)
	return deviceTypes, nil
}

// LoadModuleTypes loads module type definitions from a folder
func (dl *DataLoader) LoadModuleTypes(folder string) ([]*models.ModuleType, error) {
	var moduleTypes []*models.ModuleType
	err := dl.loadFromFolder(folder, &moduleTypes)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d module types from %s", len(moduleTypes), folder)
	return moduleTypes, nil
}

// LoadDevices loads device configurations from a folder
func (dl *DataLoader) LoadDevices(folder string) ([]*models.DeviceConfig, error) {
	var devices []*models.DeviceConfig
	err := dl.loadFromFolder(folder, &devices)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d devices from %s", len(devices), folder)
	return devices, nil
}

// LoadCustomFields loads custom field definitions from a folder
func (dl *DataLoader) LoadCustomFields(folder string) ([]*models.CustomField, error) {
	var fields []*models.CustomField
	err := dl.loadFromFolder(folder, &fields)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d custom fields from %s", len(fields), folder)
	return fields, nil
}

// LoadPlatforms loads platform definitions from a folder
func (dl *DataLoader) LoadPlatforms(folder string) ([]*models.Platform, error) {
	var platforms []*models.Platform
	err := dl.loadFromFolder(folder, &platforms)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d platforms from %s", len(platforms), folder)
	return platforms, nil
}

// LoadTenantGroups loads tenant group definitions from a folder
func (dl *DataLoader) LoadTenantGroups(folder string) ([]*models.TenantGroup, error) {
	var groups []*models.TenantGroup
	err := dl.loadFromFolder(folder, &groups)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d tenant groups from %s", len(groups), folder)
	return groups, nil
}

// LoadTenants loads tenant definitions from a folder
func (dl *DataLoader) LoadTenants(folder string) ([]*models.Tenant, error) {
	var tenants []*models.Tenant
	err := dl.loadFromFolder(folder, &tenants)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d tenants from %s", len(tenants), folder)
	return tenants, nil
}

// LoadClusterTypes loads cluster type definitions from a folder
func (dl *DataLoader) LoadClusterTypes(folder string) ([]*models.ClusterType, error) {
	var clusterTypes []*models.ClusterType
	err := dl.loadFromFolder(folder, &clusterTypes)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d cluster types from %s", len(clusterTypes), folder)
	return clusterTypes, nil
}

// LoadClusterGroups loads cluster group definitions from a folder
func (dl *DataLoader) LoadClusterGroups(folder string) ([]*models.ClusterGroup, error) {
	var clusterGroups []*models.ClusterGroup
	err := dl.loadFromFolder(folder, &clusterGroups)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d cluster groups from %s", len(clusterGroups), folder)
	return clusterGroups, nil
}

// LoadClusters loads cluster definitions from a folder
func (dl *DataLoader) LoadClusters(folder string) ([]*models.Cluster, error) {
	var clusters []*models.Cluster
	err := dl.loadFromFolder(folder, &clusters)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d clusters from %s", len(clusters), folder)
	return clusters, nil
}

// LoadVMs loads virtual machine configurations from a folder
func (dl *DataLoader) LoadVMs(folder string) ([]*models.VMConfig, error) {
	var vms []*models.VMConfig
	err := dl.loadFromFolder(folder, &vms)
	if err != nil {
		return nil, err
	}
	dl.logger.Debug("Loaded %d virtual machines from %s", len(vms), folder)
	return vms, nil
}

// loadFromFolder loads YAML files from a folder and unmarshals into the target
func (dl *DataLoader) loadFromFolder(folder string, target interface{}) error {
	targetDir := filepath.Join(dl.basePath, folder)

	// Check if directory exists
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		dl.logger.Warning("Folder %s not found, skipping", folder)
		return nil
	}

	// Find all YAML files recursively
	yamlFiles, err := dl.findYAMLFiles(targetDir)
	if err != nil {
		return fmt.Errorf("failed to find YAML files in %s: %w", targetDir, err)
	}

	if len(yamlFiles) == 0 {
		dl.logger.Warning("No YAML files found in %s", folder)
		return nil
	}

	// Load each file
	for _, file := range yamlFiles {
		if err := dl.loadFile(file, target); err != nil {
			return fmt.Errorf("failed to load %s: %w", file, err)
		}
	}

	return nil
}

// validateItems runs model validation on every item loaded from a file.
// All failures in a file are aggregated so the user sees them at once
// instead of fixing them one sync run at a time.
func validateItems[T models.Validator](path string, items []T) error {
	var errs []error
	for i, item := range items {
		if err := item.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("%s: entry %d: %w", path, i+1, err))
		}
	}
	return errors.Join(errs...)
}

// loadFile loads a single YAML file and appends items to target
func (dl *DataLoader) loadFile(path string, target interface{}) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Unmarshal YAML - usually a list, but accept a single top-level mapping
	// as a one-element list (e.g. one device type per file)
	var items []map[string]interface{}
	if err := yaml.Unmarshal(content, &items); err != nil {
		var single map[string]interface{}
		if err2 := yaml.Unmarshal(content, &single); err2 != nil {
			return fmt.Errorf("failed to unmarshal YAML: %w", err)
		}
		items = []map[string]interface{}{single}
	}

	// Get current target slice and append items from this file
	// We need to use reflection to append to the slice properly
	switch t := target.(type) {
	case *[]*models.Site:
		var newItems []*models.Site
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal sites: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.Rack:
		var newItems []*models.Rack
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal racks: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.Role:
		var newItems []*models.Role
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal roles: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.Tag:
		var newItems []*models.Tag
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal tags: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.VLAN:
		var newItems []*models.VLAN
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal vlans: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.VLANGroup:
		var newItems []*models.VLANGroup
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal vlan groups: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.VRF:
		var newItems []*models.VRF
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal vrfs: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.Prefix:
		var newItems []*models.Prefix
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal prefixes: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.DeviceType:
		var newItems []*models.DeviceType
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal device types: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.ModuleType:
		var newItems []*models.ModuleType
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal module types: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.DeviceConfig:
		var newItems []*models.DeviceConfig
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal devices: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.CustomField:
		var newItems []*models.CustomField
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal custom fields: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.Platform:
		var newItems []*models.Platform
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal platforms: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.TenantGroup:
		var newItems []*models.TenantGroup
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal tenant groups: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.Tenant:
		var newItems []*models.Tenant
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal tenants: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.ClusterType:
		var newItems []*models.ClusterType
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal cluster types: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.ClusterGroup:
		var newItems []*models.ClusterGroup
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal cluster groups: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.Cluster:
		var newItems []*models.Cluster
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal clusters: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	case *[]*models.VMConfig:
		var newItems []*models.VMConfig
		data, _ := yaml.Marshal(items)
		if err := yaml.Unmarshal(data, &newItems); err != nil {
			return fmt.Errorf("failed to unmarshal virtual machines: %w", err)
		}
		if err := validateItems(path, newItems); err != nil {
			return err
		}
		*t = append(*t, newItems...)
	default:
		return fmt.Errorf("unsupported target type: %T", target)
	}

	return nil
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
