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
			siteID, ok := nr.client.Cache().GetGlobalID("sites", group.SiteSlug)
			if ok {
				setSiteScope(payload, siteID)
			}
		}

		if group.Description != "" {
			payload["description"] = group.Description
		}
		// NetBox 4.2 replaced the scalar min_vid/max_vid fields with vid_ranges,
		// a list of [min, max] pairs. Sending the old fields is silently dropped
		// and re-PATCHed every run; emit a single range covering the configured
		// bounds instead.
		if group.MinVID > 0 && group.MaxVID > 0 {
			payload["vid_ranges"] = []interface{}{
				[]interface{}{group.MinVID, group.MaxVID},
			}
		}

		lookup := map[string]interface{}{"slug": group.Slug}
		groupObj, err := nr.client.Apply("ipam", "vlan-groups", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile VLAN group %s: %w", group.Name, err)
		}

		// Seed the cache so VLANs reconciled later in this phase can resolve their
		// group. Site caches (which hold vlan_groups) are not loaded until the
		// device phase, so without this the group lookup in ReconcileVLANs misses
		// and the association is silently dropped. Mirror loadResource's key
		// scheme: site-scoped groups go under the composite site key, global
		// groups under the plain key. Skip dry-run creates (id 0).
		if id := utils.GetIDFromObject(groupObj); id > 0 {
			if siteID, ok := nr.client.Cache().GetGlobalID("sites", group.SiteSlug); group.SiteSlug != "" && ok {
				nr.client.Cache().RegisterSite("vlan_groups", siteID, id, group.Slug, group.Name)
			} else {
				nr.client.Cache().Register("vlan_groups", id, group.Slug, group.Name)
			}
		}
	}

	return nil
}

// ReconcileVLANs reconciles VLAN definitions
func (nr *NetworkReconciler) ReconcileVLANs(vlans []*models.VLAN) error {
	nr.logger.Info("Reconciling %d VLANs...", len(vlans))

	for _, vlan := range vlans {
		// Get site ID using LIVE lookup (not cache)
		sites, err := nr.client.Filter("dcim", "sites", map[string]interface{}{
			"slug": vlan.SiteSlug,
		})
		if err != nil || len(sites) == 0 {
			// Fallback: Try by name
			sites, err = nr.client.Filter("dcim", "sites", map[string]interface{}{
				"name": vlan.SiteSlug,
			})
		}

		if err != nil || len(sites) == 0 {
			nr.logger.Warning("Site %s not found for VLAN %s, skipping", vlan.SiteSlug, vlan.Name)
			nr.client.MarkReconcileIncomplete("ipam", "vlans")
			continue
		}

		siteID := utils.GetIDFromObject(sites[0])
		if siteID == 0 {
			nr.logger.Warning("Site %s has invalid ID for VLAN %s, skipping", vlan.SiteSlug, vlan.Name)
			nr.client.MarkReconcileIncomplete("ipam", "vlans")
			continue
		}

		payload := map[string]interface{}{
			"name":   vlan.Name,
			"vid":    vlan.VID,
			"site":   siteID,
			"status": vlan.Status,
		}

		if vlan.GroupSlug != "" {
			// VLAN groups can be site-specific OR global
			// Try site-scoped lookup first (most common), then global fallback
			groupID, ok := nr.client.Cache().GetSiteID("vlan_groups", siteID, vlan.GroupSlug)
			if !ok {
				// Fallback: try global VLAN group (no site)
				groupID, ok = nr.client.Cache().GetGlobalID("vlan_groups", vlan.GroupSlug)
			}
			if ok {
				payload["group"] = groupID
			} else {
				nr.logger.Warning("VLAN group %s not found for VLAN %s", vlan.GroupSlug, vlan.Name)
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

		vlanObj, err := nr.client.Apply("ipam", "vlans", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile VLAN %s: %w", vlan.Name, err)
		}

		// Seed the cache so prefixes reconciled later in this phase can resolve
		// their VLAN. VLANs are site-scoped and not loaded into the cache until
		// the device phase, so without this the site-aware VLAN lookup in
		// ReconcilePrefixes misses and the association is silently dropped. Index
		// by name under the composite site key, matching loadResource. Skip
		// dry-run creates (id 0).
		if id := utils.GetIDFromObject(vlanObj); id > 0 {
			nr.client.Cache().RegisterSite("vlans", siteID, id, vlan.Name)
		}
	}

	return nil
}

// ReconcilePrefixes reconciles prefix definitions
func (nr *NetworkReconciler) ReconcilePrefixes(prefixes []*models.Prefix) error {
	nr.logger.Info("Reconciling %d prefixes...", len(prefixes))

	for _, prefix := range prefixes {
		payload := map[string]interface{}{
			"prefix":  prefix.Prefix,
			"status":  prefix.Status,
			"is_pool": prefix.IsPool,
		}

		if prefix.SiteSlug != "" {
			siteID, ok := nr.client.Cache().GetGlobalID("sites", prefix.SiteSlug)
			if ok {
				setSiteScope(payload, siteID)
			}
		}

		if prefix.VRFName != "" {
			vrfID, ok := nr.client.Cache().GetGlobalID("vrfs", prefix.VRFName)
			if ok {
				payload["vrf"] = vrfID
			}
		}

		if prefix.VLANName != "" {
			// CRITICAL: VLAN lookup must be site-aware to avoid cache collisions
			// If prefix has a site, use site-scoped VLAN lookup
			if prefix.SiteSlug != "" {
				siteID, ok := nr.client.Cache().GetGlobalID("sites", prefix.SiteSlug)
				if ok {
					vlanID, ok := nr.client.Cache().GetSiteID("vlans", siteID, prefix.VLANName)
					if ok {
						payload["vlan"] = vlanID
					} else {
						nr.logger.Warning("VLAN %s not found at site %s for prefix %s", prefix.VLANName, prefix.SiteSlug, prefix.Prefix)
					}
				}
			} else {
				// Fallback: prefix has no site, try legacy lookup (rare case)
				nr.logger.Warning("Prefix %s has VLAN but no site - using legacy lookup", prefix.Prefix)
				vlanID, ok := nr.client.Cache().GetGlobalID("vlans", prefix.VLANName)
				if ok {
					payload["vlan"] = vlanID
				}
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
			vrfID, ok := nr.client.Cache().GetGlobalID("vrfs", prefix.VRFName)
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
