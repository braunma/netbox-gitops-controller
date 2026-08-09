// SPDX-License-Identifier: Apache-2.0

package reconciler

import "github.com/braunma/netbox-gitops-controller/pkg/utils"

// siteScopeType is the NetBox content-type label identifying a site-scoped
// object. NetBox 4.2 replaced the dedicated `site` field on prefixes, VLAN
// groups and clusters with a generic scope expressed as scope_type + scope_id.
const siteScopeType = "dcim.site"

// setSiteScope assigns a site to a scoped object's payload using the NetBox 4.2
// generic-scope fields. Sending the pre-4.2 `site` field instead is silently
// dropped by the API, which made these objects report a phantom site change and
// re-PATCH on every run.
func setSiteScope(payload map[string]interface{}, siteID int) {
	payload["scope_type"] = siteScopeType
	payload["scope_id"] = siteID
}

// siteIDFromScope extracts the site ID from a fetched scoped object. NetBox 4.2
// returns scope_type/scope_id alongside a nested scope object; a pre-4.2 server
// (or a non-site scope) is tolerated by falling back to the legacy `site`
// reference. Returns 0 when the object is not scoped to a site.
func siteIDFromScope(obj map[string]interface{}) int {
	if st, _ := obj["scope_type"].(string); st == siteScopeType {
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
