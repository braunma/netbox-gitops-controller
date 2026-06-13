package reconciler

import (
	"fmt"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// VirtualizationReconciler handles virtualization resources: cluster types,
// cluster groups, clusters, virtual machines and VM interfaces.
type VirtualizationReconciler struct {
	client *client.NetBoxClient
	logger *utils.Logger
}

// NewVirtualizationReconciler creates a new virtualization reconciler
func NewVirtualizationReconciler(c *client.NetBoxClient) *VirtualizationReconciler {
	return &VirtualizationReconciler{
		client: c,
		logger: c.Logger(),
	}
}

// ReconcileClusterTypes reconciles cluster type definitions
func (vr *VirtualizationReconciler) ReconcileClusterTypes(types []*models.ClusterType) error {
	vr.logger.Info("Reconciling %d cluster types...", len(types))

	for _, ct := range types {
		payload := map[string]interface{}{
			"name": ct.Name,
			"slug": ct.Slug,
		}
		if ct.Description != "" {
			payload["description"] = ct.Description
		}

		lookup := map[string]interface{}{"slug": ct.Slug}
		if _, err := vr.client.Apply("virtualization", "cluster-types", lookup, payload); err != nil {
			return fmt.Errorf("failed to reconcile cluster type %s: %w", ct.Name, err)
		}
	}

	return nil
}

// ReconcileClusterGroups reconciles cluster group definitions
func (vr *VirtualizationReconciler) ReconcileClusterGroups(groups []*models.ClusterGroup) error {
	vr.logger.Info("Reconciling %d cluster groups...", len(groups))

	for _, cg := range groups {
		payload := map[string]interface{}{
			"name": cg.Name,
			"slug": cg.Slug,
		}
		if cg.Description != "" {
			payload["description"] = cg.Description
		}

		lookup := map[string]interface{}{"slug": cg.Slug}
		if _, err := vr.client.Apply("virtualization", "cluster-groups", lookup, payload); err != nil {
			return fmt.Errorf("failed to reconcile cluster group %s: %w", cg.Name, err)
		}
	}

	return nil
}

// ReconcileClusters reconciles cluster definitions. The required type and the
// optional group/site/tenant references are resolved with live lookups, since
// the type and group are typically created earlier in this same run and the
// site/tenant in the foundation phase — none are in the global cache yet.
func (vr *VirtualizationReconciler) ReconcileClusters(clusters []*models.Cluster) error {
	vr.logger.Info("Reconciling %d clusters...", len(clusters))

	for _, cl := range clusters {
		typeID, ok := vr.lookupID("virtualization", "cluster-types", cl.TypeSlug)
		if !ok {
			vr.logger.Warning("Cluster type %s not found for cluster %s, skipping", cl.TypeSlug, cl.Name)
			vr.client.MarkReconcileIncomplete("virtualization", "clusters")
			continue
		}

		payload := map[string]interface{}{
			"name": cl.Name,
			"type": typeID,
		}
		if cl.Status != "" {
			payload["status"] = cl.Status
		}
		if cl.Description != "" {
			payload["description"] = cl.Description
		}

		if cl.GroupSlug != "" {
			if groupID, ok := vr.lookupID("virtualization", "cluster-groups", cl.GroupSlug); ok {
				payload["group"] = groupID
			} else {
				vr.logger.Warning("Cluster group %s not found for cluster %s, leaving unset", cl.GroupSlug, cl.Name)
			}
		}
		if cl.SiteSlug != "" {
			if siteID, ok := vr.lookupSiteID(cl.SiteSlug); ok {
				payload["site"] = siteID
			} else {
				vr.logger.Warning("Site %s not found for cluster %s, leaving unset", cl.SiteSlug, cl.Name)
			}
		}
		if cl.Tenant != "" {
			if tenantID, ok := vr.lookupID("tenancy", "tenants", cl.Tenant); ok {
				payload["tenant"] = tenantID
			} else {
				vr.logger.Warning("Tenant %s not found for cluster %s, leaving unset", cl.Tenant, cl.Name)
			}
		}

		lookup := map[string]interface{}{"name": cl.Name}
		if _, err := vr.client.Apply("virtualization", "clusters", lookup, payload); err != nil {
			return fmt.Errorf("failed to reconcile cluster %s: %w", cl.Name, err)
		}
	}

	return nil
}

// lookupID resolves an object slug to its ID with a live API lookup, so
// objects created earlier in the same run are found (the cache is loaded
// before any reconciliation and is not updated by Apply).
func (vr *VirtualizationReconciler) lookupID(app, endpoint, slug string) (int, bool) {
	objs, err := vr.client.Filter(app, endpoint, map[string]interface{}{"slug": slug})
	if err != nil || len(objs) == 0 {
		return 0, false
	}
	id := utils.GetIDFromObject(objs[0])
	return id, id != 0
}

// lookupSiteID resolves a site by slug, falling back to its name, mirroring
// the rack/VLAN reconcilers (the site may have been created this run).
func (vr *VirtualizationReconciler) lookupSiteID(slug string) (int, bool) {
	sites, err := vr.client.Filter("dcim", "sites", map[string]interface{}{"slug": slug})
	if err != nil || len(sites) == 0 {
		sites, err = vr.client.Filter("dcim", "sites", map[string]interface{}{"name": slug})
	}
	if err != nil || len(sites) == 0 {
		return 0, false
	}
	id := utils.GetIDFromObject(sites[0])
	return id, id != 0
}
