// SPDX-License-Identifier: Apache-2.0

package reconciler

import "github.com/braunma/netbox-gitops-controller/pkg/client"

// preloadComponents lists the components of one parent on each endpoint in a
// single request apiece, so that reconciling them does not cost a lookup
// request per component (see client.PreloadComponents for why that is safe).
// The returned function releases the snapshots; it may be called more than
// once.
func preloadComponents(c *client.NetBoxClient, app, parentKey string, parentID int, endpoints ...string) func() {
	if parentID <= 0 || len(endpoints) == 0 {
		return func() {}
	}
	for _, endpoint := range endpoints {
		c.PreloadComponents(app, endpoint, parentKey, parentID)
	}
	return func() {
		for _, endpoint := range endpoints {
			c.ReleaseComponents(app, endpoint, parentKey, parentID)
		}
	}
}
