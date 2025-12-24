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
		if err := cm.loadResource(resource, path, nil); err != nil {
			return fmt.Errorf("failed to load %s: %w", resource, err)
		}
	}

	cm.client.logger.Success("Global caches loaded")
	return nil
}

// LoadSite loads site-specific resources
func (cm *CacheManager) LoadSite(siteSlug string) error {
	cm.client.logger.Info("Reloading cache for site: %s", siteSlug)

	// Find site ID
	siteID, ok := cm.GetID("sites", siteSlug)
	if !ok {
		// Try loading sites first
		if err := cm.loadResource("sites", "dcim/sites", nil); err != nil {
			return fmt.Errorf("failed to load sites: %w", err)
		}
		siteID, ok = cm.GetID("sites", siteSlug)
		if !ok {
			return fmt.Errorf("site %s not found", siteSlug)
		}
	}

	cm.client.logger.Debug("Found Site: %s (ID: %d)", siteSlug, siteID)

	// Load site-specific resources
	resources := map[string]string{
		"vlans": "ipam/vlans",
		"racks": "dcim/racks",
	}

	for resource, path := range resources {
		filters := map[string]interface{}{"site_id": siteID}
		if err := cm.loadResource(resource, path, filters); err != nil {
			return fmt.Errorf("failed to load %s: %w", resource, err)
		}
	}

	return nil
}

// loadResource loads a specific resource into cache
func (cm *CacheManager) loadResource(resource, path string, filters map[string]interface{}) error {
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

		// Index by slug
		if slug, ok := obj["slug"].(string); ok {
			cm.cache[resource][slug] = id
		}

		// Index by name/model
		if name, ok := obj["name"].(string); ok {
			cm.cache[resource][name] = id
		} else if model, ok := obj["model"].(string); ok {
			cm.cache[resource][model] = id
		} else if label, ok := obj["label"].(string); ok {
			cm.cache[resource][label] = id
		}
	}

	return nil
}

// GetID retrieves an ID from the cache
func (cm *CacheManager) GetID(resource, identifier string) (int, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if cm.cache[resource] == nil {
		return 0, false
	}

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
