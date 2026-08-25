// SPDX-License-Identifier: Apache-2.0

package importer_test

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/internal/nbtest"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/importer"
)

func TestImportDevicesTemplateDiffAndLayout(t *testing.T) {
	f, c := nbtest.New(t)
	f.Seed("dcim", "sites", client.Object{"name": "Berlin", "slug": "berlin"})
	dt := f.Seed("dcim", "device-types", client.Object{
		"model": "R640", "slug": "r640",
		"manufacturer": map[string]interface{}{"name": "Dell", "slug": "dell"},
	})
	dtID := dt["id"].(int)
	f.Seed("dcim", "interface-templates", client.Object{
		"name": "eth0", "type": map[string]interface{}{"value": "1000base-t"},
		"device_type": map[string]interface{}{"id": dtID},
	})

	dev := f.Seed("dcim", "devices", client.Object{
		"name":        "srv-01",
		"site":        map[string]interface{}{"slug": "berlin", "name": "Berlin"},
		"rack":        map[string]interface{}{"name": "Rack A01"},
		"role":        map[string]interface{}{"slug": "server"},
		"device_type": map[string]interface{}{"slug": "r640", "id": dtID},
		"position":    float64(10),
		"face":        map[string]interface{}{"value": "front"},
		"status":      map[string]interface{}{"value": "active"},
	})
	devID := dev["id"].(int)

	// eth0 matches the template (no type expected in output) but holds the
	// device's primary IP.
	eth0 := f.Seed("dcim", "interfaces", client.Object{
		"name": "eth0", "type": map[string]interface{}{"value": "1000base-t"},
		"enabled": true, "device": map[string]interface{}{"id": devID},
	})
	// eth9 is not in the template, so it must be emitted with its type.
	f.Seed("dcim", "interfaces", client.Object{
		"name": "eth9", "type": map[string]interface{}{"value": "10gbase-x-sfpp"},
		"enabled": true, "device": map[string]interface{}{"id": devID},
	})
	ip := f.Seed("ipam", "ip-addresses", client.Object{
		"address": "10.0.0.5/24", "assigned_object_type": "dcim.interface",
		"assigned_object_id": eth0["id"].(int),
	})
	// Mark it primary on the device.
	dev["primary_ip4"] = map[string]interface{}{"id": ip["id"].(int)}

	res, err := importer.Import(c, importer.Options{
		Phases:   map[string]bool{"devices": true},
		SplitBy:  "site",
		Defaults: importer.DefaultsOptions(true, 3),
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	file, ok := fileByPath(res, "inventory/hardware/active/berlin/rack-a01.yaml")
	if !ok {
		t.Fatalf("device not at expected site/rack path; files: %v", paths(res))
	}
	body := string(file.Bytes)

	if !strings.Contains(body, "name: eth0") {
		t.Errorf("eth0 (primary IP holder) should be emitted:\n%s", body)
	}
	if !strings.Contains(body, "address: 10.0.0.5/24") || !strings.Contains(body, "address_role: primary") {
		t.Errorf("primary IP not mapped:\n%s", body)
	}
	// eth9 must carry its type; eth0 (template match) must not.
	if !strings.Contains(body, "10gbase-x-sfpp") {
		t.Errorf("eth9 type missing:\n%s", body)
	}
	// The eth0 block must not restate the template type.
	if strings.Contains(body, "type: 1000base-t") {
		t.Errorf("template-matching type should be omitted:\n%s", body)
	}
}

func TestImportCableDeclaredOnce(t *testing.T) {
	f, c := nbtest.New(t)
	f.Seed("dcim", "sites", client.Object{"name": "Berlin", "slug": "berlin"})

	mk := func(name string) int {
		d := f.Seed("dcim", "devices", client.Object{
			"name":        name,
			"site":        map[string]interface{}{"slug": "berlin", "name": "Berlin"},
			"role":        map[string]interface{}{"slug": "server"},
			"device_type": map[string]interface{}{"slug": "r640"},
		})
		return d["id"].(int)
	}
	aDev := mk("aaa-01")
	zDev := mk("zzz-01")
	aEth := f.Seed("dcim", "interfaces", client.Object{
		"name": "eth0", "device": map[string]interface{}{"id": aDev},
		"type": map[string]interface{}{"value": "10gbase-x-sfpp"},
	})
	zEth := f.Seed("dcim", "interfaces", client.Object{
		"name": "eth0", "device": map[string]interface{}{"id": zDev},
		"type": map[string]interface{}{"value": "10gbase-x-sfpp"},
	})
	f.Seed("dcim", "cables", client.Object{
		"type": map[string]interface{}{"value": "smf"},
		"a_terminations": []interface{}{map[string]interface{}{
			"object_type": "dcim.interface", "object_id": aEth["id"].(int)}},
		"b_terminations": []interface{}{map[string]interface{}{
			"object_type": "dcim.interface", "object_id": zEth["id"].(int)}},
	})

	res, err := importer.Import(c, importer.Options{
		Phases: map[string]bool{"devices": true}, SplitBy: "none",
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	file, _ := fileByPath(res, "inventory/hardware/active/devices.yaml")
	body := string(file.Bytes)
	// The cable is declared exactly once, on the lexicographically-first end
	// (aaa-01), pointing at zzz-01.
	if n := strings.Count(body, "peer_device:"); n != 1 {
		t.Fatalf("cable should be declared once, found %d:\n%s", n, body)
	}
	if !strings.Contains(body, "peer_device: zzz-01") {
		t.Errorf("cable emitted on the wrong end:\n%s", body)
	}
}
