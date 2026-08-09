// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"fmt"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
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
		ctObj, err := vr.client.Apply("virtualization", "cluster-types", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile cluster type %s: %w", ct.Name, err)
		}
		// Register so a cluster can resolve a type created in this same run,
		// including in --dry-run where nothing is written to NetBox.
		vr.client.Cache().Register("cluster_types", utils.GetIDFromObject(ctObj), ct.Slug, ct.Name)
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
		cgObj, err := vr.client.Apply("virtualization", "cluster-groups", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile cluster group %s: %w", cg.Name, err)
		}
		vr.client.Cache().Register("cluster_groups", utils.GetIDFromObject(cgObj), cg.Slug, cg.Name)
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
			// Fall back to a type registered earlier in this same run: in dry-run
			// the live lookup cannot see a type that was never written to NetBox.
			typeID, ok = vr.client.Cache().GetGlobalID("cluster_types", cl.TypeSlug)
		}
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
			groupID, ok := vr.lookupID("virtualization", "cluster-groups", cl.GroupSlug)
			if !ok {
				groupID, ok = vr.client.Cache().GetGlobalID("cluster_groups", cl.GroupSlug)
			}
			if ok {
				payload["group"] = groupID
			} else {
				vr.logger.Warning("Cluster group %s not found for cluster %s, leaving unset", cl.GroupSlug, cl.Name)
			}
		}
		if cl.SiteSlug != "" {
			if siteID, ok := vr.lookupSiteID(cl.SiteSlug); ok {
				setSiteScope(payload, siteID)
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
		clObj, err := vr.client.Apply("virtualization", "clusters", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile cluster %s: %w", cl.Name, err)
		}
		// Register so a VM can resolve a cluster created in this same run.
		vr.client.Cache().Register("clusters", utils.GetIDFromObject(clObj), cl.Name)
	}

	return nil
}

// ReconcileVMs reconciles virtual machine definitions, including their
// interfaces and IP addresses. A VM is attached to its cluster and/or site
// (at least one is required, per VMConfig.Validate). The optional role,
// platform and tenant are resolved with live lookups since they are created
// in earlier phases of this same run.
func (vr *VirtualizationReconciler) ReconcileVMs(vms []*models.VMConfig) error {
	vr.logger.Info("Reconciling %d virtual machines...", len(vms))

	for _, vm := range vms {
		payload := map[string]interface{}{"name": vm.Name}
		if vm.Status != "" {
			payload["status"] = vm.Status
		}
		if vm.VCPUs > 0 {
			payload["vcpus"] = vm.VCPUs
		}
		if vm.Memory > 0 {
			payload["memory"] = vm.Memory
		}
		if vm.Disk > 0 {
			payload["disk"] = vm.Disk
		}
		// The VMID and the clone template id are stored in NetBox as custom
		// fields (the VM model has no native slot). Both `vmid` and
		// `vm_template_id` custom fields must be declared in the foundation
		// phase; see definitions/custom_fields. These are documentation only —
		// Terraform reads them from the YAML, not from NetBox.
		customFields := map[string]interface{}{}
		if vm.VMID > 0 {
			customFields["vmid"] = vm.VMID
		}
		if vm.VMTemplateID > 0 {
			customFields["vm_template_id"] = vm.VMTemplateID
		}
		if len(customFields) > 0 {
			payload["custom_fields"] = customFields
		}

		// Resolve cluster and/or site. The effective site (used for VLAN
		// lookups on interfaces) is the VM's own site, or its cluster's site.
		clusterID, clusterSiteID, hasCluster := 0, 0, false
		if vm.Cluster != "" {
			if id, siteID, ok := vr.lookupCluster(vm.Cluster); ok {
				clusterID, clusterSiteID, hasCluster = id, siteID, true
			} else {
				vr.logger.Warning("Cluster %s not found for VM %s", vm.Cluster, vm.Name)
			}
		}
		siteID, hasSite := 0, false
		if vm.SiteSlug != "" {
			if id, ok := vr.lookupSiteID(vm.SiteSlug); ok {
				siteID, hasSite = id, true
			} else {
				vr.logger.Warning("Site %s not found for VM %s", vm.SiteSlug, vm.Name)
			}
		}

		if !hasCluster && !hasSite {
			vr.logger.Warning("VM %s has no resolvable cluster or site, skipping", vm.Name)
			vr.client.MarkReconcileIncomplete("virtualization", "virtual-machines")
			continue
		}
		if hasCluster {
			payload["cluster"] = clusterID
		}
		if hasSite {
			payload["site"] = siteID
		}

		if vm.RoleSlug != "" {
			if roleID, ok := vr.lookupID("dcim", "device-roles", vm.RoleSlug); ok {
				payload["role"] = roleID
			} else {
				vr.logger.Warning("Role %s not found for VM %s, leaving unset", vm.RoleSlug, vm.Name)
			}
		}
		if vm.Platform != "" {
			if platformID, ok := vr.lookupID("dcim", "platforms", vm.Platform); ok {
				payload["platform"] = platformID
			} else {
				vr.logger.Warning("Platform %s not found for VM %s, leaving unset", vm.Platform, vm.Name)
			}
		}
		if vm.Tenant != "" {
			if tenantID, ok := vr.lookupID("tenancy", "tenants", vm.Tenant); ok {
				payload["tenant"] = tenantID
			} else {
				vr.logger.Warning("Tenant %s not found for VM %s, leaving unset", vm.Tenant, vm.Name)
			}
		}

		lookup := map[string]interface{}{"name": vm.Name}
		if hasCluster {
			lookup["cluster_id"] = clusterID
		}

		vmObj, err := vr.client.Apply("virtualization", "virtual-machines", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile VM %s: %w", vm.Name, err)
		}
		vmID := utils.GetIDFromObject(vmObj)

		effectiveSiteID := siteID
		if !hasSite {
			effectiveSiteID = clusterSiteID
		}

		if err := vr.reconcileVMInterfaces(vmID, effectiveSiteID, vm); err != nil {
			return fmt.Errorf("failed to reconcile interfaces for VM %s: %w", vm.Name, err)
		}
	}

	return nil
}

// reconcileVMInterfaces reconciles a VM's interfaces, including site-scoped
// VLAN resolution and IP assignment. It mirrors the device interface
// reconciler but cannot cable, so there is no link handling.
func (vr *VirtualizationReconciler) reconcileVMInterfaces(vmID, siteID int, vm *models.VMConfig) error {
	for i, iface := range vm.Interfaces {
		vr.logger.Debug("    VM interface %d/%d: %s", i+1, len(vm.Interfaces), iface.Name)

		payload := map[string]interface{}{
			"virtual_machine": vmID,
			"name":            iface.Name,
			"enabled":         iface.IsEnabled(),
		}
		if iface.Description != "" {
			payload["description"] = iface.Description
		}
		if iface.MTU > 0 {
			payload["mtu"] = iface.MTU
		}
		if iface.MACAddress != "" {
			payload["mac_address"] = iface.MACAddress
		}
		if iface.Mode != "" {
			payload["mode"] = iface.Mode
		}

		// VLANs are resolved with site-scoped cache keys, exactly like device
		// interfaces. A clustered VM inherits its cluster's site.
		if iface.UntaggedVLAN != "" {
			if vlanID, ok := vr.lookupSiteVLAN(siteID, iface.UntaggedVLAN); ok {
				payload["untagged_vlan"] = vlanID
			} else {
				vr.logger.Warning("      Untagged VLAN %s not found at site %d for VM %s", iface.UntaggedVLAN, siteID, vm.Name)
			}
		}
		if len(iface.TaggedVLANs) > 0 {
			var vlanIDs []int
			for _, vlanName := range iface.TaggedVLANs {
				if vlanID, ok := vr.lookupSiteVLAN(siteID, vlanName); ok {
					vlanIDs = append(vlanIDs, vlanID)
				} else {
					vr.logger.Warning("      Tagged VLAN %s not found at site %d for VM %s", vlanName, siteID, vm.Name)
				}
			}
			if len(vlanIDs) > 0 {
				payload["tagged_vlans"] = vlanIDs
			}
		}

		// A parent interface (e.g. for a sub-interface) is resolved live, as it
		// is created earlier in this same loop.
		if iface.Parent != "" {
			if parentID, ok := vr.lookupVMInterface(vmID, iface.Parent); ok {
				payload["parent"] = parentID
			} else {
				vr.logger.Warning("      Parent interface %s not found for VM %s, leaving unset", iface.Parent, vm.Name)
			}
		}

		lookup := map[string]interface{}{
			"virtual_machine_id": vmID,
			"name":               iface.Name,
		}

		ifaceObj, err := vr.client.Apply("virtualization", "interfaces", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to apply VM interface %s: %w", iface.Name, err)
		}
		ifaceID := utils.GetIDFromObject(ifaceObj)

		if iface.IP != nil && ifaceID > 0 {
			if err := vr.reconcileVMIP(vmID, ifaceID, &iface); err != nil {
				return fmt.Errorf("failed to reconcile IP for %s: %w", iface.Name, err)
			}
		}
	}

	return nil
}

// reconcileVMIP reconciles an IP address bound to a VM interface, and
// optionally promotes it to the VM's primary IP. Mirrors the device
// reconciler's reconcileIPAddress.
func (vr *VirtualizationReconciler) reconcileVMIP(vmID, ifaceID int, iface *models.VMInterfaceConfig) error {
	ipConfig := iface.IP

	payload := map[string]interface{}{
		"address":              ipConfig.Address,
		"assigned_object_type": constants.AssignedObjectVMInterface,
		"assigned_object_id":   ifaceID,
	}
	if ipConfig.Status != "" {
		payload["status"] = ipConfig.Status
	}
	if ipConfig.DNSName != "" {
		payload["dns_name"] = ipConfig.DNSName
	}
	if ipConfig.Description != "" {
		payload["description"] = ipConfig.Description
	}
	if ipConfig.VRF != "" {
		if vrfID, ok := vr.client.Cache().GetGlobalID("vrfs", ipConfig.VRF); ok {
			payload["vrf"] = vrfID
		}
	}

	lookup := map[string]interface{}{"address": ipConfig.Address}
	if ipConfig.VRF != "" {
		if vrfID, ok := vr.client.Cache().GetGlobalID("vrfs", ipConfig.VRF); ok {
			lookup["vrf_id"] = vrfID
		}
	}

	ipObj, err := vr.client.Apply("ipam", "ip-addresses", lookup, payload)
	if err != nil {
		return fmt.Errorf("failed to apply IP address: %w", err)
	}

	if iface.AddressRole == "primary" {
		if ipID := utils.GetIDFromObject(ipObj); ipID > 0 {
			if err := vr.setVMPrimaryIP(vmID, ipID); err != nil {
				return fmt.Errorf("failed to set primary IP: %w", err)
			}
		}
	}

	return nil
}

// setVMPrimaryIP sets the primary IP for a virtual machine, choosing the
// IPv4 or IPv6 field by address family. Mirrors the device setPrimaryIP.
func (vr *VirtualizationReconciler) setVMPrimaryIP(vmID, ipID int) error {
	ipObj, err := vr.client.Get("ipam", "ip-addresses", ipID)
	if err != nil {
		return fmt.Errorf("failed to get IP address: %w", err)
	}

	family := 4
	if fam, ok := ipObj["family"].(map[string]interface{}); ok {
		if val, ok := fam["value"].(float64); ok {
			family = int(val)
		}
	} else if fam, ok := ipObj["family"].(float64); ok {
		family = int(fam)
	}

	field := "primary_ip4"
	if family == 6 {
		field = "primary_ip6"
	}

	// Skip the PATCH if the VM already points at this IP. A direct Update records
	// an "update" unconditionally, so re-setting an unchanged primary IP inflates
	// the change count on every run.
	vm, err := vr.client.Get("virtualization", "virtual-machines", vmID)
	if err != nil {
		return fmt.Errorf("failed to get virtual machine: %w", err)
	}
	if utils.GetIDFromObject(vm[field]) == ipID {
		return nil
	}

	if err := vr.client.Update("virtualization", "virtual-machines", vmID, map[string]interface{}{
		field: ipID,
	}); err != nil {
		return fmt.Errorf("failed to update VM primary IP: %w", err)
	}

	vr.logger.Info("Set primary IP for VM %d", vmID)
	return nil
}

// lookupCluster resolves a cluster by name, returning its ID and the ID of
// its site (0 if the cluster has no site). A live lookup, since clusters are
// created in this same phase just before VMs.
func (vr *VirtualizationReconciler) lookupCluster(name string) (id, siteID int, ok bool) {
	clusters, err := vr.client.Filter("virtualization", "clusters", map[string]interface{}{"name": name})
	if err == nil && len(clusters) > 0 {
		cl := clusters[0]
		id = utils.GetIDFromObject(cl)
		// NetBox 4.2 reports a cluster's site via the generic scope, not a
		// `site` field; siteIDFromScope handles both representations.
		siteID = siteIDFromScope(cl)
		return id, siteID, id != 0
	}
	// Fall back to a cluster registered earlier in this same run: in --dry-run
	// the cluster was planned but never written to NetBox, so the live lookup
	// above misses. Its site id is unknown here (0), so VLAN resolution on the
	// VM's interfaces degrades to a warning — acceptable for a dry-run preview,
	// and the VM itself is still planned rather than silently skipped.
	if cid, found := vr.client.Cache().GetGlobalID("clusters", name); found {
		return cid, 0, true
	}
	return 0, 0, false
}

// lookupVMInterface resolves a VM interface by name on a given VM.
func (vr *VirtualizationReconciler) lookupVMInterface(vmID int, name string) (int, bool) {
	ifaces, err := vr.client.Filter("virtualization", "interfaces", map[string]interface{}{
		"virtual_machine_id": vmID,
		"name":               name,
	})
	if err != nil || len(ifaces) == 0 {
		return 0, false
	}
	id := utils.GetIDFromObject(ifaces[0])
	return id, id != 0
}

// lookupSiteVLAN resolves a VLAN name to its ID using the site-scoped cache,
// falling back to a global lookup. Returns false when no site is known.
func (vr *VirtualizationReconciler) lookupSiteVLAN(siteID int, name string) (int, bool) {
	if siteID == 0 {
		return 0, false
	}
	return vr.client.Cache().GetSiteID("vlans", siteID, name)
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
