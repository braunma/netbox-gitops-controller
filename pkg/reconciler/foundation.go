// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"fmt"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// FoundationReconciler handles foundation resources (sites, racks, roles, tags)
type FoundationReconciler struct {
	client *client.NetBoxClient
	logger *utils.Logger
}

// NewFoundationReconciler creates a new foundation reconciler
func NewFoundationReconciler(c *client.NetBoxClient) *FoundationReconciler {
	return &FoundationReconciler{
		client: c,
		logger: c.Logger(),
	}
}

// ReconcileSites reconciles site definitions
func (fr *FoundationReconciler) ReconcileSites(sites []*models.Site) error {
	fr.logger.Info("Reconciling %d sites...", len(sites))

	for _, site := range sites {
		payload := map[string]interface{}{
			"name":   site.Name,
			"slug":   site.Slug,
			"status": defaultStatus(site.Status),
		}

		if site.Region != "" {
			payload["region"] = site.Region
		}
		if site.TimeZone != "" {
			payload["time_zone"] = site.TimeZone
		}
		if site.Description != "" {
			payload["description"] = site.Description
		}
		if site.Comments != "" {
			payload["comments"] = site.Comments
		}

		lookup := map[string]interface{}{"slug": site.Slug}
		lookup, err := fr.client.RenamedLookup("dcim", "sites", site.Name, lookup, "slug", client.SlugifiedRename(site.RenameFrom))
		if err != nil {
			return err
		}
		siteObj, err := fr.client.Apply("dcim", "sites", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile site %s: %w", site.Name, err)
		}
		// Register so later phases (racks, VLANs, devices) can resolve a site
		// created in this same run, including in --dry-run where it is not
		// written to NetBox.
		fr.client.Cache().Register("sites", utils.GetIDFromObject(siteObj), site.Slug, site.Name)
	}

	return nil
}

// ReconcileRacks reconciles rack definitions
func (fr *FoundationReconciler) ReconcileRacks(racks []*models.Rack) error {
	fr.logger.Info("Reconciling %d racks...", len(racks))

	for _, rack := range racks {
		// Get site ID using LIVE lookup (not cache)
		// This is critical because the site might have been just created and not in cache yet
		sites, err := fr.client.Filter("dcim", "sites", map[string]interface{}{
			"slug": rack.SiteSlug,
		})
		if err != nil || len(sites) == 0 {
			// Fallback: Try by name
			sites, err = fr.client.Filter("dcim", "sites", map[string]interface{}{
				"name": rack.SiteSlug,
			})
		}

		if err != nil || len(sites) == 0 {
			fr.logger.Warning("Site %s not found for rack %s, skipping", rack.SiteSlug, rack.Name)
			fr.client.MarkReconcileIncomplete("dcim", "racks")
			continue
		}

		siteID := utils.GetIDFromObject(sites[0])
		if siteID == 0 {
			fr.logger.Warning("Site %s has invalid ID for rack %s, skipping", rack.SiteSlug, rack.Name)
			fr.client.MarkReconcileIncomplete("dcim", "racks")
			continue
		}

		payload := map[string]interface{}{
			"name":   rack.Name,
			"site":   siteID,
			"status": defaultStatus(rack.Status),
		}

		if rack.Width > 0 {
			payload["width"] = rack.Width
		}
		if rack.UHeight > 0 {
			payload["u_height"] = rack.UHeight
		}
		if rack.Description != "" {
			payload["description"] = rack.Description
		}

		lookup := map[string]interface{}{
			"site_id": siteID,
			"name":    rack.Name,
		}
		lookup, err = fr.client.RenamedLookup("dcim", "racks", rack.Name, lookup, "name", nonEmpty(rack.RenameFrom))
		if err != nil {
			return err
		}

		rackObj, err := fr.client.Apply("dcim", "racks", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile rack %s: %w", rack.Name, err)
		}
		// NetBox racks have no slug of their own — they are identified by name
		// within a site — so the YAML slug exists only here. Register it (and
		// the name) site-scoped, or a device's rack_slug resolves nothing and
		// the device is silently created with no rack, position or face.
		fr.client.Cache().RegisterSite("racks", siteID, utils.GetIDFromObject(rackObj), rack.Slug, rack.Name)
	}

	return nil
}

// ReconcileRoles reconciles role definitions
func (fr *FoundationReconciler) ReconcileRoles(roles []*models.Role) error {
	fr.logger.Info("Reconciling %d roles...", len(roles))

	for _, role := range roles {
		payload := map[string]interface{}{
			"name":        role.Name,
			"slug":        role.Slug,
			"color":       utils.NormalizeColor(role.Color),
			"vm_role":     role.VMRole,
			"description": role.Description,
		}

		lookup := map[string]interface{}{"slug": role.Slug}
		lookup, err := fr.client.RenamedLookup("dcim", "device-roles", role.Name, lookup, "slug", client.SlugifiedRename(role.RenameFrom))
		if err != nil {
			return err
		}
		roleObj, err := fr.client.Apply("dcim", "device-roles", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile role %s: %w", role.Name, err)
		}
		// Register so the device phase can resolve a role created this run
		// (the cache otherwise reflects only roles that already existed).
		fr.client.Cache().Register("roles", utils.GetIDFromObject(roleObj), role.Slug, role.Name)
	}

	return nil
}

// ReconcilePlatforms reconciles platform definitions. The optional
// manufacturer is resolved from the global cache and auto-created on a miss,
// mirroring the device/module type reconcilers.
func (fr *FoundationReconciler) ReconcilePlatforms(platforms []*models.Platform) error {
	fr.logger.Info("Reconciling %d platforms...", len(platforms))

	for _, platform := range platforms {
		payload := map[string]interface{}{
			"name": platform.Name,
			"slug": platform.Slug,
		}
		if platform.Description != "" {
			payload["description"] = platform.Description
		}

		if platform.Manufacturer != "" {
			mfgID, ok := fr.client.Cache().GetGlobalID("manufacturers", platform.Manufacturer)
			if !ok {
				mfgObj, err := fr.client.Apply("dcim", "manufacturers",
					map[string]interface{}{"slug": utils.Slugify(platform.Manufacturer)},
					map[string]interface{}{
						"name": platform.Manufacturer,
						"slug": utils.Slugify(platform.Manufacturer),
					})
				if err != nil {
					return fmt.Errorf("failed to create manufacturer %s: %w", platform.Manufacturer, err)
				}
				mfgID = utils.GetIDFromObject(mfgObj)
				fr.client.Cache().Register("manufacturers", mfgID, utils.Slugify(platform.Manufacturer), platform.Manufacturer)
			}
			payload["manufacturer"] = mfgID
		}

		lookup := map[string]interface{}{"slug": platform.Slug}
		lookup, err := fr.client.RenamedLookup("dcim", "platforms", platform.Name, lookup, "slug", client.SlugifiedRename(platform.RenameFrom))
		if err != nil {
			return err
		}
		_, err = fr.client.Apply("dcim", "platforms", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile platform %s: %w", platform.Name, err)
		}
	}

	return nil
}

// ReconcileTenantGroups reconciles tenant group definitions. A parent group
// may have been created earlier in this same run, so it is resolved with a
// live lookup rather than the cache.
func (fr *FoundationReconciler) ReconcileTenantGroups(groups []*models.TenantGroup) error {
	fr.logger.Info("Reconciling %d tenant groups...", len(groups))

	for _, group := range groups {
		payload := map[string]interface{}{
			"name": group.Name,
			"slug": group.Slug,
		}
		if group.Description != "" {
			payload["description"] = group.Description
		}

		if group.ParentSlug != "" {
			if parentID, ok := fr.lookupTenantGroupID(group.ParentSlug); ok {
				payload["parent"] = parentID
			} else {
				fr.logger.Warning("Parent tenant group %s not found for %s, leaving unset", group.ParentSlug, group.Name)
			}
		}

		lookup := map[string]interface{}{"slug": group.Slug}
		lookup, err := fr.client.RenamedLookup("tenancy", "tenant-groups", group.Name, lookup, "slug", client.SlugifiedRename(group.RenameFrom))
		if err != nil {
			return err
		}
		_, err = fr.client.Apply("tenancy", "tenant-groups", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile tenant group %s: %w", group.Name, err)
		}
	}

	return nil
}

// ReconcileTenants reconciles tenant definitions. The optional group is
// resolved with a live lookup, since it may have been created in this run by
// ReconcileTenantGroups (which must run first).
func (fr *FoundationReconciler) ReconcileTenants(tenants []*models.Tenant) error {
	fr.logger.Info("Reconciling %d tenants...", len(tenants))

	for _, tenant := range tenants {
		payload := map[string]interface{}{
			"name": tenant.Name,
			"slug": tenant.Slug,
		}
		if tenant.Description != "" {
			payload["description"] = tenant.Description
		}

		if tenant.GroupSlug != "" {
			if groupID, ok := fr.lookupTenantGroupID(tenant.GroupSlug); ok {
				payload["group"] = groupID
			} else {
				fr.logger.Warning("Tenant group %s not found for tenant %s, leaving unset", tenant.GroupSlug, tenant.Name)
			}
		}

		lookup := map[string]interface{}{"slug": tenant.Slug}
		lookup, err := fr.client.RenamedLookup("tenancy", "tenants", tenant.Name, lookup, "slug", client.SlugifiedRename(tenant.RenameFrom))
		if err != nil {
			return err
		}
		_, err = fr.client.Apply("tenancy", "tenants", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile tenant %s: %w", tenant.Name, err)
		}
	}

	return nil
}

// lookupTenantGroupID resolves a tenant group slug to its ID with a live API
// lookup, so groups created earlier in the same run are found.
func (fr *FoundationReconciler) lookupTenantGroupID(slug string) (int, bool) {
	groups, err := fr.client.Filter("tenancy", "tenant-groups", map[string]interface{}{"slug": slug})
	if err != nil || len(groups) == 0 {
		return 0, false
	}
	id := utils.GetIDFromObject(groups[0])
	return id, id != 0
}

// ReconcileCustomFields reconciles custom field definitions. Custom fields are
// not taggable, so Apply skips managed-tag injection for this endpoint (see
// constants.UntaggableEndpoints); they are consequently not pruned either.
// The field is looked up by its unique name.
func (fr *FoundationReconciler) ReconcileCustomFields(fields []*models.CustomField) error {
	fr.logger.Info("Reconciling %d custom fields...", len(fields))

	for _, cf := range fields {
		payload := map[string]interface{}{
			"name":         cf.Name,
			"type":         cf.Type,
			"object_types": cf.ObjectTypes,
			"required":     cf.Required,
		}
		if cf.Label != "" {
			payload["label"] = cf.Label
		}
		if cf.Description != "" {
			payload["description"] = cf.Description
		}

		lookup := map[string]interface{}{"name": cf.Name}
		lookup, err := fr.client.RenamedLookup("extras", "custom-fields", cf.Name, lookup, "name", nonEmpty(cf.RenameFrom))
		if err != nil {
			return err
		}
		if _, err := fr.client.Apply("extras", "custom-fields", lookup, payload); err != nil {
			return fmt.Errorf("failed to reconcile custom field %s: %w", cf.Name, err)
		}
	}

	return nil
}

// ReconcileTags reconciles tag definitions
func (fr *FoundationReconciler) ReconcileTags(tags []*models.Tag) error {
	fr.logger.Info("Reconciling %d tags...", len(tags))

	for _, tag := range tags {
		payload := map[string]interface{}{
			"name":        tag.Name,
			"slug":        tag.Slug,
			"color":       utils.NormalizeColor(tag.Color),
			"description": tag.Description,
		}

		lookup := map[string]interface{}{"slug": tag.Slug}
		lookup, err := fr.client.RenamedLookup("extras", "tags", tag.Name, lookup, "slug", client.SlugifiedRename(tag.RenameFrom))
		if err != nil {
			return err
		}
		_, err = fr.client.Apply("extras", "tags", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile tag %s: %w", tag.Name, err)
		}
	}

	return nil
}

// defaultStatus returns NetBox's default status when a declaration leaves it
// unset. NetBox rejects an empty status with "This field may not be blank"
// rather than applying its own default, so an object whose YAML simply omits
// the field would fail the whole run.
func defaultStatus(status string) string {
	if status == "" {
		return "active"
	}
	return status
}
