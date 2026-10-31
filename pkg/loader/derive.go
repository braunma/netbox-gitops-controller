// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// DeriveDefaults fills in what the rest of the data already says, for every
// field left empty that another declared object determines:
//
//   - a device's site, from its rack (a rack is at one site) or its parent
//     device (a child sits where its chassis sits);
//   - an address's VRF, from the most specific declared prefix it falls into;
//   - a prefix's site, from its VLAN;
//   - a VLAN's group, when its site has exactly one VLAN group.
//
// A value written in the YAML is never changed. Every derivation is returned
// as one line ("Derived: device srv-01 interface idrac: vrf "Production"
// (from prefix 10.0.10.0/24)") and logged by every run, so what the YAML
// leaves out is still on the record of what was sent. The rule for each is
// stated in the README under "What the controller fills in".
func DeriveDefaults(ds *Dataset) ([]string, error) {
	d := &deriver{ds: ds}
	d.index()
	d.devices()
	d.prefixesAndVLANs()
	d.addresses()
	return d.lines, errors.Join(d.errs...)
}

type deriver struct {
	ds    *Dataset
	lines []string
	errs  []error

	rackSite     map[string]string // rack slug -> site slug
	deviceByName map[string]*models.DeviceConfig
	vlanSites    map[string][]string // VLAN name -> sites it is declared at
	vlanAtSite   map[string]map[string]bool
	groupsAtSite map[string][]string
	prefixes     []declaredPrefix
}

type declaredPrefix struct {
	net netip.Prefix
	vrf string
}

func (d *deriver) note(format string, args ...interface{}) {
	d.lines = append(d.lines, fmt.Sprintf(format, args...))
}

func (d *deriver) index() {
	d.rackSite = make(map[string]string, len(d.ds.Racks))
	for _, r := range d.ds.Racks {
		d.rackSite[r.Slug] = r.SiteSlug
	}
	d.deviceByName = make(map[string]*models.DeviceConfig, len(d.ds.Devices))
	for _, dev := range d.ds.Devices {
		if _, seen := d.deviceByName[dev.Name]; !seen {
			d.deviceByName[dev.Name] = dev
		}
	}
	d.vlanSites = make(map[string][]string)
	for _, v := range d.ds.VLANs {
		d.vlanSites[v.Name] = append(d.vlanSites[v.Name], v.SiteSlug)
	}
	d.groupsAtSite = make(map[string][]string)
	for _, g := range d.ds.VLANGroups {
		d.groupsAtSite[g.SiteSlug] = append(d.groupsAtSite[g.SiteSlug], g.Slug)
	}
	for _, p := range d.ds.Prefixes {
		if parsed, err := netip.ParsePrefix(p.Prefix); err == nil {
			d.prefixes = append(d.prefixes, declaredPrefix{net: parsed, vrf: p.VRFName})
		}
	}
	// Most specific first, so the first match is the enclosing prefix.
	sort.Slice(d.prefixes, func(i, j int) bool { return d.prefixes[i].net.Bits() > d.prefixes[j].net.Bits() })
}

// enclosing returns the most specific declared prefix an address is in.
func (d *deriver) enclosing(addr netip.Addr) (declaredPrefix, bool) {
	for _, p := range d.prefixes {
		if p.net.Contains(addr) {
			return p, true
		}
	}
	return declaredPrefix{}, false
}

// devices fills a device's site from its rack or its parent. Children are
// visited after their parents by iterating until nothing changes, so a
// chassis without a site that gets one from its rack passes it on.
func (d *deriver) devices() {
	for changed := true; changed; {
		changed = false
		for _, dev := range d.ds.Devices {
			if dev.SiteSlug != "" {
				continue
			}
			switch {
			case dev.RackSlug != "" && d.rackSite[dev.RackSlug] != "":
				dev.SiteSlug = d.rackSite[dev.RackSlug]
				d.note("Derived: device %s: site %q (from rack %s)", dev.Name, dev.SiteSlug, dev.RackSlug)
				changed = true
			case dev.ParentDevice != "" && d.deviceByName[dev.ParentDevice] != nil && d.deviceByName[dev.ParentDevice].SiteSlug != "":
				dev.SiteSlug = d.deviceByName[dev.ParentDevice].SiteSlug
				d.note("Derived: device %s: site %q (from parent device %s)", dev.Name, dev.SiteSlug, dev.ParentDevice)
				changed = true
			}
		}
	}
	for _, dev := range d.ds.Devices {
		if dev.SiteSlug != "" {
			continue
		}
		switch {
		case dev.RackSlug != "":
			d.errs = append(d.errs, fmt.Errorf("device %q: site is required; rack %q is not declared in this repository, so it cannot supply one", dev.Name, dev.RackSlug))
		case dev.ParentDevice != "":
			d.errs = append(d.errs, fmt.Errorf("device %q: site is required; parent device %q is not declared in this repository, so it cannot supply one", dev.Name, dev.ParentDevice))
		default:
			d.errs = append(d.errs, fmt.Errorf("device %q: site is required (or a rack or parent_device the site can be taken from)", dev.Name))
		}
	}
}

// prefixesAndVLANs fills a prefix's site from its VLAN and a VLAN's group
// from the one group at its site.
func (d *deriver) prefixesAndVLANs() {
	for _, p := range d.ds.Prefixes {
		if p.SiteSlug != "" || p.VLANName == "" {
			continue
		}
		if sites := d.vlanSites[p.VLANName]; len(sites) == 1 {
			p.SiteSlug = sites[0]
			d.note("Derived: prefix %s: site %q (from VLAN %s)", p.Prefix, p.SiteSlug, p.VLANName)
		}
	}
	for _, v := range d.ds.VLANs {
		if v.GroupSlug != "" || v.SiteSlug == "" {
			continue
		}
		if groups := d.groupsAtSite[v.SiteSlug]; len(groups) == 1 {
			v.GroupSlug = groups[0]
			d.note("Derived: VLAN %s (%d): group %q (the one VLAN group at site %s)", v.Name, v.VID, v.GroupSlug, v.SiteSlug)
		}
	}
}

// addresses fills, for every interface with an address, the VRF from the
// enclosing prefix. The access VLAN is deliberately not derived from it: a
// port's layer 2 (access, trunk, routed) is not implied by its address, and
// a wrong guess there changes what the switch side is told.
func (d *deriver) addresses() {
	for _, dev := range d.ds.Devices {
		for i := range dev.Interfaces {
			iface := &dev.Interfaces[i]
			if iface.IP == nil || iface.IP.Address == "" {
				continue
			}
			parsed, err := netip.ParsePrefix(iface.IP.Address)
			if err != nil {
				continue
			}
			p, ok := d.enclosing(parsed.Addr())
			if !ok {
				continue
			}
			ref := fmt.Sprintf("device %s interface %s", dev.Name, iface.Name)
			if iface.IP.VRF == "" && p.vrf != "" {
				iface.IP.VRF = p.vrf
				d.note("Derived: %s: vrf %q (from prefix %s)", ref, p.vrf, p.net)
			}
		}
	}
	for _, vm := range d.ds.VMs {
		for i := range vm.Interfaces {
			iface := &vm.Interfaces[i]
			if iface.IP == nil || iface.IP.Address == "" || iface.IP.VRF != "" {
				continue
			}
			parsed, err := netip.ParsePrefix(iface.IP.Address)
			if err != nil {
				continue
			}
			if p, ok := d.enclosing(parsed.Addr()); ok && p.vrf != "" {
				iface.IP.VRF = p.vrf
				d.note("Derived: virtual machine %s interface %s: vrf %q (from prefix %s)", vm.Name, iface.Name, p.vrf, p.net)
			}
		}
	}
}
