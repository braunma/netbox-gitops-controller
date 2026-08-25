// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// Setting a device's primary IP goes through client.Update directly, not
// through Apply, so it used to escape --adopt entirely: a first sync could
// rewrite primary_ip4 on a production device while promising to write nothing
// but the managed tag. Adoption mode must suppress it.
func TestAdoptSuppressesPrimaryIPWrite(t *testing.T) {
	f, c := newFakeNetBox(t)

	site := f.seed("dcim", "sites", client.Object{"name": "Berlin", "slug": "berlin"})
	role := f.seed("dcim", "device-roles", client.Object{"name": "Server", "slug": "server", "color": "00ff00"})
	dt := f.seed("dcim", "device-types", client.Object{"model": "R640", "slug": "r640",
		"manufacturer": map[string]interface{}{"id": 999, "name": "Dell", "slug": "dell"}})
	c.Cache().Register("sites", site["id"].(int), "berlin", "Berlin")
	c.Cache().Register("roles", role["id"].(int), "server", "Server")
	c.Cache().Register("device_types", dt["id"].(int), "r640", "R640")

	dev := f.seed("dcim", "devices", client.Object{
		"name":        "srv-01",
		"site":        map[string]interface{}{"id": site["id"]},
		"role":        map[string]interface{}{"id": role["id"]},
		"device_type": map[string]interface{}{"id": dt["id"]},
		"status":      map[string]interface{}{"value": "active"},
		"tags":        []interface{}{},
		// NetBox holds a primary IP that differs from what the YAML declares.
		"primary_ip4": map[string]interface{}{"id": 4242},
	})
	iface := f.seed("dcim", "interfaces", client.Object{"name": "eth0",
		"device": map[string]interface{}{"id": dev["id"]}, "tags": []interface{}{}})
	f.seed("ipam", "ip-addresses", client.Object{"address": "10.0.0.5/24",
		"assigned_object_type": "dcim.interface", "assigned_object_id": iface["id"],
		"tags": []interface{}{}})

	c.SetAdopt(true)
	f.resetMutations()

	dr := NewDeviceReconciler(c)
	err := dr.ReconcileDevices([]*models.DeviceConfig{{
		Name: "srv-01", SiteSlug: "berlin", RoleSlug: "server", DeviceTypeSlug: "r640",
		Interfaces: []models.InterfaceConfig{{
			Name: "eth0",
			IP:   &models.IPConfig{Address: "10.0.0.5/24"},
			// This is what would trigger the primary-IP PATCH.
			AddressRole: "primary",
		}},
	}})
	if err != nil {
		t.Fatalf("ReconcileDevices: %v", err)
	}

	// Every write this run made must be a tag-only PATCH.
	for _, m := range f.mutationLog() {
		if m.method != "PATCH" {
			t.Fatalf("adoption mode issued a %s: %+v", m.method, m)
		}
		if len(m.body) != 1 {
			t.Fatalf("adoption mode wrote a non-tag field: %s %s %+v", m.method, m.path, m.body)
		}
		if _, ok := m.body["tags"]; !ok {
			t.Fatalf("adoption mode wrote a non-tag field: %s %s %+v", m.method, m.path, m.body)
		}
	}

	// And the device's existing primary IP must be untouched.
	devices := f.objects("dcim", "devices")
	if got := devices[0]["primary_ip4"]; got == nil {
		t.Fatal("primary_ip4 was cleared under --adopt")
	} else if m, ok := got.(map[string]interface{}); !ok || m["id"] != 4242 {
		t.Fatalf("primary_ip4 was rewritten under --adopt: %v", got)
	}
}
