// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"fmt"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// The rule this file exists to keep: the controller writes what the YAML
// declares, and says nothing about anything else.
//
// A NetBox object is not owned exclusively by this repository. People type
// things into the UI — an asset tag off a sticker, a description, a serial
// somebody read off the chassis, a custom field another team maintains — and a
// sync that sent an empty value for every field the YAML happens to omit would
// erase all of it, quietly, on a run whose plan said "1 to update".
//
// So: a field absent from the YAML must be absent from the payload, and a
// custom field the YAML does not mention must survive untouched.
func TestReconcileNeverClearsUndeclaredFields(t *testing.T) {
	f, c := newFakeNetBox(t)
	siteID, roleID, deviceTypeID := seedDeviceFoundation(t, f, c)
	if err := c.Cache().LoadSite("berlin-dc"); err != nil {
		t.Fatalf("LoadSite() error = %v", err)
	}

	// A device somebody has been maintaining by hand in the NetBox UI.
	f.seed("dcim", "devices", client.Object{
		"name": "sw-01", "site": siteID, "role": roleID, "device_type": deviceTypeID,
		"status":      "active",
		"serial":      "TYPED-IN-THE-UI",
		"asset_tag":   "STICKER-4711",
		"description": "the one under the desk",
		"comments":    "do not power cycle during business hours",
		"custom_fields": map[string]interface{}{
			"owner_team":   "platform",
			"hw_cpu_count": 2,
		},
		"tags": []interface{}{map[string]interface{}{"id": c.ManagedTagID()}},
	})

	// The YAML says only where it is and what it is. It declares no serial, no
	// asset tag, no description, no custom fields.
	device := testDevice("sw-01", withStatus("active"))
	if err := NewDeviceReconciler(c).ReconcileDevices([]*models.DeviceConfig{device}); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}

	// Nothing the YAML is silent about may appear in a request body at all —
	// checking the stored object alone would pass even if an empty string had
	// been sent and the fake had happened to ignore it.
	for _, req := range f.mutationLog() {
		for _, field := range []string{"serial", "asset_tag", "description", "comments"} {
			if value, sent := req.body[field]; sent {
				t.Errorf("%s %s sent %s=%v, which the YAML does not declare",
					req.method, req.path, field, value)
			}
		}
		if fields, sent := req.body["custom_fields"]; sent {
			t.Errorf("%s %s sent custom_fields=%v, which the YAML does not declare",
				req.method, req.path, fields)
		}
	}

	devices := f.objects("dcim", "devices")
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}
	stored := devices[0]
	for field, want := range map[string]string{
		"serial":      "TYPED-IN-THE-UI",
		"asset_tag":   "STICKER-4711",
		"description": "the one under the desk",
		"comments":    "do not power cycle during business hours",
	} {
		if got := stored[field]; got != want {
			t.Errorf("%s = %v after a sync that never mentioned it, want %q", field, got, want)
		}
	}
	fields, _ := stored["custom_fields"].(map[string]interface{})
	if fields["owner_team"] != "platform" {
		t.Errorf("custom_fields = %v, want the values NetBox held to survive", fields)
	}
}

// The other half of the rule: a field the YAML *does* declare is written, and
// a declared custom field does not disturb the ones beside it.
func TestReconcileWritesDeclaredFieldsAndLeavesTheRest(t *testing.T) {
	f, c := newFakeNetBox(t)
	siteID, roleID, deviceTypeID := seedDeviceFoundation(t, f, c)
	if err := c.Cache().LoadSite("berlin-dc"); err != nil {
		t.Fatalf("LoadSite() error = %v", err)
	}

	f.seed("dcim", "devices", client.Object{
		"name": "sw-01", "site": siteID, "role": roleID, "device_type": deviceTypeID,
		"status": "active", "serial": "OLD",
		"custom_fields": map[string]interface{}{"owner_team": "platform", "hw_cpu_count": 1},
		"tags":          []interface{}{map[string]interface{}{"id": c.ManagedTagID()}},
	})

	device := testDevice("sw-01", withStatus("active"))
	device.Serial = "SCANNED-SERIAL"
	device.CustomFields = map[string]interface{}{"hw_cpu_count": 2}

	if err := NewDeviceReconciler(c).ReconcileDevices([]*models.DeviceConfig{device}); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}

	stored := f.objects("dcim", "devices")[0]
	if stored["serial"] != "SCANNED-SERIAL" {
		t.Errorf("serial = %v, want the declared value written", stored["serial"])
	}
	fields, _ := stored["custom_fields"].(map[string]interface{})
	if fmt.Sprintf("%v", fields["hw_cpu_count"]) != "2" {
		t.Errorf("hw_cpu_count = %v, want the declared value written", fields["hw_cpu_count"])
	}
	if fields["owner_team"] != "platform" {
		t.Errorf("custom_fields = %v, want the undeclared field to survive", fields)
	}

	// Idempotent: a second run of the same YAML changes nothing.
	f.resetMutations()
	if err := NewDeviceReconciler(c).ReconcileDevices([]*models.DeviceConfig{device}); err != nil {
		t.Fatalf("ReconcileDevices() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}
