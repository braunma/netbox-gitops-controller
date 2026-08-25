// SPDX-License-Identifier: Apache-2.0

package client

import (
	"fmt"
	"sort"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// SiteGuardError is raised when the --assert-site write guard refuses a write
// that would land outside the allowed sites. It is a distinct type so the CLI
// can map it to the dedicated guard-violation exit code rather than the generic
// error code.
type SiteGuardError struct{ msg string }

func (e *SiteGuardError) Error() string { return e.msg }

// SetAssertSites arms the destination guard: every create and update must
// resolve to one of these site slugs, or have no site at all (shared objects
// like tags, roles and device types, which adoption is meant to touch). The
// guard also refuses an update to an existing object whose *current* site is
// outside the set — the case that catches a write about to move a production
// object. Passing no slugs disarms it.
func (c *NetBoxClient) SetAssertSites(slugs []string) {
	c.assertSites = map[string]bool{}
	for _, s := range slugs {
		if s != "" {
			c.assertSites[s] = true
		}
	}
	c.assertSiteIDs = nil // resolved lazily against the live instance
	c.sharedTouched = nil
}

// assertArmed reports whether the site guard is active.
func (c *NetBoxClient) assertArmed() bool { return len(c.assertSites) > 0 }

// SharedTouched returns the shared (site-less) endpoints the guard allowed
// through, so the caller can show the blast radius. Deduplicated and sorted.
func (c *NetBoxClient) SharedTouched() []string {
	out := make([]string, 0, len(c.sharedTouched))
	for k := range c.sharedTouched {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// siteScopedEndpoints are the endpoints whose object carries a site this guard
// can resolve. Everything else (interfaces, IP addresses, tags, roles, device
// types, …) has no site of its own — an interface is scoped by its device,
// which the guard checks when that device is written — and is treated as
// shared, which adoption is meant to touch.
var siteScopedEndpoints = map[string]bool{
	"devices":          true,
	"racks":            true,
	"vlans":            true,
	"prefixes":         true,
	"vlan-groups":      true,
	"clusters":         true,
	"virtual-machines": true,
}

// resolveAssertSiteIDs turns the allowed slugs into NetBox site ids, once.
func (c *NetBoxClient) resolveAssertSiteIDs() error {
	if c.assertSiteIDs != nil {
		return nil
	}
	c.assertSiteIDs = map[int]bool{}
	for slug := range c.assertSites {
		sites, err := c.Filter("dcim", "sites", map[string]interface{}{"slug": slug})
		if err != nil {
			return fmt.Errorf("resolving --assert-site %q: %w", slug, err)
		}
		if len(sites) == 0 {
			// A slug that names no site cannot be satisfied by any write, so a
			// run confined to it would be a silent no-op. Fail loudly instead.
			return &SiteGuardError{fmt.Sprintf("--assert-site %q names no site on this instance", slug)}
		}
		c.assertSiteIDs[utils.GetIDFromObject(sites[0])] = true
	}
	return nil
}

// checkSiteGuard enforces the destination guard for one write. existing is the
// object being updated (nil for a create); payload is the desired state. It
// returns a *SiteGuardError when the write would land, or already sits, outside
// the allowed sites.
func (c *NetBoxClient) checkSiteGuard(endpoint string, existing Object, payload map[string]interface{}) error {
	if !c.assertArmed() {
		return nil
	}
	if err := c.resolveAssertSiteIDs(); err != nil {
		return err
	}

	if !siteScopedEndpoints[endpoint] {
		// A shared, site-less object: allowed, but recorded so the caller can
		// show what the run touched outside any site.
		if c.sharedTouched == nil {
			c.sharedTouched = map[string]bool{}
		}
		c.sharedTouched[endpoint] = true
		return nil
	}

	// The desired site (from the payload) must be allowed.
	if id, ok := payloadSiteID(payload); ok && !c.assertSiteIDs[id] {
		return &SiteGuardError{fmt.Sprintf(
			"--assert-site: refusing to write %s into site id %d, which is not in the allowed set %s",
			endpoint, id, c.allowedSitesString())}
	}
	// An existing object's current site must be allowed too, or this update
	// would move a production object.
	if existing != nil {
		if id := objectSiteID(existing); id != 0 && !c.assertSiteIDs[id] {
			return &SiteGuardError{fmt.Sprintf(
				"--assert-site: refusing to update %s %q, which currently lives in site id %d, outside the allowed set %s",
				endpoint, objectLabel(existing), id, c.allowedSitesString())}
		}
	}
	return nil
}

// allowedSitesString renders the allowed slugs for an error message.
func (c *NetBoxClient) allowedSitesString() string {
	slugs := make([]string, 0, len(c.assertSites))
	for s := range c.assertSites {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)
	return "[" + strings.Join(slugs, ", ") + "]"
}

// payloadSiteID extracts the site id a payload targets: a direct site field, or
// a NetBox 4.2 site scope.
func payloadSiteID(payload map[string]interface{}) (int, bool) {
	if v, ok := payload["site"]; ok {
		if id := utils.GetIDFromObject(v); id != 0 {
			return id, true
		}
	}
	if st, _ := payload["scope_type"].(string); st == "dcim.site" {
		if id := utils.GetIDFromObject(payload["scope_id"]); id != 0 {
			return id, true
		}
	}
	return 0, false
}

// objectSiteID extracts the site id a fetched object currently sits in.
func objectSiteID(obj Object) int {
	if st, _ := obj["scope_type"].(string); st == "dcim.site" {
		if id := utils.GetIDFromObject(obj["scope_id"]); id != 0 {
			return id
		}
		if id := utils.GetIDFromObject(obj["scope"]); id != 0 {
			return id
		}
	}
	if id := utils.GetIDFromObject(obj["site"]); id != 0 {
		return id
	}
	return 0
}
