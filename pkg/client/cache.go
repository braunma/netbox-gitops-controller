package client

import (
	"fmt"
	"sync"

	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// CacheManager handles caching of NetBox objects
type CacheManager struct {
	client *NetBoxClient
	cache  map[string]map[string]int
	mu     sync.RWMutex
}

// NewCacheManager creates a new cache manager
func NewCacheManager(client *NetBoxClient) *CacheManager {
	return &CacheManager{
		client: client,
		cache:  make(map[string]map[string]int),
	}
}

// LoadGlobal loads global resources (not site-specific)
func (cm *CacheManager) LoadGlobal() error {
	cm.client.logger.Info("Loading global caches...")

	resources := map[string]string{
		"device_types":  "dcim/device-types",
		"module_types":  "dcim/module-types",
		"roles":         "dcim/device-roles",
		"manufacturers": "dcim/manufacturers",
		"sites":         "dcim/sites",
		"vrfs":          "ipam/vrfs",
	}

	for resource, path := range resources {
		cm.client.logger.Debug("→ %s", resource)
		// Pass siteID=0 for global resources (no site prefix)
		if err := cm.loadResource(resource, path, nil, 0); err != nil {
			return fmt.Errorf("failed to load %s: %w", resource, err)
		}
	}

	cm.client.logger.Success("Global caches loaded")
	return nil
}

// LoadSite loads site-specific resources with composite keys
func (cm *CacheManager) LoadSite(siteSlug string) error {
	cm.client.logger.Info("Reloading cache for site: %s", siteSlug)

	// Find site ID
	siteID, ok := cm.GetID("sites", siteSlug)
	if !ok {
		// Try loading sites first
		if err := cm.loadResource("sites", "dcim/sites", nil, 0); err != nil {
			return fmt.Errorf("failed to load sites: %w", err)
		}
		siteID, ok = cm.GetID("sites", siteSlug)
		if !ok {
			return fmt.Errorf("site %s not found", siteSlug)
		}
	}

	cm.client.logger.Debug("Found Site: %s (ID: %d)", siteSlug, siteID)

	// Load site-specific resources with composite keys
	resources := map[string]string{
		"vlans":       "ipam/vlans",
		"racks":       "dcim/racks",
		"vlan_groups": "ipam/vlan-groups", // Can be site-specific or global
	}

	for resource, path := range resources {
		filters := map[string]interface{}{"site_id": siteID}
		// Pass siteID to create composite keys
		if err := cm.loadResource(resource, path, filters, siteID); err != nil {
			return fmt.Errorf("failed to load %s: %w", resource, err)
		}
	}

	// Also load global VLAN groups (those without site_id)
	if err := cm.loadResource("vlan_groups", "ipam/vlan-groups", map[string]interface{}{
		"site_id": "null", // NetBox filter for null site
	}, 0); err != nil {
		cm.client.logger.Warning("Failed to load global VLAN groups: %v", err)
		// Don't fail - this is not critical
	}

	return nil
}

// loadResource loads a specific resource into cache
// If siteID > 0, creates composite keys: "site-{siteID}:{identifier}"
func (cm *CacheManager) loadResource(resource, path string, filters map[string]interface{}, siteID int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.cache[resource] == nil {
		cm.cache[resource] = make(map[string]int)
	}

	// Parse app and endpoint from path
	app := ""
	endpoint := ""
	if len(path) > 0 {
		parts := splitPath(path)
		if len(parts) == 2 {
			app = parts[0]
			endpoint = parts[1]
		}
	}

	if app == "" || endpoint == "" {
		return fmt.Errorf("invalid path: %s", path)
	}

	objects, err := cm.client.Filter(app, endpoint, filters)
	if err != nil {
		return fmt.Errorf("failed to filter %s: %w", resource, err)
	}

	for _, obj := range objects {
		id := utils.GetIDFromObject(obj)
		if id == 0 {
			continue
		}

		// Helper to store with optional site prefix
		storeKey := func(identifier string) {
			if siteID > 0 {
				// Site-specific resource: use composite key
				compositeKey := fmt.Sprintf("site-%d:%s", siteID, identifier)
				cm.cache[resource][compositeKey] = id
			} else {
				// Global resource: use simple key
				cm.cache[resource][identifier] = id
			}
		}

		// Index by slug
		if slug, ok := obj["slug"].(string); ok {
			storeKey(slug)
		}

		// Index by name/model
		if name, ok := obj["name"].(string); ok {
			storeKey(name)
		} else if model, ok := obj["model"].(string); ok {
			storeKey(model)
		} else if label, ok := obj["label"].(string); ok {
			storeKey(label)
		}
	}

	return nil
}

// GetID retrieves an ID from the cache (legacy method, use GetGlobalID or GetSiteID instead)
func (cm *CacheManager) GetID(resource, identifier string) (int, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if cm.cache[resource] == nil {
		return 0, false
	}

	id, ok := cm.cache[resource][identifier]
	return id, ok
}

// GetGlobalID retrieves an ID for a global resource (not site-specific)
// Use this for: device_types, module_types, roles, manufacturers, sites, vrfs
func (cm *CacheManager) GetGlobalID(resource, identifier string) (int, bool) {
	return cm.GetID(resource, identifier)
}

// GetSiteID retrieves an ID for a site-specific resource using composite key
// Use this for: vlans, racks, and other site-scoped resources
// Key format: "site-{siteID}:{identifier}"
func (cm *CacheManager) GetSiteID(resource string, siteID int, identifier string) (int, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if cm.cache[resource] == nil {
		return 0, false
	}

	// Try composite key first (new format)
	compositeKey := fmt.Sprintf("site-%d:%s", siteID, identifier)
	if id, ok := cm.cache[resource][compositeKey]; ok {
		return id, true
	}

	// Fallback to simple key for backwards compatibility (will be removed later)
	id, ok := cm.cache[resource][identifier]
	return id, ok
}

// Invalidate clears the cache for a specific resource
func (cm *CacheManager) Invalidate(resource string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	delete(cm.cache, resource)
}

// InvalidateAll clears all caches
func (cm *CacheManager) InvalidateAll() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.cache = make(map[string]map[string]int)
}

// Resources returns a list of cached resources
func (cm *CacheManager) Resources() []string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	resources := make([]string, 0, len(cm.cache))
	for resource := range cm.cache {
		resources = append(resources, resource)
	}
	return resources
}

// Size returns the number of cached items for a resource
func (cm *CacheManager) Size(resource string) int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if cm.cache[resource] == nil {
		return 0
	}

	return len(cm.cache[resource])
}

// splitPath splits a path like "dcim/sites" into ["dcim", "sites"]
func splitPath(path string) []string {
	var parts []string
	current := ""
	for _, char := range path {
		if char == '/' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(char)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}
