// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// A port the device type template defines is the hardware's, not the YAML's:
// once listed (and so tagged), then dropped from the YAML again, it must
// survive a prune. A port the YAML created beside the template is pruned.
func TestPruneKeepsTemplatePorts(t *testing.T) {
	f, c := newFakeNetBox(t)
	siteID, _, _ := seedDeviceFoundation(t, f, c)
	if err := c.Cache().LoadSite("berlin-dc"); err != nil {
		t.Fatalf("LoadSite() error = %v", err)
	}
	dev := f.seed("dcim", "devices", client.Object{"name": "sw-01", "site": siteID, "tags": managedTags(c)})
	devID := utils.GetIDFromObject(dev)
	// eth0 is a template port that an earlier apply tagged; eth9 is not in
	// the template and was created by an earlier YAML that no longer lists it.
	eth0 := f.seed("dcim", "interfaces", client.Object{"name": "eth0", "device": devID, "type": "1000base-t", "tags": managedTags(c)})
	eth9 := f.seed("dcim", "interfaces", client.Object{"name": "eth9", "device": devID, "type": "virtual", "tags": managedTags(c)})

	dr := NewDeviceReconciler(c)
	dr.SetDeviceTypes([]*models.DeviceType{{Slug: testDeviceTypeSlug, Interfaces: []models.InterfaceTemplate{{Name: "eth0", Type: "1000base-t"}}}})
	if err := dr.ReconcileDevices([]*models.DeviceConfig{testDevice("sw-01")}); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}
	if err := c.Prune([]client.PruneTarget{{App: "dcim", Endpoint: "interfaces"}}); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	left := map[int]bool{}
	for _, iface := range f.objects("dcim", "interfaces") {
		left[utils.GetIDFromObject(iface)] = true
	}
	if !left[utils.GetIDFromObject(eth0)] {
		t.Error("template port eth0 was pruned although the device type defines it")
	}
	if left[utils.GetIDFromObject(eth9)] {
		t.Error("eth9, created by the YAML and no longer listed, should have been pruned")
	}
}

// A declared template port without a type is sent with the template's type,
// so it can be created again should it ever be missing in NetBox.
func TestTemplatePortTypeFallback(t *testing.T) {
	f, c := newFakeNetBox(t)
	siteID, _, _ := seedDeviceFoundation(t, f, c)
	if err := c.Cache().LoadSite("berlin-dc"); err != nil {
		t.Fatalf("LoadSite() error = %v", err)
	}
	f.seed("dcim", "devices", client.Object{"name": "pp-01", "site": siteID, "tags": managedTags(c)})
	dr := NewDeviceReconciler(c)
	dr.SetDeviceTypes([]*models.DeviceType{{Slug: testDeviceTypeSlug,
		FrontPorts: []models.PortTemplate{{Name: "1", Type: "lc", RearPort: "1"}},
		RearPorts:  []models.PortTemplate{{Name: "1", Type: "lc"}}}})
	panel := testDevice("pp-01")
	panel.RearPorts = []models.RearPortConfig{{Name: "1"}}
	panel.FrontPorts = []models.FrontPortConfig{{Name: "1", RearPort: "1"}}
	if err := dr.ReconcileDevices([]*models.DeviceConfig{panel}); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}
	for _, kind := range []string{"rear-ports", "front-ports"} {
		ports := f.objects("dcim", kind)
		if len(ports) != 1 || ports[0]["type"] != "lc" {
			t.Errorf("%s = %v, want one port with type lc from the template", kind, ports)
		}
	}
}
