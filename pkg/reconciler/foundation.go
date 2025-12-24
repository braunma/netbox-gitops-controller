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
			"status": site.Status,
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
		_, err := fr.client.Apply("dcim", "sites", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile site %s: %w", site.Name, err)
		}
	}

	return nil
}

// ReconcileRacks reconciles rack definitions
func (fr *FoundationReconciler) ReconcileRacks(racks []*models.Rack) error {
	fr.logger.Info("Reconciling %d racks...", len(racks))

	for _, rack := range racks {
		// Get site ID
		siteID, ok := fr.client.Cache().GetID("sites", rack.SiteSlug)
		if !ok {
			fr.logger.Warning("Site %s not found for rack %s, skipping", rack.SiteSlug, rack.Name)
			continue
		}

		payload := map[string]interface{}{
			"name":   rack.Name,
			"site":   siteID,
			"status": rack.Status,
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

		_, err := fr.client.Apply("dcim", "racks", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile rack %s: %w", rack.Name, err)
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

		lookup := map[string]interface{}{"slug": role.Slug}
		_, err := fr.client.Apply("dcim", "device-roles", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile role %s: %w", role.Name, err)
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
		_, err := fr.client.Apply("extras", "tags", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile tag %s: %w", tag.Name, err)
		}
	}

	return nil
}
