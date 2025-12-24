package reconciler

import (
	"fmt"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// NetworkReconciler handles network resources (VLANs, VRFs, Prefixes)
type NetworkReconciler struct {
	client *client.NetBoxClient
	logger *utils.Logger
}

// NewNetworkReconciler creates a new network reconciler
func NewNetworkReconciler(c *client.NetBoxClient) *NetworkReconciler {
	return &NetworkReconciler{
		client: c,
		logger: c.Logger(),
	}
}

// ReconcileVRFs reconciles VRF definitions
func (nr *NetworkReconciler) ReconcileVRFs(vrfs []*models.VRF) error {
	nr.logger.Info("Reconciling %d VRFs...", len(vrfs))

	for _, vrf := range vrfs {
		payload := map[string]interface{}{
			"name":           vrf.Name,
			"enforce_unique": vrf.EnforceUnique,
		}

		if vrf.RD != "" {
			payload["rd"] = vrf.RD
		}
		if vrf.Description != "" {
			payload["description"] = vrf.Description
		}

		lookup := map[string]interface{}{"name": vrf.Name}
		_, err := nr.client.Apply("ipam", "vrfs", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile VRF %s: %w", vrf.Name, err)
		}
	}

	return nil
}

// ReconcileVLANGroups reconciles VLAN group definitions
func (nr *NetworkReconciler) ReconcileVLANGroups(groups []*models.VLANGroup) error {
	nr.logger.Info("Reconciling %d VLAN groups...", len(groups))

	for _, group := range groups {
		payload := map[string]interface{}{
			"name": group.Name,
			"slug": group.Slug,
		}

		if group.SiteSlug != "" {
			siteID, ok := nr.client.Cache().GetID("sites", group.SiteSlug)
			if ok {
				payload["site"] = siteID
			}
		}

		if group.Description != "" {
			payload["description"] = group.Description
		}
		if group.MinVID > 0 {
			payload["min_vid"] = group.MinVID
		}
		if group.MaxVID > 0 {
			payload["max_vid"] = group.MaxVID
		}

		lookup := map[string]interface{}{"slug": group.Slug}
		_, err := nr.client.Apply("ipam", "vlan-groups", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile VLAN group %s: %w", group.Name, err)
		}
	}

	return nil
}

// ReconcileVLANs reconciles VLAN definitions
func (nr *NetworkReconciler) ReconcileVLANs(vlans []*models.VLAN) error {
	nr.logger.Info("Reconciling %d VLANs...", len(vlans))

	for _, vlan := range vlans {
		// Get site ID
		siteID, ok := nr.client.Cache().GetID("sites", vlan.SiteSlug)
		if !ok {
			nr.logger.Warning("Site %s not found for VLAN %s, skipping", vlan.SiteSlug, vlan.Name)
			continue
		}

		payload := map[string]interface{}{
			"name":   vlan.Name,
			"vid":    vlan.VID,
			"site":   siteID,
			"status": vlan.Status,
		}

		if vlan.GroupSlug != "" {
			groupID, ok := nr.client.Cache().GetID("vlan_groups", vlan.GroupSlug)
			if ok {
				payload["group"] = groupID
			}
		}

		if vlan.Role != "" {
			payload["role"] = vlan.Role
		}
		if vlan.Description != "" {
			payload["description"] = vlan.Description
		}

		lookup := map[string]interface{}{
			"site_id": siteID,
			"vid":     vlan.VID,
		}

		_, err := nr.client.Apply("ipam", "vlans", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile VLAN %s: %w", vlan.Name, err)
		}
	}

	return nil
}

// ReconcilePrefixes reconciles prefix definitions
func (nr *NetworkReconciler) ReconcilePrefixes(prefixes []*models.Prefix) error {
	nr.logger.Info("Reconciling %d prefixes...", len(prefixes))

	for _, prefix := range prefixes {
		payload := map[string]interface{}{
			"prefix": prefix.Prefix,
			"status": prefix.Status,
			"is_pool": prefix.IsPool,
		}

		if prefix.SiteSlug != "" {
			siteID, ok := nr.client.Cache().GetID("sites", prefix.SiteSlug)
			if ok {
				payload["site"] = siteID
			}
		}

		if prefix.VRFName != "" {
			vrfID, ok := nr.client.Cache().GetID("vrfs", prefix.VRFName)
			if ok {
				payload["vrf"] = vrfID
			}
		}

		if prefix.VLANName != "" {
			// Need to find VLAN by name
			// This is a simplified lookup - in production you'd need site context
			vlanID, ok := nr.client.Cache().GetID("vlans", prefix.VLANName)
			if ok {
				payload["vlan"] = vlanID
			}
		}

		if prefix.Role != "" {
			payload["role"] = prefix.Role
		}
		if prefix.Description != "" {
			payload["description"] = prefix.Description
		}

		lookup := map[string]interface{}{"prefix": prefix.Prefix}
		if prefix.VRFName != "" {
			vrfID, ok := nr.client.Cache().GetID("vrfs", prefix.VRFName)
			if ok {
				lookup["vrf_id"] = vrfID
			}
		}

		_, err := nr.client.Apply("ipam", "prefixes", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile prefix %s: %w", prefix.Prefix, err)
		}
	}

	return nil
}
