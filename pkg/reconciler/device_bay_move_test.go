// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// A blade whose device_bay changed sits in its old bay. NetBox refuses to
// install it elsewhere until that bay is empty, so the old bay is vacated
// first — and only that one: a bay holding another device is left alone.
func TestInstallDeviceIntoBayVacatesThePreviousBay(t *testing.T) {
	f, c := newFakeNetBox(t)
	create := func(endpoint string, body map[string]interface{}) int {
		t.Helper()
		obj, err := c.Request("POST", "/api/dcim/"+endpoint+"/", body)
		if err != nil {
			t.Fatalf("create %s: %v", endpoint, err)
		}
		return utils.GetIDFromObject(obj)
	}
	blade := create("devices", map[string]interface{}{"name": "blade-1"})
	other := create("devices", map[string]interface{}{"name": "blade-2"})
	oldBay := create("device-bays", map[string]interface{}{"name": "Bay-1", "installed_device": map[string]interface{}{"id": blade}})
	otherBay := create("device-bays", map[string]interface{}{"name": "Bay-2", "installed_device": map[string]interface{}{"id": other}})
	newBay := create("device-bays", map[string]interface{}{"name": "Bay-3"})
	f.resetMutations()

	dr := NewDeviceReconciler(c)
	if err := dr.installDeviceIntoBay(blade, newBay, &models.DeviceConfig{Name: "blade-1", DeviceBay: "Bay-3"}); err != nil {
		t.Fatalf("installDeviceIntoBay: %v", err)
	}

	installed := func(bayID int) int {
		t.Helper()
		bay, err := c.Get("dcim", "device-bays", bayID)
		if err != nil {
			t.Fatal(err)
		}
		return utils.GetIDFromObject(bay["installed_device"])
	}
	if got := installed(oldBay); got != 0 {
		t.Errorf("the old bay still holds device %d", got)
	}
	if got := installed(newBay); got != blade {
		t.Errorf("the new bay holds %d, want the blade (%d)", got, blade)
	}
	if got := installed(otherBay); got != other {
		t.Errorf("another device's bay was touched: it holds %d, want %d", got, other)
	}

	// Vacate first, install second: the other order is what NetBox rejects.
	var order []string
	for _, m := range f.mutations {
		if m.method == "PATCH" && len(m.path) > 0 && m.body != nil {
			if _, ok := m.body["installed_device"]; ok {
				order = append(order, m.path)
			}
		}
	}
	want := []string{fmt.Sprintf("/api/dcim/device-bays/%d/", oldBay), fmt.Sprintf("/api/dcim/device-bays/%d/", newBay)}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("bay updates = %v, want the old bay and then the new one %v", order, want)
	}

	// A second run finds the device where it belongs and changes nothing.
	f.resetMutations()
	if err := dr.installDeviceIntoBay(blade, newBay, &models.DeviceConfig{Name: "blade-1", DeviceBay: "Bay-3"}); err != nil {
		t.Fatal(err)
	}
	if len(f.mutations) != 0 {
		t.Errorf("a second run wrote %d requests", len(f.mutations))
	}
}
