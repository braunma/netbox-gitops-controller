// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// scopeSiteSlug returns the site slug of a scoped object (prefix, VLAN group,
// cluster). NetBox 4.2 expresses the scope generically as scope_type +
// scope_id with a nested scope object; a pre-4.2 server used a direct `site`
// reference. A scope that is not a site (a region, a location) yields "", which
// the caller reports as a gap this schema cannot express.
func scopeSiteSlug(obj client.Object) string {
	if st, _ := obj["scope_type"].(string); st == "dcim.site" {
		if s := slugOrName(nested(obj, "scope")); s != "" {
			return s
		}
	}
	// Pre-4.2, or a VLAN that carries a direct site FK.
	return refSlug(obj, "site")
}

// scopedElsewhere reports whether an object carries a scope that is not a site,
// so the caller can note the gap rather than silently emitting a site-less
// object.
func scopedElsewhere(obj client.Object) bool {
	st, _ := obj["scope_type"].(string)
	return st != "" && st != "dcim.site"
}

// network imports VRFs, VLAN groups, VLANs and prefixes.
func (rc *runContext) network() error {
	steps := []func() error{
		rc.importVRFs,
		rc.importVLANGroups,
		rc.importVLANs,
		rc.importPrefixes,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func (rc *runContext) importVRFs() error {
	objs, err := rc.f.list("ipam", "vrfs", nil)
	if err != nil {
		return err
	}

	// Under a rewrite, all IPAM is routed into a single scratch VRF (see
	// vrfOut), so the only VRF the sandbox declares is that scratch VRF, with
	// enforce_unique set — uniqueness must be at least as strict as the global
	// table, or a duplicate that production would reject could pass the
	// rehearsal. Production VRFs are not carried in; a rehearsal is not the
	// place to reproduce them.
	if rc.rewriting() && rc.opts.Rewrite.VRF != "" {
		rc.report.count("ipam/vrfs", len(objs), 1)
		return rc.emit("definitions/vrfs/vrfs.yaml",
			genHeader("The scratch VRF the sandbox routes all IPAM into."), "items",
			[]interface{}{&models.VRF{
				Name:          rc.opts.Rewrite.VRF,
				EnforceUnique: true,
				Description:   "Scratch VRF for the sandbox rehearsal; isolates imported IPAM from production.",
				Tags:          []string{rc.sandboxTag()},
			}})
	}

	var items []interface{}
	exported := 0
	for _, o := range objs {
		if !rc.keep(o) {
			continue
		}
		items = append(items, &models.VRF{
			Name:          str(o, "name"),
			RD:            str(o, "rd"),
			Description:   str(o, "description"),
			EnforceUnique: boolOf(o, "enforce_unique"),
			Tags:          rc.tags(o),
		})
		exported++
	}
	rc.report.count("ipam/vrfs", len(objs), exported)
	if len(items) == 0 {
		return nil
	}
	return rc.emit("definitions/vrfs/vrfs.yaml",
		genHeader("Every VRF this NetBox holds."), "items", items)
}

func (rc *runContext) importVLANGroups() error {
	objs, err := rc.f.list("ipam", "vlan-groups", nil)
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
		if scopedElsewhere(o) {
			rc.report.note("ipam/vlan-groups: a group scoped to a region or location is emitted without a scope (only site scope is modelled)")
		}
		items = append(items, &models.VLANGroup{
			Name:        str(o, "name"),
			Slug:        str(o, "slug"),
			SiteSlug:    rc.siteOut(siteSlug),
			MinVID:      vidBound(o, "min_vid"),
			MaxVID:      vidBound(o, "max_vid"),
			Description: str(o, "description"),
			Tags:        rc.tags(o),
		})
		exported++
	}
	rc.report.count("ipam/vlan-groups", len(objs), exported)
	if len(items) == 0 {
		return nil
	}
	return rc.emit("definitions/vlan_groups/vlan_groups.yaml",
		genHeader("Every VLAN group this NetBox holds."), "items", items)
}

func (rc *runContext) importVLANs() error {
	objs, err := rc.f.list("ipam", "vlans", nil)
	if err != nil {
		return err
	}
	var items []interface{}
	exported := 0
	for _, o := range objs {
		if !rc.keep(o) {
			continue
		}
		siteSlug := refSlug(o, "site")
		if siteSlug == "" {
			// The model requires a site; a site-less VLAN cannot be expressed.
			rc.report.skip("ipam/vlans", objName(o), "VLAN has no site; the schema requires site_slug")
			continue
		}
		if !rc.siteAllowed(siteSlug) {
			continue
		}
		items = append(items, &models.VLAN{
			Name:        str(o, "name"),
			VID:         intOf(o, "vid"),
			SiteSlug:    rc.siteOut(siteSlug),
			GroupSlug:   refSlug(o, "group"),
			Status:      choiceValue(o, "status"),
			Role:        refSlug(o, "role"),
			Description: str(o, "description"),
			Tags:        rc.tags(o),
		})
		exported++
	}
	rc.report.count("ipam/vlans", len(objs), exported)
	if len(items) == 0 {
		return nil
	}
	return rc.emit("definitions/vlans/vlans.yaml",
		genHeader("Every VLAN this NetBox holds. A VLAN with no site is skipped and\n"+
			"listed in the import report — the schema identifies a VLAN by its VID\n"+
			"within a site."), "items", items)
}

func (rc *runContext) importPrefixes() error {
	objs, err := rc.f.list("ipam", "prefixes", nil)
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
		// A --site filter cannot exclude a prefix that has no site: its scope
		// may be a region, a location or nothing (a container prefix is
		// commonly global). Count those separately so the picture is honest.
		if len(rc.opts.Sites) > 0 && siteSlug == "" {
			rc.report.note("ipam/prefixes: a --site filter does not exclude site-less prefixes; some may be outside the requested site(s)")
		}
		if siteSlug != "" && !rc.siteAllowed(siteSlug) {
			continue
		}
		if scopedElsewhere(o) {
			rc.report.note("ipam/prefixes: a prefix scoped to a region or location is emitted without a scope (only site scope is modelled)")
		}
		items = append(items, &models.Prefix{
			Prefix:      str(o, "prefix"),
			SiteSlug:    rc.siteOut(siteSlug),
			VRFName:     rc.vrfOut(refName(o, "vrf")),
			VLANName:    refName(o, "vlan"),
			Status:      choiceValue(o, "status"),
			Role:        refSlug(o, "role"),
			IsPool:      boolOf(o, "is_pool"),
			Description: str(o, "description"),
			Tags:        rc.tags(o),
		})
		exported++
	}
	rc.report.count("ipam/prefixes", len(objs), exported)
	if len(items) == 0 {
		return nil
	}
	return rc.emit("definitions/prefixes/prefixes.yaml",
		genHeader("Every prefix this NetBox holds."), "items", items)
}

// vidBound reads a VLAN-group VID bound, which NetBox has serialised as a plain
// integer and (on some releases) as a nested object.
func vidBound(obj client.Object, key string) int {
	if n := nested(obj, key); n != nil {
		return intOf(client.Object(n), "value")
	}
	return intOf(obj, key)
}
