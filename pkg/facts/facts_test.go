// SPDX-License-Identifier: Apache-2.0

package facts

import (
	"reflect"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// fakeAPI holds devices by name and records every write.
type fakeAPI struct {
	devices map[string]client.Object
	dryRun  bool
	filters []map[string]interface{}
	updates []map[string]interface{}
}

func (f *fakeAPI) Filter(app, endpoint string, filters map[string]interface{}) ([]client.Object, error) {
	f.filters = append(f.filters, filters)
	if obj, ok := f.devices[filters["name"].(string)]; ok {
		return []client.Object{obj}, nil
	}
	return nil, nil
}

func (f *fakeAPI) Update(app, endpoint string, id int, data map[string]interface{}) error {
	f.updates = append(f.updates, map[string]interface{}{"endpoint": app + "/" + endpoint, "id": id, "data": data})
	return nil
}

func (f *fakeAPI) IsDryRun() bool { return f.dryRun }

func definitions(names ...string) []*models.CustomField {
	out := make([]*models.CustomField, 0, len(names)+2)
	for _, name := range names {
		out = append(out, &models.CustomField{Name: name, Type: "text", ObjectTypes: []string{"dcim.device"}})
	}
	// Neither of these may be written: one is outside the prefix, the other is
	// not assigned to devices.
	out = append(out,
		&models.CustomField{Name: "vmid", ObjectTypes: []string{"virtualization.virtualmachine"}},
		&models.CustomField{Name: "hw_vm_only", ObjectTypes: []string{"virtualization.virtualmachine"}})
	return out
}

// netboxDevice is a device as the API returns it: numbers are float64.
func netboxDevice(id int, customFields map[string]interface{}) client.Object {
	return client.Object{"id": float64(id), "custom_fields": customFields}
}

func TestWriteSendsOnlyWhatDiffers(t *testing.T) {
	api := &fakeAPI{devices: map[string]client.Object{
		"srv-01": netboxDevice(7, map[string]interface{}{
			"hw_cpu_count":    float64(2),
			"hw_bios_version": "1.5.0",
			"hw_ram_total_gb": nil,
			"owner_team":      "storage",
		}),
	}}
	w := NewWriter(api, definitions("hw_cpu_count", "hw_bios_version", "hw_ram_total_gb"), "hw_")

	report, err := w.Write(&models.DeviceConfig{Name: "srv-01", SiteSlug: "berlin"}, map[string]interface{}{
		"hw_cpu_count":    2,
		"hw_bios_version": "1.6.10",
		"hw_ram_total_gb": 128,
		"hw_power_state":  "On",
		"owner_team":      "scan",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if report.Outcome != Updated {
		t.Fatalf("Outcome = %s, want updated", report.Outcome)
	}
	if got := api.filters[0]; got["name"] != "srv-01" || got["site"] != "berlin" {
		t.Errorf("looked the device up with %v, want its name and site", got)
	}
	if len(api.updates) != 1 {
		t.Fatalf("%d updates, want 1", len(api.updates))
	}
	want := map[string]interface{}{"custom_fields": map[string]interface{}{
		"hw_bios_version": "1.6.10",
		"hw_ram_total_gb": 128,
	}}
	if got := api.updates[0]["data"]; !reflect.DeepEqual(got, want) {
		t.Errorf("payload = %v\nwant     %v — the equal integer, the undeclared field and the field outside the prefix must not be sent", got, want)
	}
	if api.updates[0]["id"] != 7 || api.updates[0]["endpoint"] != "dcim/devices" {
		t.Errorf("update went to %v id %v", api.updates[0]["endpoint"], api.updates[0]["id"])
	}
	if got := w.Undeclared(); !reflect.DeepEqual(got, []string{"hw_power_state"}) {
		t.Errorf("Undeclared = %v, want the reported field nobody declared", got)
	}
}

func TestWriteUnchangedAndMissing(t *testing.T) {
	api := &fakeAPI{devices: map[string]client.Object{
		"srv-01": netboxDevice(7, map[string]interface{}{"hw_cpu_count": float64(2)}),
	}}
	w := NewWriter(api, definitions("hw_cpu_count"), "hw_")

	same, err := w.Write(&models.DeviceConfig{Name: "srv-01"}, map[string]interface{}{"hw_cpu_count": 2})
	if err != nil || same.Outcome != Unchanged {
		t.Errorf("equal values: outcome %s, err %v; want unchanged", same.Outcome, err)
	}
	gone, err := w.Write(&models.DeviceConfig{Name: "srv-99"}, map[string]interface{}{"hw_cpu_count": 2})
	if err != nil || gone.Outcome != Missing {
		t.Errorf("unknown device: outcome %s, err %v; want missing", gone.Outcome, err)
	}
	if len(api.updates) != 0 {
		t.Errorf("%d updates were sent; neither case may write", len(api.updates))
	}
}

// A custom field the YAML sets on the device is the YAML's: the next sync
// would write its value back, so the scan leaves it alone and says so.
func TestWriteLeavesYAMLOwnedFields(t *testing.T) {
	api := &fakeAPI{devices: map[string]client.Object{
		"srv-01": netboxDevice(7, map[string]interface{}{"hw_cpu_count": float64(1), "hw_bios_version": "1.0"}),
	}}
	w := NewWriter(api, definitions("hw_cpu_count", "hw_bios_version"), "hw_")
	dev := &models.DeviceConfig{Name: "srv-01", CustomFields: map[string]interface{}{"hw_cpu_count": 1}}

	report, err := w.Write(dev, map[string]interface{}{"hw_cpu_count": 2, "hw_bios_version": "2.0"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !reflect.DeepEqual(report.YAMLOwned, []string{"hw_cpu_count"}) {
		t.Errorf("YAMLOwned = %v", report.YAMLOwned)
	}
	payload := api.updates[0]["data"].(map[string]interface{})["custom_fields"].(map[string]interface{})
	if _, sent := payload["hw_cpu_count"]; sent || payload["hw_bios_version"] != "2.0" {
		t.Errorf("payload = %v, want only the field the YAML does not set", payload)
	}
}

func TestWriteDryRunWritesNothing(t *testing.T) {
	api := &fakeAPI{dryRun: true, devices: map[string]client.Object{
		"srv-01": netboxDevice(7, map[string]interface{}{"hw_cpu_count": float64(1)}),
	}}
	w := NewWriter(api, definitions("hw_cpu_count"), "hw_")

	report, err := w.Write(&models.DeviceConfig{Name: "srv-01"}, map[string]interface{}{"hw_cpu_count": 2})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if report.Outcome != Updated || len(report.Changes) != 1 || report.Changes[0].Old != float64(1) {
		t.Errorf("a dry run must still report the change: %+v", report)
	}
	if len(api.updates) != 0 {
		t.Error("a dry run sent an update")
	}
}
