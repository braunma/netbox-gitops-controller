// SPDX-License-Identifier: Apache-2.0

package export

import (
	"path"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

type exportedVM struct {
	cfg  *models.VMConfig
	file string
}

func (e *exporter) exportVMs() []exportedVM {
	ifacesBy := byParent(e.data["virtualization/interfaces"], "virtual_machine")
	clusterSite := map[string]string{}
	for _, c := range e.data["virtualization/clusters"] {
		s, _ := siteFromScope(c, e.siteSlugByID)
		clusterSite[str(c, "name")] = s
	}

	// A VM is matched by its name alone, so a name NetBox holds twice would
	// make both declarations adopt the same VM.
	count := map[string]int{}
	for _, v := range e.data["virtualization/virtual-machines"] {
		count[str(v, "name")]++
	}

	var out []exportedVM
	for _, v := range e.data["virtualization/virtual-machines"] {
		name := str(v, "name")
		cluster := nestedStr(v, "cluster", "name")
		site := nestedStr(v, "site", "slug")
		effSite := site
		if effSite == "" {
			effSite = clusterSite[cluster]
		}
		if e.sites != nil && !e.inScope(effSite) {
			continue
		}
		if e.declared.has(kindVM, name) {
			e.skipDeclared(kindVM)
			continue
		}
		if count[name] > 1 {
			e.warn("virtual machine name %s exists %d times; none of them was exported, because a VM is matched by its name alone", name, count[name])
			continue
		}
		if cluster == "" && site == "" {
			e.warn("virtual machine %s has neither a cluster nor a site; this repository needs one of them, so it was not exported", name)
			continue
		}
		cfg := &models.VMConfig{
			Name: name, Cluster: cluster, SiteSlug: site,
			RoleSlug: nestedStr(v, "role", "slug"), Platform: nestedStr(v, "platform", "slug"),
			Tenant: nestedStr(v, "tenant", "slug"), Status: status(v),
			Tags: e.tags(v),
		}
		if n, present, ok := whole(v, "vcpus"); ok {
			cfg.VCPUs = n
		} else if present {
			e.warn("virtual machine %s has %s vCPUs, which is not a whole number; vcpus is left out and kept as it is", name, str(v, "vcpus"))
		}
		cfg.Memory, _, _ = whole(v, "memory")
		cfg.Disk, _, _ = whole(v, "disk")
		if cf, ok := v["custom_fields"].(map[string]interface{}); ok {
			cfg.VMID, _, _ = whole(cf, "vmid")
			cfg.VMTemplateID, _, _ = whole(cf, "vm_template_id")
		}

		ifaces := ifacesBy[idOf(v)]
		sortBy(ifaces, func(o client.Object) string { return naturalKey(str(o, "name")) })
		for _, i := range ifaces {
			cfg.Interfaces = append(cfg.Interfaces, e.vmInterfaceConfig(v, i, effSite))
		}

		group := cluster
		if group == "" {
			group = "site-" + site
		}
		out = append(out, exportedVM{
			cfg:  cfg,
			file: path.Join("inventory/virtual", FileStem, fileName(group)+".yaml"),
		})
	}
	return out
}

func (e *exporter) vmInterfaceConfig(vm, i client.Object, site string) models.VMInterfaceConfig {
	what := "virtual machine " + str(vm, "name") + " interface " + str(i, "name")
	c := models.VMInterfaceConfig{
		Name: str(i, "name"), Description: str(i, "description"),
		Mode: choice(i, "mode"), Parent: nestedStr(i, "parent", "name"),
		MACAddress: str(i, "mac_address"), Tags: e.tags(i),
	}
	if en, ok := i["enabled"].(bool); ok && !en {
		c.Enabled = &en
	}
	c.MTU, _, _ = whole(i, "mtu")
	if v := nestedID(i, "untagged_vlan"); v != 0 {
		c.UntaggedVLAN = e.vlanRef(site, v, what)
	}
	if list, ok := i["tagged_vlans"].([]interface{}); ok {
		for _, v := range list {
			if ref := e.vlanRef(site, idOf(asMap(v)), what); ref != "" {
				c.TaggedVLANs = append(c.TaggedVLANs, ref)
			}
		}
	}
	if ip := e.pickIP("virtualization.vminterface", idOf(i), vm, what); ip != nil {
		c.IP = ip.cfg
		if ip.primary {
			c.AddressRole = "primary"
		}
	}
	return c
}

func (e *exporter) writeVMs(vms []exportedVM) {
	files := map[string][]*models.VMConfig{}
	for _, v := range vms {
		files[v.file] = append(files[v.file], v.cfg)
	}
	for _, p := range sortedFiles(files) {
		list := files[p]
		sortBy(list, func(v *models.VMConfig) string { return naturalKey(v.Name) })
		e.addFile(p, kindVM, list, len(list), "")
	}
}
