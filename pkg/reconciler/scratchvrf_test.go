// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"fmt"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// The sandbox rehearsal routes IPAM into a scratch VRF so a rewritten dataset
// cannot touch production IPAM, which the site rewrite alone cannot isolate (a
// prefix is identified by CIDR, an address by its address — neither carries a
// site). This proves it: a production prefix in the global table is left
// untouched when the same CIDR is applied under a run-created scratch VRF, and
// a second, VRF-scoped prefix is created instead.
func TestScratchVRFIsolatesProductionPrefix(t *testing.T) {
	f, c := newFakeNetBox(t)

	// Production prefix, global table (no VRF).
	prod := f.seed("ipam", "prefixes", client.Object{
		"prefix": "10.0.0.0/24",
		"status": map[string]interface{}{"value": "active"},
		"tags":   []interface{}{},
	})
	prodID := prod["id"].(int)
	f.resetMutations()

	nr := NewNetworkReconciler(c)
	// The run creates the scratch VRF first (network phase order), then the
	// prefix scoped to it.
	if err := nr.ReconcileVRFs([]*models.VRF{{Name: "sandbox", EnforceUnique: true}}); err != nil {
		t.Fatalf("ReconcileVRFs: %v", err)
	}
	if err := nr.ReconcilePrefixes([]*models.Prefix{
		{Prefix: "10.0.0.0/24", VRFName: "sandbox"},
	}); err != nil {
		t.Fatalf("ReconcilePrefixes: %v", err)
	}

	prefixes := f.objects("ipam", "prefixes")
	if len(prefixes) != 2 {
		t.Fatalf("expected 2 prefixes (production + scratch-VRF copy), got %d", len(prefixes))
	}

	// The production prefix must be byte-for-byte untouched: still global.
	var prodNow client.Object
	for _, p := range prefixes {
		if p["id"] == prodID {
			prodNow = p
		}
	}
	if prodNow == nil {
		t.Fatal("production prefix vanished")
	}
	if prodNow["vrf"] != nil {
		t.Fatalf("production prefix was re-scoped into a VRF: %v", prodNow["vrf"])
	}

	// No PATCH ever hit the production prefix.
	prodPath := fmt.Sprintf("/%d/", prodID)
	for _, m := range f.mutationLog() {
		if m.method == "PATCH" && strings.Contains(m.path, prodPath) {
			t.Fatalf("production prefix was modified: %+v", m)
		}
	}
}
