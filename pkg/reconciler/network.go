// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
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

		if _, err := ensure(nr.client, "ipam", "vrfs", ensureSpec{
			kind:        "VRF",
			name:        vrf.Name,
			lookup:      map[string]interface{}{"name": vrf.Name},
			renameField: "name",
			renameFrom:  nonEmpty(vrf.RenameFrom),
			payload:     payload,
			// Register so a VRF created in this same run resolves for the
			// prefixes and interfaces that reference it later. Without this,
			// the global cache — loaded once, before any phase — still has no
			// entry, so on a fresh NetBox a VRF-scoped prefix was created in
			// the global table and the next run created a second, correctly
			// scoped copy beside it.
			register: func(id int) {
				nr.client.Cache().Register("vrfs", id, vrf.Name)
			},
		}); err != nil {
			return err
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

		if _, err := ensure(nr.client, "ipam", "vlan-groups", ensureSpec{
			kind:        "VLAN group",
			name:        group.Name,
			lookup:      map[string]interface{}{"slug": group.Slug},
			renameField: "slug",
			renameFrom:  client.SlugifiedRename(group.RenameFrom),
			payload:     payload,
			// Seed the cache so VLANs reconciled later in this phase can
			// resolve their group. Site caches (which hold vlan_groups) are
			// not loaded until the device phase, so without this the group
			// lookup in ReconcileVLANs misses and the association is silently
			// dropped. Mirror loadResource's key scheme: site-scoped groups go
			// under the composite site key, global groups under the plain key.
			// Skip dry-run creates (id 0).
			register: func(id int) {
				if id <= 0 {
					return
				}
				if siteID, ok := nr.client.Cache().GetGlobalID("sites", group.SiteSlug); group.SiteSlug != "" && ok {
					nr.client.Cache().RegisterSite("vlan_groups", siteID, id, group.Slug, group.Name)
				} else {
					nr.client.Cache().Register("vlan_groups", id, group.Slug, group.Name)
				}
			},
		}); err != nil {
			return err
		}
	}

	return nil
}

// ReconcileVLANs reconciles VLAN definitions
func (nr *NetworkReconciler) ReconcileVLANs(vlans []*models.VLAN) error {
	nr.logger.Info("Reconciling %d VLANs...", len(vlans))

	for _, vlan := range vlans {
		siteID, ok := resolveRef(nr.client, reference{
			app: "dcim", endpoint: "sites", cacheKind: "sites",
			value: vlan.SiteSlug,
			kind:  "Site", forKind: "VLAN", forName: vlan.Name,
			consumerApp: "ipam", consumerEndpoint: "vlans",
		})
		if !ok {
			continue
		}

		payload := map[string]interface{}{
			"name":   vlan.Name,
			"vid":    vlan.VID,
			"site":   siteID,
			"status": defaultStatus(vlan.Status),
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

		if _, err := ensure(nr.client, "ipam", "vlans", ensureSpec{
			kind: "VLAN",
			name: vlan.Name,
			lookup: map[string]interface{}{
				"site_id": siteID,
				"vid":     vlan.VID,
			},
			// A VLAN is identified by its VID, so rename_from carries the
			// previous VID; correcting the *name* alone needs no declaration.
			renameField: "vid",
			renameFrom:  nonEmpty(vlan.RenameFrom),
			payload:     payload,
			// Seed the cache so prefixes reconciled later in this phase can
			// resolve their VLAN. VLANs are site-scoped and not loaded into
			// the cache until the device phase, so without this the site-aware
			// VLAN lookup in ReconcilePrefixes misses and the association is
			// silently dropped. Index by name under the composite site key,
			// matching loadResource. Skip dry-run creates (id 0).
			register: func(id int) {
				if id > 0 {
					nr.client.Cache().RegisterSite("vlans", siteID, id, vlan.Name)
				}
			},
		}); err != nil {
			return err
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
			"status":  defaultStatus(prefix.Status),
			"is_pool": prefix.IsPool,
		}

		if prefix.SiteSlug != "" {
			siteID, ok := nr.client.Cache().GetGlobalID("sites", prefix.SiteSlug)
			if ok {
				setSiteScope(payload, siteID)
			}
		}

		if prefix.VRFName != "" {
			// A VRF registered during a --dry-run has id 0 (nothing was
			// written), and NetBox rejects 0 both as a value and as a filter.
			if vrfID, ok := nr.client.Cache().GetGlobalID("vrfs", prefix.VRFName); ok && vrfID != 0 {
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
			if vrfID, ok := nr.client.Cache().GetGlobalID("vrfs", prefix.VRFName); ok {
				// A VRF this run declares but has only planned (id 0 under
				// --dry-run) must still scope the lookup: sending vrf_id=0 makes
				// Apply treat the prefix as new rather than omitting the filter
				// and matching a same-CIDR prefix in the global table or another
				// VRF. Without this, a dry-run of a scratch-VRF dataset would
				// plan an update to a production prefix that the real apply,
				// with the VRF created, would correctly create instead.
				lookup["vrf_id"] = vrfID
			}
		}

		// A prefix is identified by the prefix itself, so rename_from carries
		// the previous CIDR — the case where the network was typed wrong.
		if _, err := ensure(nr.client, "ipam", "prefixes", ensureSpec{
			kind:        "prefix",
			name:        prefix.Prefix,
			lookup:      lookup,
			renameField: "prefix",
			renameFrom:  nonEmpty(prefix.RenameFrom),
			payload:     payload,
		}); err != nil {
			return err
		}
	}

	return nil
}
