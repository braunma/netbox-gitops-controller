package reconciler

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// seedDeviceFoundation seeds a site, a role, and a device type, loads the
// caches, and returns their IDs.
func seedDeviceFoundation(t *testing.T, f *fakeNetBox, c *client.NetBoxClient) (siteID, roleID, deviceTypeID int) {
	t.Helper()
	site := f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	role := f.seed("dcim", "device-roles", client.Object{"name": "Switch", "slug": "switch"})
	dt := f.seed("dcim", "device-types", client.Object{"model": "C9300", "slug": "c9300"})
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	return utils.GetIDFromObject(site), utils.GetIDFromObject(role), utils.GetIDFromObject(dt)
}

func TestReconcileDevicesFullFlow(t *testing.T) {
	f, c := newFakeNetBox(t)
	siteID, roleID, deviceTypeID := seedDeviceFoundation(t, f, c)
	rack := f.seed("dcim", "racks", client.Object{"name": "R01", "site": siteID})
	vlan := f.seed("ipam", "vlans", client.Object{"name": "mgmt", "vid": 100, "site": siteID})
	if err := c.Cache().LoadSite("berlin-dc"); err != nil {
		t.Fatalf("LoadSite() error = %v", err)
	}

	devices := []*models.DeviceConfig{
		{
			Name: "sw-01", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "switch",
			RackSlug: "R01", Position: 10, Face: "front",
			Interfaces: []models.InterfaceConfig{{
				Name: "eth0", Type: "1000base-t", Enabled: true, MTU: 9000,
				Mode: "access", UntaggedVLAN: "mgmt",
				IP:          &models.IPConfig{Address: "10.0.0.1/24"},
				AddressRole: "primary",
				Link:        &models.LinkConfig{PeerDevice: "sw-02", PeerPort: "eth0", CableType: "cat6"},
			}},
		},
		{
			Name: "sw-02", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "switch",
			Interfaces: []models.InterfaceConfig{{
				Name: "eth0", Type: "1000base-t", Enabled: true,
				Link: &models.LinkConfig{PeerDevice: "sw-01", PeerPort: "eth0", CableType: "cat6"},
			}},
		},
	}

	dr := NewDeviceReconciler(c)
	if err := dr.ReconcileDevices(devices); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}

	// Devices: resolved references, rack placement, defaulted status.
	stored := f.objects("dcim", "devices")
	if len(stored) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(stored))
	}
	var sw01 client.Object
	for _, d := range stored {
		if d["name"] == "sw-01" {
			sw01 = d
		}
	}
	if sw01 == nil {
		t.Fatal("device sw-01 not created")
	}
	if got := utils.GetIDFromObject(sw01["role"]); got != roleID {
		t.Errorf("sw-01 role = %d, expected %d", got, roleID)
	}
	if got := utils.GetIDFromObject(sw01["device_type"]); got != deviceTypeID {
		t.Errorf("sw-01 device_type = %d, expected %d", got, deviceTypeID)
	}
	if got := utils.GetIDFromObject(sw01["rack"]); got != utils.GetIDFromObject(rack) {
		t.Errorf("sw-01 rack = %d, expected site-scoped rack ID %d", got, utils.GetIDFromObject(rack))
	}
	if got := utils.GetIDFromObject(sw01["position"]); got != 10 {
		t.Errorf("sw-01 position = %v, expected 10", sw01["position"])
	}
	if sw01["status"] != "active" {
		t.Errorf("sw-01 status = %v, expected default \"active\"", sw01["status"])
	}

	// Interfaces: site-scoped VLAN resolution.
	var eth0 client.Object
	for _, iface := range f.objects("dcim", "interfaces") {
		if utils.GetIDFromObject(iface["device"]) == utils.GetIDFromObject(sw01) {
			eth0 = iface
		}
	}
	if eth0 == nil {
		t.Fatal("interface eth0 on sw-01 not created")
	}
	if got := utils.GetIDFromObject(eth0["untagged_vlan"]); got != utils.GetIDFromObject(vlan) {
		t.Errorf("eth0 untagged_vlan = %d, expected site-scoped VLAN ID %d", got, utils.GetIDFromObject(vlan))
	}

	// IP address: assigned to the interface and set as primary on the device.
	ips := f.objects("ipam", "ip-addresses")
	if len(ips) != 1 {
		t.Fatalf("expected 1 IP address, got %d", len(ips))
	}
	if got := utils.GetIDFromObject(ips[0]["assigned_object_id"]); got != utils.GetIDFromObject(eth0) {
		t.Errorf("IP assigned_object_id = %d, expected interface ID %d", got, utils.GetIDFromObject(eth0))
	}
	if got := utils.GetIDFromObject(sw01["primary_ip4"]); got != utils.GetIDFromObject(ips[0]) {
		t.Errorf("sw-01 primary_ip4 = %v, expected IP ID %d", sw01["primary_ip4"], utils.GetIDFromObject(ips[0]))
	}

	// Cables: both directions were queued, but the pair is reconciled once.
	if got := len(f.objects("dcim", "cables")); got != 1 {
		t.Errorf("expected exactly 1 cable for the bidirectional link definition, got %d", got)
	}

	// Second run: everything is found and diffed to a no-op. setPrimaryIP now
	// skips the PATCH when the device already points at the IP, so an unchanged
	// inventory produces zero mutations.
	f.resetMutations()
	if err := NewDeviceReconciler(c).ReconcileDevices(devices); err != nil {
		t.Fatalf("ReconcileDevices() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileDevicesErrorsOnUnresolvedReferences(t *testing.T) {
	f, c := newFakeNetBox(t)
	dr := NewDeviceReconciler(c)

	device := &models.DeviceConfig{
		Name: "sw-01", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "switch",
	}

	err := dr.ReconcileDevices([]*models.DeviceConfig{device})
	if err == nil || !strings.Contains(err.Error(), "site berlin-dc not found") {
		t.Errorf("error = %v, expected unknown site error", err)
	}

	f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	err = dr.ReconcileDevices([]*models.DeviceConfig{device})
	if err == nil || !strings.Contains(err.Error(), "role switch not found") {
		t.Errorf("error = %v, expected unknown role error", err)
	}

	f.seed("dcim", "device-roles", client.Object{"name": "Switch", "slug": "switch"})
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}
	err = dr.ReconcileDevices([]*models.DeviceConfig{device})
	if err == nil || !strings.Contains(err.Error(), "device type c9300 not found") {
		t.Errorf("error = %v, expected unknown device type error", err)
	}
}

// TestReconcileDeviceDryRunResolvesSameRunDependencies reproduces a feature
// branch that adds a new site, role and device type *together with* a device
// that references them, and validates it with --dry-run before merging. None
// of the dependencies exist in NetBox yet, so this used to abort with
// "site ... not found". They are declared in the same run, so the device must
// validate (be planned) instead, while nothing is written to NetBox.
func TestReconcileDeviceDryRunResolvesSameRunDependencies(t *testing.T) {
	f, c := newFakeNetBox(t)
	c.SetDryRun(true)

	// Empty NetBox, exactly as a branch pipeline sees it before the apply.
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	fr := NewFoundationReconciler(c)
	if err := fr.ReconcileSites([]*models.Site{{Name: "Berlin DC", Slug: "berlin-dc", Status: "active"}}); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}
	if err := fr.ReconcileRoles([]*models.Role{{Name: "Switch", Slug: "switch"}}); err != nil {
		t.Fatalf("ReconcileRoles() error = %v", err)
	}
	dtr := NewDeviceTypeReconciler(c)
	if err := dtr.ReconcileDeviceTypes([]*models.DeviceType{{Model: "C9300", Slug: "c9300", Manufacturer: "Cisco"}}); err != nil {
		t.Fatalf("ReconcileDeviceTypes() error = %v", err)
	}

	device := &models.DeviceConfig{
		Name: "sw-01", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "switch",
	}
	if err := NewDeviceReconciler(c).ReconcileDevices([]*models.DeviceConfig{device}); err != nil {
		t.Fatalf("ReconcileDevices() in dry-run = %v; a device referencing a same-run site/role/device-type must validate, not abort", err)
	}

	// Dry-run must not have written anything to NetBox.
	if got := len(f.objects("dcim", "devices")); got != 0 {
		t.Errorf("dry-run wrote %d device(s), expected 0", got)
	}
	if muts := f.mutationLog(); len(muts) != 0 {
		t.Errorf("dry-run sent %d mutating request(s), expected 0: %+v", len(muts), muts)
	}
}

// TestReconcileDeviceDryRunSkipsUnresolvedParentInBay covers a branch that
// adds a parent chassis and a child device installed into its bay in one go.
// In dry-run the parent is never created, so the child's parent/bay cannot be
// resolved; this must be skipped with a warning rather than aborting the run.
// TestDryRunFullOrderingResolvesNewSite mirrors main.go's runSync ordering:
// the foundation phase reconciles a brand-new site, then the devices phase
// calls Cache().LoadSite() (which itself hard-fails with "site not found")
// before reconciling the device. Both steps must succeed in dry-run against an
// empty NetBox, proving the orchestration path — not just the reconciler — is
// covered for a branch that adds a site and a device in it together.
func TestDryRunFullOrderingResolvesNewSite(t *testing.T) {
	f, c := newFakeNetBox(t)
	c.SetDryRun(true)
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	// Phase 1 (foundation): declare the site, role and device type.
	fr := NewFoundationReconciler(c)
	if err := fr.ReconcileSites([]*models.Site{{Name: "Berlin DC", Slug: "berlin-dc", Status: "active"}}); err != nil {
		t.Fatalf("ReconcileSites() error = %v", err)
	}
	if err := fr.ReconcileRoles([]*models.Role{{Name: "Switch", Slug: "switch"}}); err != nil {
		t.Fatalf("ReconcileRoles() error = %v", err)
	}
	if err := NewDeviceTypeReconciler(c).ReconcileDeviceTypes([]*models.DeviceType{{Model: "C9300", Slug: "c9300", Manufacturer: "Cisco"}}); err != nil {
		t.Fatalf("ReconcileDeviceTypes() error = %v", err)
	}

	// Phase 3 (devices) prologue: main.go warms the site cache here. This used
	// to abort with "site berlin-dc not found" for a not-yet-applied site.
	if err := c.Cache().LoadSite("berlin-dc"); err != nil {
		t.Fatalf("LoadSite() in dry-run = %v; a site declared this run must not abort the devices phase", err)
	}

	device := &models.DeviceConfig{Name: "sw-01", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "switch"}
	if err := NewDeviceReconciler(c).ReconcileDevices([]*models.DeviceConfig{device}); err != nil {
		t.Fatalf("ReconcileDevices() in dry-run = %v", err)
	}
	if muts := f.mutationLog(); len(muts) != 0 {
		t.Errorf("dry-run sent %d mutating request(s), expected 0: %+v", len(muts), muts)
	}
}

func TestReconcileDeviceDryRunSkipsUnresolvedParentInBay(t *testing.T) {
	f, c := newFakeNetBox(t)
	siteID, roleID, deviceTypeID := seedDeviceFoundation(t, f, c)
	_, _, _ = siteID, roleID, deviceTypeID
	c.SetDryRun(true)

	devices := []*models.DeviceConfig{
		// New parent chassis: in dry-run it is planned but not created.
		{Name: "chassis-01", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "switch"},
		// Child placed into the not-yet-created parent's bay.
		{
			Name: "node-01", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "switch",
			ParentDevice: "chassis-01", DeviceBay: "bay-1",
		},
	}
	if err := NewDeviceReconciler(c).ReconcileDevices(devices); err != nil {
		t.Fatalf("ReconcileDevices() in dry-run = %v; an unresolved same-run parent must be skipped, not abort", err)
	}
	if muts := f.mutationLog(); len(muts) != 0 {
		t.Errorf("dry-run sent %d mutating request(s), expected 0: %+v", len(muts), muts)
	}
}

func TestReconcileDevicesInstallsChildIntoBay(t *testing.T) {
	f, c := newFakeNetBox(t)
	siteID, roleID, deviceTypeID := seedDeviceFoundation(t, f, c)
	rack := f.seed("dcim", "racks", client.Object{"name": "R01", "site": siteID})
	rackID := utils.GetIDFromObject(rack)

	// Parent chassis with a free bay, as NetBox would return them.
	parent := f.seed("dcim", "devices", client.Object{
		"name": "chassis-01", "site": siteID, "role": roleID, "device_type": deviceTypeID,
		"rack": map[string]interface{}{"id": rackID},
	})
	parentID := utils.GetIDFromObject(parent)
	bay := f.seed("dcim", "device-bays", client.Object{"name": "bay-1", "device": parentID})

	child := &models.DeviceConfig{
		Name: "node-01", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "switch",
		ParentDevice: "chassis-01", DeviceBay: "bay-1",
	}
	if err := NewDeviceReconciler(c).ReconcileDevices([]*models.DeviceConfig{child}); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}

	muts := f.mutationLog()
	if len(muts) != 3 {
		t.Fatalf("expected 3 mutations (create child, detach from rack, install into bay), got %d: %+v", len(muts), muts)
	}

	// The child inherits the parent's rack on creation...
	if muts[0].method != "POST" || utils.GetIDFromObject(muts[0].body["rack"]) != rackID {
		t.Errorf("child create = %s body %v, expected POST with parent's rack %d", muts[0].method, muts[0].body, rackID)
	}
	if _, hasPos := muts[0].body["position"]; hasPos {
		t.Errorf("child create body %v: bay-mounted devices must not set a position", muts[0].body)
	}

	// ...is then detached from the rack so it can enter the bay...
	if muts[1].method != "PATCH" || !strings.Contains(muts[1].path, "/api/dcim/devices/") {
		t.Errorf("second mutation = %s %s, expected device detach PATCH", muts[1].method, muts[1].path)
	}
	if rackVal, ok := muts[1].body["rack"]; !ok || rackVal != nil {
		t.Errorf("detach PATCH body = %v, expected rack to be cleared", muts[1].body)
	}

	// ...and finally the bay is updated to hold the child.
	if muts[2].method != "PATCH" || !strings.Contains(muts[2].path, "/api/dcim/device-bays/") {
		t.Errorf("third mutation = %s %s, expected device-bay install PATCH", muts[2].method, muts[2].path)
	}
	bayObj := f.objects("dcim", "device-bays")[0]
	var childObj client.Object
	for _, d := range f.objects("dcim", "devices") {
		if d["name"] == "node-01" {
			childObj = d
		}
	}
	if childObj == nil {
		t.Fatal("child device not created")
	}
	if got := utils.GetIDFromObject(bayObj["installed_device"]); got != utils.GetIDFromObject(childObj) {
		t.Errorf("bay installed_device = %v, expected child ID %d", bayObj["installed_device"], utils.GetIDFromObject(childObj))
	}
	_ = bay
}

func TestReconcileDevicesBayInstallIsIdempotent(t *testing.T) {
	f, c := newFakeNetBox(t)
	siteID, roleID, deviceTypeID := seedDeviceFoundation(t, f, c)

	// Parent chassis with no rack (as the Isilon chassis is modelled), so the
	// child's device Apply produces no changes and only the bay placement is
	// in play.
	parent := f.seed("dcim", "devices", client.Object{
		"name": "chassis-01", "site": siteID, "role": roleID, "device_type": deviceTypeID,
	})
	parentID := utils.GetIDFromObject(parent)
	f.seed("dcim", "device-bays", client.Object{"name": "bay-1", "device": parentID})

	child := &models.DeviceConfig{
		Name: "node-01", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "switch",
		ParentDevice: "chassis-01", DeviceBay: "bay-1",
	}

	// First run installs the child into the bay.
	if err := NewDeviceReconciler(c).ReconcileDevices([]*models.DeviceConfig{child}); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}

	// Second run must be a no-op. Before the fix, installDeviceIntoBay checked a
	// non-existent "device_bay" field on the device, so it re-detached and
	// re-installed every run, inflating the change count.
	f.resetMutations()
	if err := NewDeviceReconciler(c).ReconcileDevices([]*models.DeviceConfig{child}); err != nil {
		t.Fatalf("ReconcileDevices() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileDevicesSelfHealsDeviceBays(t *testing.T) {
	f, c := newFakeNetBox(t)
	_, _, deviceTypeID := seedDeviceFoundation(t, f, c)
	f.seed("dcim", "device-bay-templates", client.Object{
		"name": "bay-1", "label": "Bay 1", "device_type": deviceTypeID,
	})

	device := &models.DeviceConfig{
		Name: "chassis-01", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "switch",
	}
	if err := NewDeviceReconciler(c).ReconcileDevices([]*models.DeviceConfig{device}); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}

	bays := f.objects("dcim", "device-bays")
	if len(bays) != 1 {
		t.Fatalf("expected missing device bay to be self-healed from the template, got %d bays", len(bays))
	}
	if bays[0]["name"] != "bay-1" || bays[0]["label"] != "Bay 1" {
		t.Errorf("created bay = %v, expected name and label from the template", bays[0])
	}

	// Second run: the bay exists, nothing is recreated.
	f.resetMutations()
	if err := NewDeviceReconciler(c).ReconcileDevices([]*models.DeviceConfig{device}); err != nil {
		t.Fatalf("ReconcileDevices() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileDevicesInstallsModules(t *testing.T) {
	f, c := newFakeNetBox(t)
	siteID, roleID, deviceTypeID := seedDeviceFoundation(t, f, c)
	moduleType := f.seed("dcim", "module-types", client.Object{"model": "H200", "slug": "h200"})
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	// Pre-seed the device and its module bay: in real NetBox the bay is
	// instantiated from the module bay template on device creation.
	device := f.seed("dcim", "devices", client.Object{
		"name": "srv-01", "site": siteID, "role": roleID, "device_type": deviceTypeID,
		"status": "active", "tags": []interface{}{map[string]interface{}{"id": c.ManagedTagID()}},
	})
	deviceID := utils.GetIDFromObject(device)
	bay := f.seed("dcim", "module-bays", client.Object{"name": "gpu-1", "device": deviceID})

	config := &models.DeviceConfig{
		Name: "srv-01", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "switch",
		Status: "active",
		Modules: []models.ModuleConfig{
			{Name: "gpu-1", ModuleTypeSlug: "h200"},
			{Name: "gpu-1", ModuleTypeSlug: "unknown-type"}, // unknown type: skipped
			{Name: "gpu-9", ModuleTypeSlug: "h200"},         // no such bay: skipped
		},
	}
	if err := NewDeviceReconciler(c).ReconcileDevices([]*models.DeviceConfig{config}); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}

	modules := f.objects("dcim", "modules")
	if len(modules) != 1 {
		t.Fatalf("expected exactly 1 module (others skipped), got %d", len(modules))
	}
	if got := utils.GetIDFromObject(modules[0]["module_bay"]); got != utils.GetIDFromObject(bay) {
		t.Errorf("module module_bay = %d, expected bay ID %d", got, utils.GetIDFromObject(bay))
	}
	if got := utils.GetIDFromObject(modules[0]["module_type"]); got != utils.GetIDFromObject(moduleType) {
		t.Errorf("module module_type = %d, expected %d", got, utils.GetIDFromObject(moduleType))
	}
	if serial, ok := modules[0]["serial"]; !ok || serial != "" {
		t.Errorf("module serial = %v, expected explicit empty string (avoids NetBox 400)", serial)
	}
}

func TestReconcileDevicesRoleBasedCableEndpoints(t *testing.T) {
	f, c := newFakeNetBox(t)
	siteID, _, _ := seedDeviceFoundation(t, f, c)
	ppRole := f.seed("dcim", "device-roles", client.Object{"name": "Patch Panel", "slug": "patch-panel"})
	srvRole := f.seed("dcim", "device-roles", client.Object{"name": "Server", "slug": "server"})
	if err := c.Cache().LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal() error = %v", err)
	}

	// Peer patch panel as NetBox would return it: nested role with slug.
	pp01 := f.seed("dcim", "devices", client.Object{
		"name": "pp-01", "site": siteID,
		"role": map[string]interface{}{"id": utils.GetIDFromObject(ppRole), "slug": "patch-panel"},
	})
	pp01ID := utils.GetIDFromObject(pp01)
	pp01Rear := f.seed("dcim", "rear-ports", client.Object{"name": "rp1", "device": pp01ID})
	pp01Front := f.seed("dcim", "front-ports", client.Object{"name": "fp1", "device": pp01ID})
	_ = srvRole

	devices := []*models.DeviceConfig{
		{
			// Patch panel to patch panel: the peer port must be looked
			// up as a REAR port (backbone cable).
			Name: "pp-02", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "patch-panel",
			RearPorts: []models.RearPortConfig{{
				Name: "rp1", Type: "8p8c", Positions: 1,
				Link: &models.LinkConfig{PeerDevice: "pp-01", PeerPort: "rp1"},
			}},
			FrontPorts: []models.FrontPortConfig{{Name: "fp1", Type: "8p8c", RearPort: "rp1"}},
		},
		{
			// Server to patch panel: the peer port must be looked up as
			// a FRONT port (access cable).
			Name: "srv-01", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "server",
			Interfaces: []models.InterfaceConfig{{
				Name: "eth0", Type: "1000base-t",
				Link: &models.LinkConfig{PeerDevice: "pp-01", PeerPort: "fp1"},
			}},
		},
	}
	if err := NewDeviceReconciler(c).ReconcileDevices(devices); err != nil {
		t.Fatalf("ReconcileDevices() error = %v", err)
	}

	// pp-02's own front port references its just-created rear port.
	var pp02Front client.Object
	for _, fp := range f.objects("dcim", "front-ports") {
		if utils.GetIDFromObject(fp) != utils.GetIDFromObject(pp01Front) {
			pp02Front = fp
		}
	}
	if pp02Front == nil {
		t.Fatal("front port on pp-02 not created")
	}
	if utils.GetIDFromObject(pp02Front["rear_port"]) == 0 {
		t.Errorf("pp-02 front port rear_port = %v, expected resolved rear port ID", pp02Front["rear_port"])
	}

	cables := f.objects("dcim", "cables")
	if len(cables) != 2 {
		t.Fatalf("expected 2 cables (backbone + access), got %d", len(cables))
	}
	peerTypeByID := map[int]string{}
	for _, cable := range cables {
		bTerms := cable["b_terminations"].([]interface{})
		bTerm := bTerms[0].(map[string]interface{})
		peerTypeByID[utils.GetIDFromObject(bTerm["object_id"])] = bTerm["object_type"].(string)
	}
	if got := peerTypeByID[utils.GetIDFromObject(pp01Rear)]; got != "dcim.rearport" {
		t.Errorf("backbone cable peer type = %q, expected dcim.rearport (both ends are patch panels)", got)
	}
	if got := peerTypeByID[utils.GetIDFromObject(pp01Front)]; got != "dcim.frontport" {
		t.Errorf("access cable peer type = %q, expected dcim.frontport (peer is a patch panel)", got)
	}
}

func TestReconcileDevicesSkipsCableWhenPeerMissing(t *testing.T) {
	f, c := newFakeNetBox(t)
	seedDeviceFoundation(t, f, c)

	devices := []*models.DeviceConfig{{
		Name: "sw-01", SiteSlug: "berlin-dc", DeviceTypeSlug: "c9300", RoleSlug: "switch",
		Interfaces: []models.InterfaceConfig{{
			Name: "eth0", Type: "1000base-t",
			Link: &models.LinkConfig{PeerDevice: "ghost-device", PeerPort: "eth0"},
		}},
	}}
	if err := NewDeviceReconciler(c).ReconcileDevices(devices); err != nil {
		t.Fatalf("ReconcileDevices() error = %v, expected missing peer to be skipped without error", err)
	}
	if got := len(f.objects("dcim", "cables")); got != 0 {
		t.Errorf("expected no cables for a missing peer device, got %d", got)
	}
}
