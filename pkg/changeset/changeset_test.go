// SPDX-License-Identifier: Apache-2.0

package changeset

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// dataset builds the small inventory the tests vary: two servers of one
// type cabled to a switch of another, a chassis with a blade, and a VM.
func dataset() *loader.Dataset {
	return &loader.Dataset{
		Tags:  []*models.Tag{{Name: "Production", Slug: "production"}},
		Sites: []*models.Site{{Name: "Berlin", Slug: "berlin"}},
		Racks: []*models.Rack{{Name: "A01", Slug: "a01", SiteSlug: "berlin"}},
		VLANs: []*models.VLAN{{Name: "mgmt", VID: 10, SiteSlug: "berlin"}},
		DeviceTypes: []*models.DeviceType{
			{Model: "Server", Slug: "server"},
			{Model: "Switch", Slug: "switch"},
			{Model: "Chassis", Slug: "chassis"},
		},
		ModuleTypes: []*models.ModuleType{{Model: "PSU 750", Slug: "psu-750"}},
		Clusters:    []*models.Cluster{{Name: "prod"}},
		Devices: []*models.DeviceConfig{
			{Name: "sw-01", SiteSlug: "berlin", DeviceTypeSlug: "switch"},
			{
				Name: "srv-01", SiteSlug: "berlin", DeviceTypeSlug: "server",
				Modules: []models.ModuleConfig{{Name: "PSU1", ModuleTypeSlug: "psu-750"}},
				Interfaces: []models.InterfaceConfig{
					{Name: "eth0", IP: &models.IPConfig{Address: "10.0.0.1/24"}},
					{Name: "eth1", Link: &models.LinkConfig{PeerDevice: "sw-01", PeerPort: "Eth1"}},
				},
			},
			{Name: "srv-02", SiteSlug: "berlin", DeviceTypeSlug: "server"},
			{Name: "chassis-01", SiteSlug: "berlin", DeviceTypeSlug: "chassis"},
			{Name: "blade-01", SiteSlug: "berlin", DeviceTypeSlug: "server", ParentDevice: "chassis-01", DeviceBay: "1"},
		},
		VMs: []*models.VMConfig{
			{Name: "web-01", Cluster: "prod", Interfaces: []models.VMInterfaceConfig{{Name: "eth0"}}},
			{Name: "solo-01"},
		},
	}
}

func device(ds *loader.Dataset, name string) *models.DeviceConfig {
	for _, d := range ds.Devices {
		if d.Name == name {
			return d
		}
	}
	panic("no device " + name)
}

func names(set map[string]bool) []string {
	var out []string
	for name := range set {
		out = append(out, name)
	}
	// Small sets; order them for a stable comparison.
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func why(res Result, kind, name string) string {
	for _, e := range res.Entries {
		if e.Kind == kind && e.Name == name {
			return e.Why
		}
	}
	return ""
}

func TestIdenticalDatasetsChangeNothing(t *testing.T) {
	res := Compare(dataset(), dataset(), nil)
	if !res.Empty() || len(res.Entries) != 0 || len(res.Removed) != 0 {
		t.Fatalf("Compare() of equal datasets = %+v", res)
	}
}

// TestLoadingTwiceChangesNothing guards the property the whole comparison
// rests on: loading the same files twice yields equal datasets. A loader that
// let map iteration order into a slice would select devices at random.
func TestLoadingTwiceChangesNothing(t *testing.T) {
	root := filepath.Join("..", "..", "example")
	load := func() *loader.Dataset {
		ds, err := loader.NewDataLoader(root, utils.NewDiscardLogger()).LoadDataset(loader.DatasetOptions{
			DeviceTypeLibrary: filepath.Join(root, "definitions", "device_type_library"),
			ModuleTypeLibrary: filepath.Join(root, "definitions", "module_type_library"),
		})
		if err != nil {
			t.Fatal(err)
		}
		return &ds
	}
	for i := 0; i < 5; i++ {
		res := Compare(load(), load(), nil)
		if !res.Empty() || len(res.Removed) != 0 {
			t.Fatalf("two loads of the example data differ: %+v", res.Entries)
		}
	}
}

func TestChangedDeviceIsSelectedAlone(t *testing.T) {
	base, head := dataset(), dataset()
	device(head, "srv-02").Serial = "ABC"
	device(head, "srv-02").Position = 4
	head.Origins = map[any]loader.Origin{device(head, "srv-02"): {File: "inventory/servers.yaml", Line: 9}}

	res := Compare(base, head, map[string]bool{"inventory/servers.yaml": true})
	if got := names(res.Devices); !reflect.DeepEqual(got, []string{"srv-02"}) {
		t.Fatalf("devices = %v, want only srv-02", got)
	}
	if len(res.Groups) != 0 || len(res.VMs) != 0 {
		t.Errorf("groups %v and VMs %v selected for a device edit", res.Groups, res.VMs)
	}
	e := res.Entries[0]
	if e.Why != "changed: position, serial" || e.File != "inventory/servers.yaml" || !e.Touched {
		t.Errorf("entry = %+v", e)
	}
}

// A device whose own file is as it was, and whose resolved values differ
// anyway, says so: that is the case a file-based selection would miss.
func TestChangeResolvedFromElsewhereIsNotTouched(t *testing.T) {
	base, head := dataset(), dataset()
	device(head, "srv-02").RoleSlug = "from-defaults"
	head.Origins = map[any]loader.Origin{device(head, "srv-02"): {File: "inventory/servers.yaml"}}

	res := Compare(base, head, map[string]bool{"inventory/other.yaml": true})
	if len(res.Entries) != 1 || res.Entries[0].Touched {
		t.Fatalf("entries = %+v, want one untouched entry", res.Entries)
	}
}

func TestNewAndRenamedDevices(t *testing.T) {
	base, head := dataset(), dataset()
	head.Devices = append(head.Devices, &models.DeviceConfig{Name: "srv-03", DeviceTypeSlug: "server"})
	renamed := device(head, "srv-02")
	renamed.Name, renamed.RenameFrom = "srv-two", "srv-02"

	res := Compare(base, head, nil)
	if got := why(res, "device", "srv-03"); got != "new" {
		t.Errorf("srv-03: %q", got)
	}
	if got := why(res, "device", "srv-two"); got != "renamed from srv-02" {
		t.Errorf("srv-two: %q", got)
	}
	if len(res.Removed) != 0 {
		t.Errorf("a rename was reported as a removal: %v", res.Removed)
	}
}

func TestChangedDeviceTypeSelectsItsDevices(t *testing.T) {
	base, head := dataset(), dataset()
	head.DeviceTypes[0].Comments = "now with comments"

	res := Compare(base, head, nil)
	if !res.Groups[GroupDeviceTypes] || len(res.Groups) != 1 {
		t.Errorf("groups = %v, want device-types only", res.Groups)
	}
	// Every device of type "server", and nothing of the other types.
	if got := names(res.Devices); !reflect.DeepEqual(got, []string{"blade-01", "srv-01", "srv-02"}) {
		t.Errorf("devices = %v", got)
	}
	if got := why(res, "device", "srv-02"); got != "its device type server changed" {
		t.Errorf("srv-02: %q", got)
	}
}

func TestChangedModuleTypeSelectsItsDevices(t *testing.T) {
	base, head := dataset(), dataset()
	head.ModuleTypes[0].Description = "changed"

	res := Compare(base, head, nil)
	if got := names(res.Devices); !reflect.DeepEqual(got, []string{"srv-01"}) {
		t.Errorf("devices = %v, want srv-01 (the one with a PSU 750)", got)
	}
}

func TestChangedParentSelectsItsChildren(t *testing.T) {
	base, head := dataset(), dataset()
	device(head, "chassis-01").Serial = "CH-1"

	res := Compare(base, head, nil)
	if got := names(res.Devices); !reflect.DeepEqual(got, []string{"blade-01", "chassis-01"}) {
		t.Errorf("devices = %v", got)
	}
	if got := why(res, "device", "blade-01"); !strings.Contains(got, "parent device chassis-01") {
		t.Errorf("blade-01: %q", got)
	}
}

func TestChangedPeerSelectsTheDeviceDeclaringTheCable(t *testing.T) {
	base, head := dataset(), dataset()
	device(head, "sw-01").Serial = "SW-1"

	res := Compare(base, head, nil)
	// srv-01 declares the cable to sw-01. srv-02 has no cable to it.
	if got := names(res.Devices); !reflect.DeepEqual(got, []string{"srv-01", "sw-01"}) {
		t.Errorf("devices = %v", got)
	}
	if got := why(res, "device", "srv-01"); got != "it declares a cable to sw-01, which changed" {
		t.Errorf("srv-01: %q", got)
	}
}

func TestChangedPanelAlongAPathSelectsTheEndpoint(t *testing.T) {
	base, head := dataset(), dataset()
	for _, ds := range []*loader.Dataset{base, head} {
		ds.Devices = append(ds.Devices, &models.DeviceConfig{Name: "pp-01", DeviceTypeSlug: "panel"})
		device(ds, "srv-01").Interfaces[1].Link.Via = []models.ViaHop{{Device: "pp-01", Port: "1"}}
	}
	device(head, "pp-01").Serial = "PP"

	res := Compare(base, head, nil)
	if !res.Devices["srv-01"] {
		t.Errorf("devices = %v; srv-01's cable runs through the changed panel", names(res.Devices))
	}
}

func TestDefinitionChangeSelectsItsGroupOnly(t *testing.T) {
	base, head := dataset(), dataset()
	head.VLANs[0].Description = "edited"
	head.Sites = append(head.Sites, &models.Site{Name: "Munich", Slug: "munich"})

	res := Compare(base, head, nil)
	want := map[string]bool{GroupFoundation: true, GroupNetwork: true}
	if !reflect.DeepEqual(res.Groups, want) {
		t.Errorf("groups = %v, want %v", res.Groups, want)
	}
	if len(res.Devices) != 0 {
		t.Errorf("devices selected for a VLAN and a site change: %v", names(res.Devices))
	}
	vlan := fmt.Sprintf("berlin/mgmt (%d)", base.VLANs[0].VID)
	if got := why(res, "VLAN", vlan); got != "changed: description" {
		t.Errorf("VLAN: %q", got)
	}
	if got := why(res, "site", "munich"); got != "new" {
		t.Errorf("site: %q", got)
	}
}

func TestChangedClusterSelectsItsVMs(t *testing.T) {
	base, head := dataset(), dataset()
	head.Clusters[0].Description = "changed"

	res := Compare(base, head, nil)
	if !res.Groups[GroupClusters] {
		t.Errorf("groups = %v", res.Groups)
	}
	if got := names(res.VMs); !reflect.DeepEqual(got, []string{"web-01"}) {
		t.Errorf("VMs = %v, want web-01 (solo-01 has no cluster)", got)
	}
}

func TestChangedVM(t *testing.T) {
	base, head := dataset(), dataset()
	head.VMs[1].Memory = 4096

	res := Compare(base, head, nil)
	if got := names(res.VMs); !reflect.DeepEqual(got, []string{"solo-01"}) {
		t.Errorf("VMs = %v", got)
	}
	if len(res.Devices) != 0 || len(res.Groups) != 0 {
		t.Errorf("a VM edit selected devices %v, groups %v", names(res.Devices), res.Groups)
	}
}

func TestRemovedObjectsAreListedNotSelected(t *testing.T) {
	base, head := dataset(), dataset()
	// A whole device, a VM, a rack, a tag ...
	head.Devices = head.Devices[:2] // sw-01, srv-01
	head.VMs = head.VMs[:1]
	head.Racks = nil
	head.Tags = nil
	// ... and parts of a device that stays.
	srv := device(head, "srv-01")
	srv.Modules = nil
	srv.Interfaces = []models.InterfaceConfig{{Name: "eth0"}}

	res := Compare(base, head, nil)
	want := []string{
		"VM solo-01",
		"address 10.0.0.1/24 of srv-01",
		"cable eth1 <-> sw-01[Eth1] of srv-01",
		"device blade-01",
		"device chassis-01",
		"device srv-02",
		"interface eth1 of srv-01",
		"module PSU1 of srv-01",
		"rack berlin/a01",
	}
	if !reflect.DeepEqual(res.Removed, want) {
		t.Errorf("removed =\n  %s\nwant\n  %s", strings.Join(res.Removed, "\n  "), strings.Join(want, "\n  "))
	}
	// Tags are never pruned, so a dropped tag is not announced as a deletion.
	for _, r := range res.Removed {
		if strings.HasPrefix(r, "tag ") {
			t.Errorf("a tag was listed for deletion: %s", r)
		}
	}
	// srv-01 changed (it lost parts); the removed devices are not selected.
	if got := names(res.Devices); !reflect.DeepEqual(got, []string{"srv-01"}) {
		t.Errorf("devices = %v", got)
	}
	if res.Groups[GroupFoundation] {
		t.Error("removing a rack and a tag selected the foundation group; nothing was added or changed in it")
	}
}

func TestRenamedInterfaceIsNotARemoval(t *testing.T) {
	base, head := dataset(), dataset()
	device(head, "srv-01").Interfaces[0].Name = "mgmt0"
	device(head, "srv-01").Interfaces[0].RenameFrom = "eth0"

	res := Compare(base, head, nil)
	for _, r := range res.Removed {
		if strings.Contains(r, "interface eth0") {
			t.Errorf("a renamed interface was listed for deletion: %v", res.Removed)
		}
	}
}

// Two declarations under one identity are compared as a pair, so a change to
// either is seen and neither hides the other.
func TestDuplicateIdentities(t *testing.T) {
	base, head := dataset(), dataset()
	for _, ds := range []*loader.Dataset{base, head} {
		ds.Sites = append(ds.Sites, &models.Site{Name: "Berlin again", Slug: "berlin"})
	}
	if res := Compare(base, head, nil); !res.Empty() {
		t.Fatalf("equal duplicates differ: %+v", res.Entries)
	}
	head.Sites[1].Description = "second one changed"
	if res := Compare(base, head, nil); !res.Groups[GroupFoundation] {
		t.Error("a change to the second declaration went unseen")
	}
	head.Sites = head.Sites[:1]
	res := Compare(base, head, nil)
	if got := why(res, "site", "berlin"); got != "declared a different number of times" {
		t.Errorf("site berlin: %q", got)
	}
}

func TestDifferingFieldsOfOtherValues(t *testing.T) {
	if got := differingFields("a", "a"); got != nil {
		t.Errorf("equal strings: %v", got)
	}
	if got := differingFields("a", "b"); !reflect.DeepEqual(got, []string{"value"}) {
		t.Errorf("different strings: %v", got)
	}
}

// A VLAN is the vid within its site, as the reconciler looks it up: a new
// name or group is a change to the same VLAN, a new vid is another VLAN
// unless rename_from names the old one.
func TestVLANIsIdentifiedByItsVID(t *testing.T) {
	base, head := dataset(), dataset()
	old := base.VLANs[0].VID
	head.VLANs[0].Name = "management"
	head.VLANs[0].GroupSlug = "some-group"
	res := Compare(base, head, nil)
	if got := why(res, "VLAN", fmt.Sprintf("berlin/management (%d)", old)); got != "changed: name, group" && got != "changed: name, group_slug" {
		t.Errorf("renamed and regrouped VLAN: %q (entries %+v)", got, res.Entries)
	}
	if len(res.Removed) != 0 {
		t.Errorf("removed = %v, want nothing: it is the same VLAN", res.Removed)
	}

	head = dataset()
	head.VLANs[0].VID = old + 1
	res = Compare(base, head, nil)
	if got := why(res, "VLAN", fmt.Sprintf("berlin/mgmt (%d)", old+1)); got != "new" {
		t.Errorf("VLAN with another vid: %q", got)
	}
	if len(res.Removed) != 1 || !strings.Contains(res.Removed[0], fmt.Sprintf("(%d)", old)) {
		t.Errorf("removed = %v, want the VLAN with the old vid", res.Removed)
	}

	head.VLANs[0].RenameFrom = strconv.Itoa(old)
	res = Compare(base, head, nil)
	if got := why(res, "VLAN", fmt.Sprintf("berlin/mgmt (%d)", old+1)); !strings.HasPrefix(got, "renamed from") {
		t.Errorf("VLAN renumbered with rename_from: %q", got)
	}
	if len(res.Removed) != 0 {
		t.Errorf("removed = %v, want nothing after a rename", res.Removed)
	}
}

// NetBox finds a device by name and site and a VM by name and cluster. Moving
// one leaves the old object behind for a prune, which the plan has to say.
func TestMoveToAnotherSiteOrClusterIsAnnouncedAsRemoval(t *testing.T) {
	base, head := dataset(), dataset()
	device(head, "srv-01").SiteSlug = "munich"
	base.VMs = []*models.VMConfig{{Name: "vm-1", Cluster: "c1"}, {Name: "vm-2"}}
	head.VMs = []*models.VMConfig{{Name: "vm-1", Cluster: "c2"}, {Name: "vm-2"}}

	res := Compare(base, head, nil)
	if !res.Devices["srv-01"] || !res.VMs["vm-1"] || res.VMs["vm-2"] {
		t.Errorf("selected devices %v, VMs %v", names(res.Devices), names(res.VMs))
	}
	joined := strings.Join(res.Removed, "\n")
	for _, want := range []string{"device srv-01 at site berlin", "VM vm-1 in cluster c1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("removed lacks %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "vm-2") {
		t.Errorf("an unmoved VM is listed as removed:\n%s", joined)
	}
}

// A MAC address is an object of its own in NetBox. One the YAML replaced or
// dropped waits for a prune, so the plan lists it.
func TestChangedVMMACIsAnnouncedAsRemoval(t *testing.T) {
	vms := func(mac string) []*models.VMConfig {
		return []*models.VMConfig{{Name: "vm-1", Interfaces: []models.VMInterfaceConfig{{Name: "eth0", MACAddress: mac}}}}
	}
	base, head := dataset(), dataset()
	base.VMs, head.VMs = vms("02:00:00:aa:bb:01"), vms("02:00:00:AA:BB:02")
	res := Compare(base, head, nil)
	if !res.VMs["vm-1"] {
		t.Error("the VM whose MAC changed is not selected")
	}
	if joined := strings.Join(res.Removed, "\n"); !strings.Contains(joined, "MAC address 02:00:00:AA:BB:01 of vm-1") {
		t.Errorf("removed = %q, want the old MAC address", joined)
	}

	head.VMs = vms("")
	if joined := strings.Join(Compare(base, head, nil).Removed, "\n"); !strings.Contains(joined, "MAC address 02:00:00:AA:BB:01") {
		t.Errorf("a dropped MAC is not listed: %q", joined)
	}
}
