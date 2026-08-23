// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
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

		if _, err := ensure(fr.client, "dcim", "sites", ensureSpec{
			kind:        "site",
			name:        site.Name,
			lookup:      map[string]interface{}{"slug": site.Slug},
			renameField: "slug",
			renameFrom:  client.SlugifiedRename(site.RenameFrom),
			payload:     payload,
			// Register so later phases (racks, VLANs, devices) can resolve a
			// site created in this same run, including in --dry-run where it
			// is not written to NetBox.
			register: func(id int) {
				fr.client.Cache().Register("sites", id, site.Slug, site.Name)
			},
		}); err != nil {
			return err
		}
	}

	return nil
}

// ReconcileRacks reconciles rack definitions
func (fr *FoundationReconciler) ReconcileRacks(racks []*models.Rack) error {
	fr.logger.Info("Reconciling %d racks...", len(racks))

	for _, rack := range racks {
		siteID, ok := resolveRef(fr.client, reference{
			app: "dcim", endpoint: "sites", cacheKind: "sites",
			value: rack.SiteSlug,
			kind:  "Site", forKind: "rack", forName: rack.Name,
			consumerApp: "dcim", consumerEndpoint: "racks",
		})
		if !ok {
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

		if _, err := ensure(fr.client, "dcim", "racks", ensureSpec{
			kind: "rack",
			name: rack.Name,
			lookup: map[string]interface{}{
				"site_id": siteID,
				"name":    rack.Name,
			},
			renameField: "name",
			renameFrom:  nonEmpty(rack.RenameFrom),
			payload:     payload,
			// NetBox racks have no slug of their own — they are identified by
			// name within a site — so the YAML slug exists only here. Register
			// it (and the name) site-scoped, or a device's rack_slug resolves
			// nothing and the device is silently created with no rack,
			// position or face.
			register: func(id int) {
				fr.client.Cache().RegisterSite("racks", siteID, id, rack.Slug, rack.Name)
			},
		}); err != nil {
			return err
		}
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

		if _, err := ensure(fr.client, "dcim", "device-roles", ensureSpec{
			kind:        "role",
			name:        role.Name,
			lookup:      map[string]interface{}{"slug": role.Slug},
			renameField: "slug",
			renameFrom:  client.SlugifiedRename(role.RenameFrom),
			payload:     payload,
			// Register so the device phase can resolve a role created this run
			// (the cache otherwise reflects only roles that already existed).
			register: func(id int) {
				fr.client.Cache().Register("roles", id, role.Slug, role.Name)
			},
		}); err != nil {
			return err
		}
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
			mfgID, err := ensureManufacturer(fr.client, platform.Manufacturer)
			if err != nil {
				return err
			}
			payload["manufacturer"] = mfgID
		}

		if _, err := ensure(fr.client, "dcim", "platforms", ensureSpec{
			kind:        "platform",
			name:        platform.Name,
			lookup:      map[string]interface{}{"slug": platform.Slug},
			renameField: "slug",
			renameFrom:  client.SlugifiedRename(platform.RenameFrom),
			payload:     payload,
		}); err != nil {
			return err
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

		if _, err := ensure(fr.client, "tenancy", "tenant-groups", ensureSpec{
			kind:        "tenant group",
			name:        group.Name,
			lookup:      map[string]interface{}{"slug": group.Slug},
			renameField: "slug",
			renameFrom:  client.SlugifiedRename(group.RenameFrom),
			payload:     payload,
		}); err != nil {
			return err
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

		if _, err := ensure(fr.client, "tenancy", "tenants", ensureSpec{
			kind:        "tenant",
			name:        tenant.Name,
			lookup:      map[string]interface{}{"slug": tenant.Slug},
			renameField: "slug",
			renameFrom:  client.SlugifiedRename(tenant.RenameFrom),
			payload:     payload,
		}); err != nil {
			return err
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

		if _, err := ensure(fr.client, "extras", "custom-fields", ensureSpec{
			kind:        "custom field",
			name:        cf.Name,
			lookup:      map[string]interface{}{"name": cf.Name},
			renameField: "name",
			renameFrom:  nonEmpty(cf.RenameFrom),
			payload:     payload,
		}); err != nil {
			return err
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

		if _, err := ensure(fr.client, "extras", "tags", ensureSpec{
			kind:        "tag",
			name:        tag.Name,
			lookup:      map[string]interface{}{"slug": tag.Slug},
			renameField: "slug",
			renameFrom:  client.SlugifiedRename(tag.RenameFrom),
			payload:     payload,
		}); err != nil {
			return err
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
