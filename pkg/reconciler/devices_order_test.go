// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// dev is a shorthand for the ordering tests: a device, optionally hosted in a
// bay on parent.
func dev(name, parent string) *models.DeviceConfig {
	if parent == "" {
		return testDevice(name)
	}
	return testDevice(name, inBay(parent, "bay-1"))
}

func TestSortDevicesByDependency(t *testing.T) {
	tests := []struct {
		name  string
		input []*models.DeviceConfig
		want  []string
	}{
		{
			// The order the loader produces is lexical by filename, so a
			// "blades.yaml" is read before a "chassis.yaml".
			name:  "child declared before parent",
			input: []*models.DeviceConfig{dev("blade-01", "chassis-01"), dev("chassis-01", "")},
			want:  []string{"chassis-01", "blade-01"},
		},
		{
			name: "unrelated devices keep their original order",
			input: []*models.DeviceConfig{
				dev("sw-03", ""), dev("sw-01", ""), dev("sw-02", ""),
			},
			want: []string{"sw-03", "sw-01", "sw-02"},
		},
		{
			name: "siblings stay in declaration order behind their parent",
			input: []*models.DeviceConfig{
				dev("blade-01", "chassis-01"), dev("blade-02", "chassis-01"), dev("chassis-01", ""),
			},
			want: []string{"chassis-01", "blade-01", "blade-02"},
		},
		{
			name: "nesting is resolved at any depth",
			input: []*models.DeviceConfig{
				dev("sub-blade", "blade-01"), dev("blade-01", "chassis-01"), dev("chassis-01", ""),
			},
			want: []string{"chassis-01", "blade-01", "sub-blade"},
		},
		{
			// A parent from an earlier run already exists in NetBox and is
			// resolved by the live lookup, so it must not reorder anything.
			name:  "parent not declared in this run is left alone",
			input: []*models.DeviceConfig{dev("blade-01", "chassis-external"), dev("sw-01", "")},
			want:  []string{"blade-01", "sw-01"},
		},
		{
			name: "interleaved families are each ordered without disturbing the rest",
			input: []*models.DeviceConfig{
				dev("blade-a1", "chassis-a"), dev("sw-01", ""),
				dev("blade-b1", "chassis-b"), dev("chassis-b", ""), dev("chassis-a", ""),
			},
			want: []string{"chassis-a", "blade-a1", "sw-01", "chassis-b", "blade-b1"},
		},
		{
			name:  "already ordered input is unchanged",
			input: []*models.DeviceConfig{dev("chassis-01", ""), dev("blade-01", "chassis-01")},
			want:  []string{"chassis-01", "blade-01"},
		},
		{
			name:  "empty input",
			input: nil,
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sortDevicesByDependency(tt.input)
			if err != nil {
				t.Fatalf("sortDevicesByDependency() error = %v", err)
			}
			gotNames := deviceNames(got)
			if len(gotNames) != len(tt.want) {
				t.Fatalf("got %v, want %v", gotNames, tt.want)
			}
			for i := range tt.want {
				if gotNames[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", gotNames, tt.want)
				}
			}
		})
	}
}

func TestSortDevicesByDependencyRejectsCycles(t *testing.T) {
	tests := []struct {
		name  string
		input []*models.DeviceConfig
	}{
		{
			name:  "two-device cycle",
			input: []*models.DeviceConfig{dev("a", "b"), dev("b", "a")},
		},
		{
			name:  "three-device cycle",
			input: []*models.DeviceConfig{dev("a", "b"), dev("b", "c"), dev("c", "a")},
		},
		{
			name:  "device is its own parent",
			input: []*models.DeviceConfig{dev("a", "a")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A cycle can never be reconciled (no member can be created first),
			// so it must be reported rather than silently dropping devices.
			got, err := sortDevicesByDependency(tt.input)
			if err == nil {
				t.Fatalf("sortDevicesByDependency() = %v, want a cycle error", deviceNames(got))
			}
			if !strings.Contains(err.Error(), "cycle") {
				t.Errorf("error = %q, expected it to name the problem as a cycle", err)
			}
		})
	}
}

// TestReconcileDevicesOrdersParentBeforeChild is the regression test for the
// ordering itself: with the child listed first, reconciliation used to abort
// with "parent device chassis-01 not found" because the parent had not been
// created yet.
func TestReconcileDevicesOrdersParentBeforeChild(t *testing.T) {
	f, c := newFakeNetBox(t)
	siteID, roleID, deviceTypeID := seedDeviceFoundation(t, f, c)
	_, _ = siteID, roleID

	// The chassis device type carries a bay template, so the bay is self-healed
	// onto the parent during its own reconcile — as it is on a real apply.
	f.seed("dcim", "device-bay-templates", client.Object{
		"name": "bay-1", "device_type": deviceTypeID,
	})

	// Child first, exactly as the loader would hand them over from a
	// "blades.yaml" read before a "chassis.yaml".
	devices := []*models.DeviceConfig{
		dev("blade-01", "chassis-01"),
		dev("chassis-01", ""),
	}

	if err := NewDeviceReconciler(c).ReconcileDevices(devices); err != nil {
		t.Fatalf("ReconcileDevices() error = %v; a same-run parent must be reconciled before its child", err)
	}

	var chassis, blade client.Object
	for _, d := range f.objects("dcim", "devices") {
		switch d["name"] {
		case "chassis-01":
			chassis = d
		case "blade-01":
			blade = d
		}
	}
	if chassis == nil || blade == nil {
		t.Fatalf("expected both devices to be created, got chassis=%v blade=%v", chassis, blade)
	}

	// Both devices share the device type, so each self-heals its own bay; the
	// one that matters is the bay on the chassis.
	var chassisBay client.Object
	for _, bay := range f.objects("dcim", "device-bays") {
		if utils.GetIDFromObject(bay["device"]) == utils.GetIDFromObject(chassis) {
			chassisBay = bay
		}
	}
	if chassisBay == nil {
		t.Fatalf("no device bay self-healed onto the chassis: %+v", f.objects("dcim", "device-bays"))
	}
	if got := utils.GetIDFromObject(chassisBay["installed_device"]); got != utils.GetIDFromObject(blade) {
		t.Errorf("chassis bay installed_device = %v, expected the child %d", chassisBay["installed_device"], utils.GetIDFromObject(blade))
	}
}

// TestReconcileDevicesReportsParentCycle guards the error path end to end: a
// cycle must stop the run rather than reconcile an arbitrary subset.
func TestReconcileDevicesReportsParentCycle(t *testing.T) {
	f, c := newFakeNetBox(t)
	seedDeviceFoundation(t, f, c)

	devices := []*models.DeviceConfig{dev("a", "b"), dev("b", "a")}

	err := NewDeviceReconciler(c).ReconcileDevices(devices)
	if err == nil {
		t.Fatal("ReconcileDevices() = nil, want a cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %q, expected it to name the problem as a cycle", err)
	}
	if muts := f.mutationLog(); len(muts) != 0 {
		t.Errorf("cycle sent %d mutating request(s), expected 0: %+v", len(muts), muts)
	}
}
