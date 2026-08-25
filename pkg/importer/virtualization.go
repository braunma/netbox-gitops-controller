// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"path"
	"sort"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// unplacedVMFile parks VMs that belong to neither a cluster nor a site. The
// schema requires one or the other, so only a human can say where they go.
const unplacedVMFile = "inventory/virtual/_unplaced.yaml"

// virtualization imports cluster types, cluster groups, clusters and VMs.
func (rc *runContext) virtualization() error {
	steps := []func() error{
		rc.importClusterTypes,
		rc.importClusterGroups,
		rc.importClusters,
		rc.importVMs,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func (rc *runContext) importClusterTypes() error {
	objs, err := rc.f.list("virtualization", "cluster-types", nil)
	if err != nil {
		return err
	}
	var items []interface{}
	exported := 0
	for _, o := range objs {
		if !rc.keep(o) {
			continue
		}
		items = append(items, &models.ClusterType{
			Name: str(o, "name"), Slug: str(o, "slug"),
			Description: str(o, "description"), Tags: rc.tags(o),
		})
		exported++
	}
	rc.report.count("virtualization/cluster-types", len(objs), exported)
	if len(items) == 0 {
		return nil
	}
	return rc.emit("definitions/virtualization/cluster_types/cluster_types.yaml",
		genHeader("Every cluster type this NetBox holds."), "items", items)
}

func (rc *runContext) importClusterGroups() error {
	objs, err := rc.f.list("virtualization", "cluster-groups", nil)
	if err != nil {
		return err
	}
	var items []interface{}
	exported := 0
	for _, o := range objs {
		if !rc.keep(o) {
			continue
		}
		items = append(items, &models.ClusterGroup{
			Name: str(o, "name"), Slug: str(o, "slug"),
			Description: str(o, "description"), Tags: rc.tags(o),
		})
		exported++
	}
	rc.report.count("virtualization/cluster-groups", len(objs), exported)
	if len(items) == 0 {
		return nil
	}
	return rc.emit("definitions/virtualization/cluster_groups/cluster_groups.yaml",
		genHeader("Every cluster group this NetBox holds."), "items", items)
}

func (rc *runContext) importClusters() error {
	objs, err := rc.f.list("virtualization", "clusters", nil)
	if err != nil {
		return err
	}
	var items []interface{}
	exported := 0
	for _, o := range objs {
		if !rc.keep(o) {
			continue
		}
		siteSlug := scopeSiteSlug(o)
		if siteSlug != "" && !rc.siteAllowed(siteSlug) {
			continue
		}
		items = append(items, &models.Cluster{
			Name:        rc.nameOut(str(o, "name")),
			TypeSlug:    refSlug(o, "type"),
			GroupSlug:   refSlug(o, "group"),
			SiteSlug:    rc.siteOut(siteSlug),
			Tenant:      refName(o, "tenant"),
			Status:      choiceValue(o, "status"),
			Description: str(o, "description"),
			Tags:        rc.tags(o),
		})
		exported++
	}
	rc.report.count("virtualization/clusters", len(objs), exported)
	if len(items) == 0 {
		return nil
	}
	return rc.emit("definitions/virtualization/clusters/clusters.yaml",
		genHeader("Every cluster this NetBox holds."), "items", items)
}

func (rc *runContext) importVMs() error {
	vms, err := rc.f.list("virtualization", "virtual-machines", nil)
	if err != nil {
		return err
	}
	ifaces, err := rc.f.list("virtualization", "interfaces", nil)
	if err != nil {
		return err
	}
	ifByVM := groupBy(ifaces, "virtual_machine")

	ips, err := rc.f.list("ipam", "ip-addresses", nil)
	if err != nil {
		return err
	}
	ipsByIface := map[int][]client.Object{}
	for _, ip := range ips {
		if t, _ := ip["assigned_object_type"].(string); t == "virtualization.vminterface" {
			id := utils.GetIDFromObject(ip["assigned_object_id"])
			ipsByIface[id] = append(ipsByIface[id], ip)
		}
	}

	files := map[string][]interface{}{}
	exported := 0
	for _, o := range vms {
		if !rc.keep(o) {
			continue
		}
		siteSlug := refSlug(o, "site")
		clusterName := refName(o, "cluster")
		if siteSlug != "" && !rc.siteAllowed(siteSlug) {
			continue
		}
		vm := rc.mapVM(o, ifByVM, ipsByIface)
		// Placement uses the rewritten cluster/site so a sandbox VM lands in a
		// sandbox file, not under its production cluster's name.
		p := rc.vmPath(vm.Cluster, vm.SiteSlug)
		files[p] = append(files[p], vm)
		_ = clusterName
		exported++
	}
	rc.report.count("virtualization/virtual-machines", len(vms), exported)

	for p, items := range files {
		sort.Slice(items, func(i, j int) bool {
			return items[i].(*models.VMConfig).Name < items[j].(*models.VMConfig).Name
		})
		header := "Virtual machines in this cluster/site."
		if p == unplacedVMFile {
			header = "NOT APPLIED — the underscore parks this file.\n\n" +
				"These VMs belong to neither a cluster nor a site, which the schema\n" +
				"requires. Give each a cluster or a site_slug and rename this file\n" +
				"without the leading underscore."
		}
		if err := rc.emit(p, genHeader(header), "items", items); err != nil {
			return err
		}
	}
	return nil
}

// mapVM maps one VM with its interfaces and their IPs.
func (rc *runContext) mapVM(o client.Object, ifByVM, ipsByIface map[int][]client.Object) *models.VMConfig {
	cf, _ := o["custom_fields"].(map[string]interface{})
	vm := &models.VMConfig{
		Name:         rc.nameOut(str(o, "name")),
		VMID:         cfInt(cf, "vmid"),
		VMTemplateID: cfInt(cf, "vm_template_id"),
		Cluster:      rc.nameOut(refName(o, "cluster")),
		SiteSlug:     rc.siteOut(refSlug(o, "site")),
		RoleSlug:     refSlug(o, "role"),
		Platform:     refSlug(o, "platform"),
		Tenant:       refName(o, "tenant"),
		Status:       choiceValue(o, "status"),
		VCPUs:        int(floatOf(o, "vcpus")),
		Memory:       intOf(o, "memory"),
		Disk:         intOf(o, "disk"),
		Tags:         rc.tags(o),
	}

	primary := primaryIPIDs(o)
	ifaces := ifByVM[idOf(o)]
	sortByName(ifaces)
	for _, iface := range ifaces {
		ic := models.VMInterfaceConfig{
			Name:         str(iface, "name"),
			Description:  str(iface, "description"),
			MTU:          intOf(iface, "mtu"),
			MACAddress:   str(iface, "mac_address"),
			Mode:         choiceValue(iface, "mode"),
			UntaggedVLAN: vlanKey(nested(iface, "untagged_vlan")),
			TaggedVLANs:  taggedVLANKeys(iface),
			Parent:       refName(iface, "parent"),
			Tags:         rc.tags(iface),
		}
		if en, ok := iface["enabled"].(bool); ok && !en {
			f := false
			ic.Enabled = &f
		}
		ic.IP, ic.AddressRole = rc.pickIPFrom(ipsByIface[idOf(iface)], str(iface, "name"), primary)
		vm.Interfaces = append(vm.Interfaces, ic)
	}
	return vm
}

// pickIPFrom is pickIP over an already-gathered address list (VM interfaces).
func (rc *runContext) pickIPFrom(ips []client.Object, ifaceName string, primary map[int]bool) (*models.IPConfig, string) {
	if len(ips) == 0 {
		return nil, ""
	}
	sort.SliceStable(ips, func(i, j int) bool { return str(ips[i], "address") < str(ips[j], "address") })
	chosen := ips[0]
	role := ""
	for _, ip := range ips {
		if primary[idOf(ip)] {
			chosen, role = ip, "primary"
			break
		}
	}
	for _, ip := range ips {
		if idOf(ip) == idOf(chosen) {
			continue
		}
		rc.report.skip("ipam/ip-addresses", str(ip, "address"),
			"a second address on VM interface "+ifaceName+"; the schema holds one IP per interface")
	}
	return &models.IPConfig{
		Address:     str(chosen, "address"),
		DNSName:     str(chosen, "dns_name"),
		Description: str(chosen, "description"),
		Status:      choiceValue(chosen, "status"),
		VRF:         rc.vrfOut(refName(chosen, "vrf")),
		Tags:        rc.tags(chosen),
	}, role
}

// vmPath places a VM: one file per cluster, else per site, else the parked
// unplaced file.
func (rc *runContext) vmPath(cluster, site string) string {
	switch {
	case cluster != "":
		return path.Join("inventory/virtual", utils.Slugify(cluster)+".yaml")
	case site != "":
		return path.Join("inventory/virtual", site+".yaml")
	default:
		return unplacedVMFile
	}
}

// cfInt reads an integer custom-field value (NetBox numbers arrive as float64).
func cfInt(cf map[string]interface{}, name string) int {
	if cf == nil {
		return 0
	}
	switch v := cf[name].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}
