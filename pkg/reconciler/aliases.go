// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// A rack and a module type have a slug only in the YAML: NetBox identifies a
// rack by its name within a site and a module type by manufacturer and model.
// The phases that reconcile them register the YAML slug in the cache as they
// go. When such a phase does not run — a change-based sync in which nothing
// of it changed, or --only — the functions below register the same slugs
// from what NetBox already holds, so that a device's `rack` and a module's
// `module_type` still resolve. Without them the device phase finds neither
// and leaves the rack or the module out.
//
// Both only add keys that are missing, so calling them after the phase did
// run changes nothing, and an object NetBox does not hold yet stays unknown.

// RegisterModuleTypeAliases makes the declared module type slugs resolvable.
// It needs the global caches.
func RegisterModuleTypeAliases(c *client.NetBoxClient, moduleTypes []*models.ModuleType) {
	cache := c.Cache()
	ambiguous := ambiguousModuleTypeSlugs(moduleTypes)
	for _, mt := range moduleTypes {
		// The cache was loaded with "<manufacturer slug>/<slugified model>"
		// for every module type; the bare model is the fallback for a
		// manufacturer whose slug was not derived from its name.
		id, ok := cache.GetGlobalID("module_types", utils.Slugify(mt.Manufacturer)+"/"+utils.Slugify(mt.Model))
		if !ok {
			if _, clash := ambiguous[mt.Slug]; clash {
				continue
			}
			if id, ok = cache.GetGlobalID("module_types", mt.Model); !ok {
				continue
			}
		}
		identifiers := []string{qualifiedModuleTypeSlug(mt.Manufacturer, mt.Slug)}
		if _, clash := ambiguous[mt.Slug]; !clash {
			identifiers = append(identifiers, mt.Slug)
		}
		for _, identifier := range identifiers {
			if identifier == "" {
				continue
			}
			if _, known := cache.GetGlobalID("module_types", identifier); !known {
				cache.Register("module_types", id, identifier)
			}
		}
	}
}

// RegisterRackAliases makes the declared rack slugs resolvable for the sites
// whose caches are loaded. Call it after CacheManager.LoadSite.
func RegisterRackAliases(c *client.NetBoxClient, racks []*models.Rack) {
	cache := c.Cache()
	for _, rack := range racks {
		if rack.Slug == "" {
			continue
		}
		siteID, ok := cache.GetGlobalID("sites", rack.SiteSlug)
		if !ok || siteID == 0 {
			continue
		}
		if _, known := cache.GetSiteID("racks", siteID, rack.Slug); known {
			continue
		}
		if id, ok := cache.GetSiteID("racks", siteID, rack.Name); ok {
			cache.RegisterSite("racks", siteID, id, rack.Slug)
		}
	}
}
