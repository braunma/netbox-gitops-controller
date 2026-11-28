// SPDX-License-Identifier: Apache-2.0

package export

import (
	"net/netip"
)

// The loader fills in some fields the YAML leaves empty (see
// loader.DeriveDefaults). For an object that really has no value there, an
// exported declaration would therefore not mean "as it is": the first sync
// would set the derived value. These checks find those objects.

// globalIPInVRFPrefix reports the VRF an address without one would be given
// from the most specific prefix it falls into. Declaring it would make the
// sync look the address up inside that VRF, miss the one NetBox holds, and
// create a second one; such an address is not declared.
func (e *exporter) globalIPInVRFPrefix(address string) string {
	parsed, err := netip.ParsePrefix(address)
	if err != nil {
		return ""
	}
	addr := parsed.Addr()
	best, vrf := -1, ""
	for _, p := range e.data["ipam/prefixes"] {
		net, err := netip.ParsePrefix(str(p, "prefix"))
		if err != nil || !net.Contains(addr) || net.Bits() <= best {
			continue
		}
		best, vrf = net.Bits(), nestedStr(p, "vrf", "name")
	}
	return vrf
}

// vlanGroupDerived warns about a VLAN without a group at a site with exactly
// one VLAN group: the loader assigns it to that group.
func (e *exporter) vlanGroupDerived(name string, vid int, site string) {
	var groups []string
	for _, g := range e.data["ipam/vlan-groups"] {
		if s, ok := siteFromScope(g, e.siteSlugByID); ok && s == site {
			groups = append(groups, str(g, "slug"))
		}
	}
	if len(groups) == 1 {
		e.warn("VLAN %s (%d) at site %s is in no VLAN group; the controller files a VLAN without one under its site's only group, so the first sync moves it into %s. Put it in a group in NetBox first, or accept the move",
			name, vid, site, groups[0])
	}
}

// prefixSiteDerived warns about a prefix without a site whose VLAN is
// declared at exactly one site: the loader gives the prefix that site.
func (e *exporter) prefixSiteDerived(prefix, vlan string) {
	sites := map[string]bool{}
	for _, v := range e.data["ipam/vlans"] {
		if str(v, "name") == vlan {
			if s := nestedStr(v, "site", "slug"); s != "" {
				sites[s] = true
			}
		}
	}
	if len(sites) == 1 {
		e.warn("prefix %s has no site but its VLAN %s is at site %s; the controller derives the prefix's site from its VLAN, so the first sync scopes it to %s",
			prefix, vlan, joinSorted(sites), joinSorted(sites))
	}
}
