// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"errors"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// A clustered VM inherits its site from its cluster and sends no `site` of its
// own. --assert-site must still refuse it when that cluster lives outside the
// allowed sites, or a VM lands in a production site the guard was meant to fence
// off.
func TestAssertSiteRefusesClusteredVMInDisallowedSite(t *testing.T) {
	f, c := newFakeNetBox(t)
	sandbox := f.seed("dcim", "sites", client.Object{"name": "Sandbox", "slug": "sandbox"})
	prod := f.seed("dcim", "sites", client.Object{"name": "Prod", "slug": "prod"})
	c.Cache().Register("sites", sandbox["id"].(int), "sandbox", "Sandbox")
	c.Cache().Register("sites", prod["id"].(int), "prod", "Prod")

	// A cluster that already exists in prod (not created this run).
	f.seed("virtualization", "clusters", client.Object{
		"name":       "prod-cluster",
		"type":       map[string]interface{}{"id": 1, "slug": "vmware"},
		"scope_type": "dcim.site", "scope_id": prod["id"],
		"scope": map[string]interface{}{"id": prod["id"]},
	})

	c.SetAssertSites([]string{"sandbox"})
	f.resetMutations()

	vr := NewVirtualizationReconciler(c)
	err := vr.ReconcileVMs([]*models.VMConfig{
		{Name: "vm-01", Cluster: "prod-cluster"},
	})
	if err == nil {
		t.Fatal("expected the guard to refuse a VM whose cluster lives in prod")
	}
	var sg *client.SiteGuardError
	if !errors.As(err, &sg) {
		t.Fatalf("expected a *client.SiteGuardError, got %T: %v", err, err)
	}
	for _, m := range f.mutationLog() {
		if m.method != "GET" {
			t.Fatalf("guard let a %s %s through before aborting", m.method, m.path)
		}
	}
}
