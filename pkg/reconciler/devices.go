// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"fmt"
	"strings"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// DeviceReconciler handles device reconciliation
type DeviceReconciler struct {
	client          *client.NetBoxClient
	logger          *utils.Logger
	cableReconciler *CableReconciler
	// Track all device interfaces/ports for cable reconciliation at the end
	pendingCables []pendingCable
}

// pendingCable tracks a cable that needs to be created after all devices are processed
type pendingCable struct {
	sourceDevice string
	sourcePort   string
	sourceType   string
	sourceID     int
	sourceRole   string // Device role slug (e.g., "patch-panel", "server", "switch")
	link         *models.LinkConfig
}

// NewDeviceReconciler creates a new device reconciler
func NewDeviceReconciler(c *client.NetBoxClient) *DeviceReconciler {
	return &DeviceReconciler{
		client:          c,
		logger:          c.Logger(),
		cableReconciler: NewCableReconciler(c),
		pendingCables:   make([]pendingCable, 0),
	}
}

// ReconcileDevices reconciles device configurations
func (dr *DeviceReconciler) ReconcileDevices(devices []*models.DeviceConfig) error {
	dr.logger.Info("Reconciling %d devices...", len(devices))

	// A child device is placed into a bay on its parent, which reconcileDevice
	// resolves with a live NetBox lookup — so a parent declared in this same run
	// must have been reconciled already. Order the list accordingly instead of
	// relying on the order the loader happened to read the files in.
	devices, err := sortDevicesByDependency(devices)
	if err != nil {
		return err
	}

	// Phase 1: Reconcile all devices and their ports
	dr.logger.Debug("═══ Phase 1: Devices and Ports ═══")
	for i, device := range devices {
		dr.logger.Debug("──── Device %d/%d: %s ────", i+1, len(devices), device.Name)
		if err := dr.reconcileDevice(device); err != nil {
			return fmt.Errorf("failed to reconcile device %s: %w", device.Name, err)
		}
	}

	// Phase 2: Reconcile all cables (after all devices/ports exist)
	dr.logger.Debug("═══ Phase 2: Cables ═══")
	dr.logger.Info("Reconciling %d pending cable connections...", len(dr.pendingCables))
	if err := dr.reconcilePendingCables(); err != nil {
		return fmt.Errorf("failed to reconcile cables: %w", err)
	}

	return nil
}

// sortDevicesByDependency returns the devices ordered so that a parent always
// precedes the children it hosts, at any nesting depth. Devices that are not
// related by parent_device keep their original relative order, so a run's
// output stays stable and diffable.
//
// A parent_device that is not declared in this run is left alone: it must
// already exist in NetBox, and reconcileDevice looks it up there.
func sortDevicesByDependency(devices []*models.DeviceConfig) ([]*models.DeviceConfig, error) {
	// First occurrence wins; a duplicate name is a data error that surfaces
	// with a clearer message when NetBox rejects the second device.
	indexByName := make(map[string]int, len(devices))
	for i, d := range devices {
		if _, seen := indexByName[d.Name]; !seen {
			indexByName[d.Name] = i
		}
	}

	const (
		unvisited = iota
		visiting
		done
	)
	state := make([]int, len(devices))
	sorted := make([]*models.DeviceConfig, 0, len(devices))

	// Depth-first emit: a device is appended only after its ancestors are.
	var visit func(i int, chain []string) error
	visit = func(i int, chain []string) error {
		switch state[i] {
		case done:
			return nil
		case visiting:
			// Includes a device naming itself as its own parent.
			return fmt.Errorf("parent_device cycle detected: %s",
				strings.Join(append(chain, devices[i].Name), " -> "))
		}

		state[i] = visiting
		if parent := devices[i].ParentDevice; parent != "" {
			if pi, ok := indexByName[parent]; ok {
				if err := visit(pi, append(chain, devices[i].Name)); err != nil {
					return err
				}
			}
		}
		state[i] = done

		sorted = append(sorted, devices[i])
		return nil
	}

	for i := range devices {
		if err := visit(i, nil); err != nil {
			return nil, err
		}
	}
	return sorted, nil
}

// reconcileDevice reconciles a single device. A missing required reference
// (site, role, device type) warns and skips the device, marking the endpoint's
// reconcile incomplete — the shared behavior of resolveRef.
func (dr *DeviceReconciler) reconcileDevice(device *models.DeviceConfig) error {
	siteID, ok := resolveRef(dr.client, reference{
		app: "dcim", endpoint: "sites", cacheKind: "sites",
		value: device.SiteSlug,
		kind:  "Site", forKind: "device", forName: device.Name,
		consumerApp: "dcim", consumerEndpoint: "devices",
	})
	if !ok {
		return nil
	}

	roleID, ok := resolveRef(dr.client, reference{
		app: "dcim", endpoint: "device-roles", cacheKind: "roles",
		value: device.RoleSlug,
		kind:  "Role", forKind: "device", forName: device.Name,
		consumerApp: "dcim", consumerEndpoint: "devices",
	})
	if !ok {
		return nil
	}

	deviceTypeID, ok := resolveRef(dr.client, reference{
		app: "dcim", endpoint: "device-types", cacheKind: "device_types",
		value: device.DeviceTypeSlug,
		kind:  "Device type", forKind: "device", forName: device.Name,
		consumerApp: "dcim", consumerEndpoint: "devices",
	})
	if !ok {
		return nil
	}

	// A. Rack & Parent Logic
	var yamlRackID, parentRackID, deviceBayID int
	var finalRackID int

	// Get rack ID from YAML config if specified
	// CRITICAL: Use site-scoped lookup - racks are site-specific
	// (avoids cache collisions between same-named racks in different sites)
	if device.RackSlug != "" {
		if rackID, ok := dr.client.Cache().GetSiteID("racks", siteID, device.RackSlug); ok {
			yamlRackID = rackID
		} else {
			// Racks are only registered by the foundation phase, so a run that
			// skips it (--only devices) can miss one. Say so: placing a device
			// with no rack when the YAML asks for one is silent data loss.
			dr.logger.Warning("Rack %q not found at site %q for device %s; the device will have no rack, position or face",
				device.RackSlug, device.SiteSlug, device.Name)
		}
	}

	// Handle parent device and device bay installation
	if device.ParentDevice != "" {
		// device_bay is required to place a child into a parent.
		if device.DeviceBay == "" {
			return fmt.Errorf("device_bay must be specified when parent_device is set")
		}

		// Find parent device (live lookup: a parent declared in the same run has
		// really been created by an earlier iteration on a live apply).
		parentDevices, err := dr.client.Filter("dcim", "devices", map[string]interface{}{
			"name": device.ParentDevice,
		})
		if err != nil {
			return fmt.Errorf("failed to look up parent device %s: %w", device.ParentDevice, err)
		}
		if len(parentDevices) == 0 {
			// In --dry-run a parent added in this same run does not exist in
			// NetBox yet, so it cannot be resolved. Skip placing the child rather
			// than aborting validation; it is placed for real on the apply.
			if dr.client.IsDryRun() {
				dr.logger.Warning("Parent device %s not found yet (declared this run?); skipping %s placement in dry-run", device.ParentDevice, device.Name)
				dr.client.MarkReconcileIncomplete("dcim", "devices")
				return nil
			}
			return fmt.Errorf("parent device %s not found", device.ParentDevice)
		}
		parentDevice := parentDevices[0]
		parentDeviceID := utils.GetIDFromObject(parentDevice)

		// The parent is looked up by name across every site, so under
		// --assert-site a name collision could otherwise install this child into
		// a parent that lives outside the allowed sites — a write into the very
		// objects the guard exists to protect. Check it here, where the parent
		// object is already in hand.
		if err := dr.client.CheckObjectSite("device", device.ParentDevice, parentDevice); err != nil {
			return err
		}

		// Get parent's rack if it has one
		if rack, ok := parentDevice["rack"].(map[string]interface{}); ok {
			if rackID, ok := rack["id"].(float64); ok {
				parentRackID = int(rackID)
			}
		}

		bays, err := dr.client.Filter("dcim", "device-bays", map[string]interface{}{
			"device_id": parentDeviceID,
			"name":      device.DeviceBay,
		})
		if err != nil {
			return fmt.Errorf("failed to look up device bay %s on parent %s: %w", device.DeviceBay, device.ParentDevice, err)
		}
		if len(bays) == 0 {
			// The bay is self-healed from a device-bay template during the apply
			// (see reconcileDeviceBays), so in dry-run a not-yet-created parent's
			// bay is expected to be missing. Skip placement instead of failing.
			if dr.client.IsDryRun() {
				dr.logger.Warning("Device bay %s not found on parent %s yet; skipping %s placement in dry-run", device.DeviceBay, device.ParentDevice, device.Name)
				dr.client.MarkReconcileIncomplete("dcim", "devices")
				return nil
			}
			return fmt.Errorf("device bay %s not found on parent %s", device.DeviceBay, device.ParentDevice)
		}
		deviceBayID = utils.GetIDFromObject(bays[0])
	}

	// Determine final rack ID: YAML rack takes precedence, then parent's rack.
	// A bay-mounted device is the exception: NetBox derives its location from
	// the parent and installDeviceIntoBay has to clear rack/position/face to
	// install it, so sending a rack here would be undone on install and
	// re-sent on the next run forever. Inventory files commonly set rack_slug
	// in a shared `defaults` block, so a blade inherits one it must not use.
	switch {
	case deviceBayID > 0:
		finalRackID = 0
	case yamlRackID > 0:
		finalRackID = yamlRackID
	case parentRackID > 0:
		finalRackID = parentRackID
	}

	// B. Build device payload
	// Default status to "active" if not provided
	status := defaultStatus(device.Status)

	payload := map[string]interface{}{
		"name":        device.Name,
		"site":        siteID,
		"role":        roleID,
		"device_type": deviceTypeID,
		"status":      status,
	}

	// Add rack if we have one
	if finalRackID > 0 {
		payload["rack"] = finalRackID
	}

	// Handle position and face based on device type
	if deviceBayID > 0 {
		// Child device going into a bay - remove position and face
		// (will be installed into bay after creation)
	} else if finalRackID > 0 {
		// Rack-mounted device - can have position and face
		if device.Position > 0 {
			payload["position"] = device.Position
		}
		if device.Face != "" {
			payload["face"] = device.Face
		}
	}
	// Else: No rack and no bay - position/face cannot be set

	if device.Serial != "" {
		payload["serial"] = device.Serial
	}

	if device.AssetTag != "" {
		payload["asset_tag"] = device.AssetTag
	}

	// Only the custom fields the YAML declares are sent. NetBox returns every
	// defined field on an object, and the client compares the declared subset
	// against it (see mapSubsetEqual), so a field this repository says nothing
	// about keeps whatever value it has — including one a human set in the UI.
	if len(device.CustomFields) > 0 {
		payload["custom_fields"] = device.CustomFields
	}

	// C. Create or update device
	lookup := map[string]interface{}{
		"name":    device.Name,
		"site_id": siteID,
	}

	lookup, err := dr.client.RenamedLookup("dcim", "devices", device.Name, lookup, "name", nonEmpty(device.RenameFrom))
	if err != nil {
		return err
	}

	deviceObj, err := dr.client.Apply("dcim", "devices", lookup, payload)
	if err != nil {
		return fmt.Errorf("failed to apply device: %w", err)
	}

	deviceID := utils.GetIDFromObject(deviceObj)
	if deviceID == 0 {
		dr.logger.Debug("Device created in dry-run mode")
		return nil
	}

	// D. Install device into bay if specified
	if deviceBayID > 0 {
		if err := dr.installDeviceIntoBay(deviceID, deviceBayID, device); err != nil {
			return fmt.Errorf("failed to install device into bay: %w", err)
		}
	}

	// Reconcile components
	dr.logger.Debug("  Reconciling interfaces for %s...", device.Name)
	if err := dr.reconcileInterfaces(deviceID, device); err != nil {
		return fmt.Errorf("failed to reconcile interfaces: %w", err)
	}

	// Rear ports must exist before front ports: front ports reference
	// them by ID (same ordering constraint as device type templates).
	dr.logger.Debug("  Reconciling rear ports for %s...", device.Name)
	if err := dr.reconcileRearPorts(deviceID, device); err != nil {
		return fmt.Errorf("failed to reconcile rear ports: %w", err)
	}

	dr.logger.Debug("  Reconciling front ports for %s...", device.Name)
	if err := dr.reconcileFrontPorts(deviceID, device); err != nil {
		return fmt.Errorf("failed to reconcile front ports: %w", err)
	}

	// Self-healing: Create missing device bays from device type templates
	dr.logger.Debug("  Reconciling device bays for %s...", device.Name)
	if err := dr.reconcileDeviceBays(deviceID, deviceTypeID); err != nil {
		return fmt.Errorf("failed to reconcile device bays: %w", err)
	}

	dr.logger.Debug("  Reconciling modules for %s...", device.Name)
	if err := dr.reconcileModules(deviceID, device); err != nil {
		return fmt.Errorf("failed to reconcile modules: %w", err)
	}

	return nil
}

// reconcileInterfaces reconciles device interfaces
func (dr *DeviceReconciler) reconcileInterfaces(deviceID int, device *models.DeviceConfig) error {
	// Get device's site ID for site-aware VLAN lookups
	// VLANs with the same name can exist at different sites, so we use site-scoped cache
	siteID, ok := dr.client.Cache().GetGlobalID("sites", device.SiteSlug)
	if !ok {
		return fmt.Errorf("site %s not found in cache", device.SiteSlug)
	}

	for i, iface := range device.Interfaces {
		dr.logger.Debug("    Interface %d/%d: %s", i+1, len(device.Interfaces), iface.Name)

		payload := map[string]interface{}{
			"device":  deviceID,
			"name":    iface.Name,
			"enabled": iface.IsEnabled(),
		}

		// Only include type if not empty (NetBox rejects empty string)
		if iface.Type != "" {
			payload["type"] = iface.Type
		}

		if iface.Label != "" {
			payload["label"] = iface.Label
		}
		if iface.Description != "" {
			payload["description"] = iface.Description
		}
		if iface.MTU > 0 {
			payload["mtu"] = iface.MTU
		}

		// VLAN configuration
		if iface.Mode != "" {
			payload["mode"] = iface.Mode
		}

		// CRITICAL: Use site-scoped cache lookup for VLANs
		// VLANs are cached with composite keys: "site-{siteID}:{vlanName}"
		// This prevents collisions when multiple sites have VLANs with same name
		if iface.UntaggedVLAN != "" {
			vlanID, ok := dr.client.Cache().GetSiteID("vlans", siteID, iface.UntaggedVLAN)
			if ok {
				payload["untagged_vlan"] = vlanID
				dr.logger.Debug("      Untagged VLAN: %s (ID: %d)", iface.UntaggedVLAN, vlanID)
			} else {
				dr.logger.Warning("      Untagged VLAN %s not found at site %s (ID: %d)", iface.UntaggedVLAN, device.SiteSlug, siteID)
			}
		}

		if len(iface.TaggedVLANs) > 0 {
			var vlanIDs []int
			for _, vlanName := range iface.TaggedVLANs {
				if vlanID, ok := dr.client.Cache().GetSiteID("vlans", siteID, vlanName); ok {
					vlanIDs = append(vlanIDs, vlanID)
				} else {
					dr.logger.Warning("      Tagged VLAN %s not found at site %s (ID: %d)", vlanName, device.SiteSlug, siteID)
				}
			}
			if len(vlanIDs) > 0 {
				payload["tagged_vlans"] = vlanIDs
				dr.logger.Debug("      Tagged VLANs: %d configured", len(vlanIDs))
			}
		}

		lookup := map[string]interface{}{
			"device_id": deviceID,
			"name":      iface.Name,
		}

		lookup, err := dr.client.RenamedLookup("dcim", "interfaces", iface.Name, lookup, "name", nonEmpty(iface.RenameFrom))
		if err != nil {
			return err
		}
		ifaceObj, err := dr.client.Apply("dcim", "interfaces", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to apply interface %s: %w", iface.Name, err)
		}

		ifaceID := utils.GetIDFromObject(ifaceObj)

		// Reconcile IP address if configured
		if iface.IP != nil && ifaceID > 0 {
			dr.logger.Debug("      IP Address: %s", iface.IP.Address)
			if err := dr.reconcileIPAddress(deviceID, ifaceID, &iface); err != nil {
				return fmt.Errorf("failed to reconcile IP for %s: %w", iface.Name, err)
			}
		}

		// Queue cable for later reconciliation (after all devices are processed)
		if iface.Link != nil && ifaceID > 0 {
			dr.logger.Debug("      Cable: %s → %s[%s]", iface.Name, iface.Link.PeerDevice, iface.Link.PeerPort)
			dr.pendingCables = append(dr.pendingCables, pendingCable{
				sourceDevice: device.Name,
				sourcePort:   iface.Name,
				sourceType:   "dcim.interface",
				sourceID:     ifaceID,
				sourceRole:   device.RoleSlug, // Track device role for port type determination
				link:         iface.Link,
			})
		}
	}

	// LAG membership runs after the loop: a member points at the LAG by ID, so
	// the LAG interface must already exist, and it may be declared after its
	// members in the YAML.
	if err := dr.reconcileLAGMembers(deviceID, device); err != nil {
		return err
	}

	return nil
}

// reconcileLAGMembers binds each interface listed under a LAG's `members` to
// that LAG. Members are interfaces on the same device; one that does not exist
// is reported rather than skipped silently, since a bond missing a leg is a
// real difference from what the YAML asked for.
func (dr *DeviceReconciler) reconcileLAGMembers(deviceID int, device *models.DeviceConfig) error {
	for _, iface := range device.Interfaces {
		if len(iface.Members) == 0 {
			continue
		}

		lagID, ok := dr.lookupDeviceInterface(deviceID, iface.Name)
		if !ok {
			if dr.client.IsDryRun() {
				dr.logger.Warning("      LAG %s is only planned in this run; skipping member binding in dry-run", iface.Name)
				dr.client.MarkReconcileIncomplete("dcim", "interfaces")
				continue
			}
			return fmt.Errorf("LAG interface %s not found on device %s", iface.Name, device.Name)
		}

		for _, member := range iface.Members {
			if _, ok := dr.lookupDeviceInterface(deviceID, member); !ok {
				dr.logger.Warning("      LAG %s lists member %s, which does not exist on %s; not bound",
					iface.Name, member, device.Name)
				dr.client.MarkReconcileIncomplete("dcim", "interfaces")
				continue
			}
			dr.logger.Debug("      LAG %s ← member %s", iface.Name, member)
			if _, err := dr.client.Apply("dcim", "interfaces",
				map[string]interface{}{"device_id": deviceID, "name": member},
				map[string]interface{}{"device": deviceID, "name": member, "lag": lagID},
			); err != nil {
				return fmt.Errorf("failed to bind %s into LAG %s: %w", member, iface.Name, err)
			}
		}
	}

	return nil
}

// lookupDeviceInterface resolves an interface by name on a device.
func (dr *DeviceReconciler) lookupDeviceInterface(deviceID int, name string) (int, bool) {
	found, err := dr.client.Filter("dcim", "interfaces", map[string]interface{}{
		"device_id": deviceID,
		"name":      name,
	})
	if err != nil || len(found) == 0 {
		return 0, false
	}
	id := utils.GetIDFromObject(found[0])
	return id, id > 0
}

// reconcileIPAddress reconciles an IP address for an interface
func (dr *DeviceReconciler) reconcileIPAddress(deviceID, ifaceID int, iface *models.InterfaceConfig) error {
	ipConfig := iface.IP

	payload := map[string]interface{}{
		"address":              ipConfig.Address,
		"assigned_object_type": constants.AssignedObjectInterface,
		"assigned_object_id":   ifaceID,
	}

	// Only include status if explicitly set (NetBox rejects empty string)
	if ipConfig.Status != "" {
		payload["status"] = ipConfig.Status
	}

	if ipConfig.DNSName != "" {
		payload["dns_name"] = ipConfig.DNSName
	}
	if ipConfig.Description != "" {
		payload["description"] = ipConfig.Description
	}

	if ipConfig.VRF != "" {
		vrfID, ok := dr.client.Cache().GetGlobalID("vrfs", ipConfig.VRF)
		if ok {
			payload["vrf"] = vrfID
		}
	}

	lookup := map[string]interface{}{
		"address": ipConfig.Address,
	}

	if ipConfig.VRF != "" {
		if vrfID, ok := dr.client.Cache().GetGlobalID("vrfs", ipConfig.VRF); ok {
			lookup["vrf_id"] = vrfID
		}
	}

	ipObj, err := dr.client.Apply("ipam", "ip-addresses", lookup, payload)
	if err != nil {
		return fmt.Errorf("failed to apply IP address: %w", err)
	}

	// Set as primary IP if requested
	if iface.IsPrimaryIP() {
		ipID := utils.GetIDFromObject(ipObj)
		if ipID > 0 {
			if err := dr.setPrimaryIP(deviceID, ipID); err != nil {
				return fmt.Errorf("failed to set primary IP: %w", err)
			}
		}
	}

	return nil
}

// setPrimaryIP sets the primary IP for a device
func (dr *DeviceReconciler) setPrimaryIP(deviceID, ipID int) error {
	// Get the IP address to determine family
	ipObj, err := dr.client.Get("ipam", "ip-addresses", ipID)
	if err != nil {
		return fmt.Errorf("failed to get IP address: %w", err)
	}

	family := 4
	if fam, ok := ipObj["family"].(map[string]interface{}); ok {
		if val, ok := fam["value"].(float64); ok {
			family = int(val)
		}
	} else if fam, ok := ipObj["family"].(float64); ok {
		family = int(fam)
	}

	field := "primary_ip4"
	if family == 6 {
		field = "primary_ip6"
	}

	// Skip the PATCH if the device already points at this IP. A direct Update
	// records an "update" unconditionally, so re-setting an unchanged primary
	// IP inflates the change count on every run.
	device, err := dr.client.Get("dcim", "devices", deviceID)
	if err != nil {
		return fmt.Errorf("failed to get device: %w", err)
	}
	if utils.GetIDFromObject(device[field]) == ipID {
		return nil
	}

	// Update device
	err = dr.client.Update("dcim", "devices", deviceID, map[string]interface{}{
		field: ipID,
	})

	if err != nil {
		return fmt.Errorf("failed to update device primary IP: %w", err)
	}

	dr.logger.Info("Set primary IP for device %d", deviceID)
	return nil
}

// reconcileModules reconciles device modules
func (dr *DeviceReconciler) reconcileModules(deviceID int, device *models.DeviceConfig) error {
	for _, module := range device.Modules {
		// Get module type ID
		moduleTypeID, ok := dr.client.Cache().GetGlobalID("module_types", module.ModuleTypeSlug)
		if !ok {
			dr.logger.Warning("Module type %s not found, skipping", module.ModuleTypeSlug)
			// A declared module was skipped; keep prune off this endpoint so a
			// declared-but-unreconciled module is not mistaken for an orphan.
			dr.client.MarkReconcileIncomplete("dcim", "modules")
			continue
		}

		// Find module bay
		bays, err := dr.client.Filter("dcim", "module-bays", map[string]interface{}{
			"device_id": deviceID,
			"name":      module.Name,
		})
		if err != nil {
			return fmt.Errorf("failed to find module bay: %w", err)
		}

		if len(bays) == 0 {
			dr.logger.Warning("Module bay %s not found on device, skipping", module.Name)
			dr.client.MarkReconcileIncomplete("dcim", "modules")
			continue
		}

		bayID := utils.GetIDFromObject(bays[0])

		// Default status to "active" if not provided
		status := module.Status
		if status == "" {
			status = "active"
		}

		payload := map[string]interface{}{
			"device":      deviceID,
			"module_bay":  bayID,
			"module_type": moduleTypeID,
			"status":      status,
		}

		// Only a declared serial is sent. This used to send an explicit empty
		// string whenever the YAML omitted one, which cleared the serial of
		// every module on every sync — including one somebody had typed into
		// the NetBox UI. NetBox's module serial is an optional blank field, so
		// omitting it on create leaves it empty just the same, and omitting it
		// on update leaves whatever is there alone. Saying nothing is how this
		// controller declines to have an opinion.
		if module.Serial != "" {
			payload["serial"] = module.Serial
		}

		if module.AssetTag != "" {
			payload["asset_tag"] = module.AssetTag
		}
		if module.Description != "" {
			payload["description"] = module.Description
		}

		// Add managed tag if available
		if dr.client.ManagedTagID() > 0 {
			payload["tags"] = []int{dr.client.ManagedTagID()}
		}

		lookup := map[string]interface{}{
			"device_id":     deviceID,
			"module_bay_id": bayID,
		}

		_, err = dr.client.Apply("dcim", "modules", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to apply module %s: %w", module.Name, err)
		}
	}

	return nil
}

// installDeviceIntoBay installs a device into a device bay using the bay-centric approach
func (dr *DeviceReconciler) installDeviceIntoBay(deviceID, deviceBayID int, device *models.DeviceConfig) error {
	dr.logger.Debug("  Installing device into bay...")

	// Check if the bay already holds this device. The device serializer has no
	// "device_bay" field (the relationship is exposed as the bay's
	// "installed_device"), so reading it off the device never matched and the
	// detach/install pair re-ran on every sync, inflating the change count.
	currentBay, err := dr.client.Get("dcim", "device-bays", deviceBayID)
	if err != nil {
		return fmt.Errorf("failed to get current device bay state: %w", err)
	}
	if utils.GetIDFromObject(currentBay["installed_device"]) == deviceID {
		dr.logger.Debug("  ✓ Already installed in correct device bay")
		return nil
	}

	if dr.client.IsDryRun() {
		dr.logger.Info("  [DRY-RUN] Would install into device bay %s", device.DeviceBay)
		return nil
	}

	// STEP 1: "Free" the device by removing rack/position/face
	// A device cannot go into a bay if it has these fields set
	dr.logger.Debug("    1. Detaching device from rack...")
	err = dr.client.Update("dcim", "devices", deviceID, map[string]interface{}{
		"rack":     nil,
		"position": nil,
		"face":     nil,
	})
	if err != nil {
		return fmt.Errorf("failed to detach device from rack: %w", err)
	}

	// STEP 2: Update the bay to install the device
	// This is the NetBox API way to install a device into a bay
	dr.logger.Debug("    2. Updating bay to install device...")
	err = dr.client.Update("dcim", "device-bays", deviceBayID, map[string]interface{}{
		"installed_device": deviceID,
	})
	if err != nil {
		return fmt.Errorf("failed to update device bay: %w", err)
	}

	dr.logger.Success("  ✓ Installed %s into device bay %s", device.Name, device.DeviceBay)
	return nil
}

// reconcileDeviceBays performs self-healing by creating missing device bays
// based on the device type templates
func (dr *DeviceReconciler) reconcileDeviceBays(deviceID, deviceTypeID int) error {
	// Get device bay templates for this device type
	templates, err := dr.client.Filter("dcim", "device-bay-templates", map[string]interface{}{
		"device_type_id": deviceTypeID,
	})
	if err != nil {
		return fmt.Errorf("failed to get device bay templates: %w", err)
	}

	// If no templates, this device type doesn't support bays - skip silently
	if len(templates) == 0 {
		return nil
	}

	dr.logger.Debug("    Checking %d device bay template(s)", len(templates))

	// Get existing device bays on the device
	existingBays, err := dr.client.Filter("dcim", "device-bays", map[string]interface{}{
		"device_id": deviceID,
	})
	if err != nil {
		return fmt.Errorf("failed to get existing device bays: %w", err)
	}

	// Build map of existing bay names
	existingBayNames := make(map[string]bool)
	for _, bay := range existingBays {
		if name, ok := bay["name"].(string); ok {
			existingBayNames[name] = true
		}
	}

	// Check each template and create missing bays
	for _, tmpl := range templates {
		name, ok := tmpl["name"].(string)
		if !ok {
			continue
		}

		if existingBayNames[name] {
			continue // Bay already exists
		}

		// Create missing bay
		dr.logger.Debug("    Missing bay '%s' - creating...", name)

		bayPayload := map[string]interface{}{
			"device": deviceID,
			"name":   name,
		}

		// Add label if present in template
		if label, ok := tmpl["label"].(string); ok && label != "" {
			bayPayload["label"] = label
		}

		if dr.client.IsDryRun() {
			dr.logger.Info("    [DRY-RUN] Would create device bay '%s'", name)
		} else {
			_, err := dr.client.Create("dcim", "device-bays", bayPayload)
			if err != nil {
				return fmt.Errorf("failed to create device bay %s: %w", name, err)
			}
			dr.logger.Success("    + Created device bay '%s'", name)
		}
	}

	return nil
}

// reconcileFrontPorts reconciles device front ports
func (dr *DeviceReconciler) reconcileFrontPorts(deviceID int, device *models.DeviceConfig) error {
	if len(device.FrontPorts) == 0 {
		return nil
	}

	for i, port := range device.FrontPorts {
		dr.logger.Debug("    Front Port %d/%d: %s", i+1, len(device.FrontPorts), port.Name)

		// Find rear port ID
		rearPorts, err := dr.client.Filter("dcim", "rear-ports", map[string]interface{}{
			"device_id": deviceID,
			"name":      port.RearPort,
		})
		if err != nil {
			return fmt.Errorf("failed to find rear port %s: %w", port.RearPort, err)
		}

		if len(rearPorts) == 0 {
			dr.logger.Warning("      Rear port %s not found, skipping front port", port.RearPort)
			// A declared front port was skipped; do not let prune treat the
			// existing one (if any) as an orphan.
			dr.client.MarkReconcileIncomplete("dcim", "front-ports")
			continue
		}

		rearPortID := utils.GetIDFromObject(rearPorts[0])

		payload := map[string]interface{}{
			"device": deviceID,
			"name":   port.Name,
		}
		setFrontPortTermination(dr.client, payload, rearPortID, defaultPosition(port.RearPortPosition))

		// Only include type if not empty (NetBox rejects empty string)
		if port.Type != "" {
			payload["type"] = port.Type
		}
		if port.Label != "" {
			payload["label"] = port.Label
		}
		if port.Description != "" {
			payload["description"] = port.Description
		}

		lookup := map[string]interface{}{
			"device_id": deviceID,
			"name":      port.Name,
		}

		portObj, err := dr.client.Apply("dcim", "front-ports", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to apply front port %s: %w", port.Name, err)
		}

		// Queue cable for later reconciliation
		portID := utils.GetIDFromObject(portObj)
		if port.Link != nil && portID > 0 {
			dr.logger.Debug("      Cable: %s → %s[%s]", port.Name, port.Link.PeerDevice, port.Link.PeerPort)
			dr.pendingCables = append(dr.pendingCables, pendingCable{
				sourceDevice: device.Name,
				sourcePort:   port.Name,
				sourceType:   "dcim.frontport",
				sourceID:     portID,
				sourceRole:   device.RoleSlug, // Track device role for port type determination
				link:         port.Link,
			})
		}
	}

	return nil
}

// reconcileRearPorts reconciles device rear ports
func (dr *DeviceReconciler) reconcileRearPorts(deviceID int, device *models.DeviceConfig) error {
	if len(device.RearPorts) == 0 {
		return nil
	}

	for i, port := range device.RearPorts {
		dr.logger.Debug("    Rear Port %d/%d: %s", i+1, len(device.RearPorts), port.Name)

		payload := map[string]interface{}{
			"device": deviceID,
			"name":   port.Name,
		}

		// Only include type if not empty (NetBox rejects empty string)
		if port.Type != "" {
			payload["type"] = port.Type
		}

		if port.Positions > 0 {
			payload["positions"] = port.Positions
		}
		if port.Label != "" {
			payload["label"] = port.Label
		}
		if port.Description != "" {
			payload["description"] = port.Description
		}

		lookup := map[string]interface{}{
			"device_id": deviceID,
			"name":      port.Name,
		}

		portObj, err := dr.client.Apply("dcim", "rear-ports", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to apply rear port %s: %w", port.Name, err)
		}

		// Queue cable for later reconciliation
		portID := utils.GetIDFromObject(portObj)
		if port.Link != nil && portID > 0 {
			dr.logger.Debug("      Cable: %s → %s[%s]", port.Name, port.Link.PeerDevice, port.Link.PeerPort)
			dr.pendingCables = append(dr.pendingCables, pendingCable{
				sourceDevice: device.Name,
				sourcePort:   port.Name,
				sourceType:   "dcim.rearport",
				sourceID:     portID,
				sourceRole:   device.RoleSlug, // Track device role for port type determination
				link:         port.Link,
			})
		}
	}

	return nil
}

// reconcilePendingCables processes all pending cable connections
func (dr *DeviceReconciler) reconcilePendingCables() error {
	// Build a lookup map ONLY for source ports (which are already known from device reconciliation)
	// DO NOT pre-cache peer ports - they must be looked up dynamically based on source device role
	// This is because the same port name (e.g., pp-rack-a-01[2]) can be BOTH:
	//   - A frontport (when accessed from server/switch)
	//   - A rearport (when used for patch panel backbone cables)
	// So we do fresh role-based lookups for each cable
	portLookup := make(map[string]portInfo)

	dr.logger.Debug("Building port lookup table from %d pending cables...", len(dr.pendingCables))
	for _, pc := range dr.pendingCables {
		key := fmt.Sprintf("%s::%s", pc.sourceDevice, pc.sourcePort)
		portLookup[key] = portInfo{
			objectType: pc.sourceType,
			objectID:   pc.sourceID,
			device:     pc.sourceDevice,
			port:       pc.sourcePort,
		}
		// NOTE: We do NOT pre-cache peer ports here!
		// Peer ports will be looked up dynamically during cable reconciliation
		// using role-based logic (findPort with sourceRole parameter)
	}

	dr.logger.Debug("Port lookup table built with %d entries", len(portLookup))

	// Now reconcile each cable
	for _, pc := range dr.pendingCables {
		sourceKey := fmt.Sprintf("%s::%s", pc.sourceDevice, pc.sourcePort)

		source, sourceOK := portLookup[sourceKey]

		if !sourceOK {
			dr.logger.Warning("Source port not found: %s", sourceKey)
			continue
		}

		// Look up peer port dynamically using role-based logic (NOT from cached lookup)
		// This ensures pp-rack-a-01[2] is found as frontport when source is server,
		// but as rearport when source is patch-panel (for backbone cables)
		peerInfo := dr.findPort(pc.link.PeerDevice, pc.link.PeerPort, pc.sourceRole)
		if peerInfo == nil {
			dr.logger.Warning("Peer port not found: %s::%s (from %s, role=%s)",
				pc.link.PeerDevice, pc.link.PeerPort, sourceKey, pc.sourceRole)
			continue
		}

		// Create cable endpoints
		aEnd := &CableEndpoint{
			DeviceName: source.device,
			PortName:   source.port,
			ObjectType: source.objectType,
			ObjectID:   source.objectID,
		}

		bEnd := &CableEndpoint{
			DeviceName: peerInfo.device,
			PortName:   peerInfo.port,
			ObjectType: peerInfo.objectType,
			ObjectID:   peerInfo.objectID,
		}

		// Reconcile the cable
		if err := dr.cableReconciler.ReconcileCable(aEnd, bEnd, pc.link); err != nil {
			return fmt.Errorf("failed to reconcile cable %s[%s] <-> %s[%s]: %w",
				source.device, source.port, peerInfo.device, peerInfo.port, err)
		}
	}

	return nil
}

// portInfo stores port information for cable reconciliation
type portInfo struct {
	objectType string
	objectID   int
	device     string
	port       string
}

// findPort searches for a port by device and port name, using role-based logic to determine port type
func (dr *DeviceReconciler) findPort(deviceName, portName, sourceRole string) *portInfo {
	// Get device ID using LIVE lookup (not cache)
	// Devices are not loaded into cache, so we must query NetBox directly
	devices, err := dr.client.Filter("dcim", "devices", map[string]interface{}{
		"name": deviceName,
	})
	if err != nil || len(devices) == 0 {
		dr.logger.Debug("    Device %s not found", deviceName)
		return nil
	}

	device := devices[0]
	deviceID := utils.GetIDFromObject(device)
	if deviceID == 0 {
		dr.logger.Debug("    Device %s has invalid ID", deviceName)
		return nil
	}

	// Get peer device role
	peerRole := ""
	if roleMap, ok := device["role"].(map[string]interface{}); ok {
		if slug, ok := roleMap["slug"].(string); ok {
			peerRole = slug
		}
	}

	// Determine port type based on device roles
	isSourcePP := sourceRole == "patch-panel"
	isPeerPP := peerRole == "patch-panel"

	// Debug logging to trace role-based port type selection
	dr.logger.Debug("    findPort(%s, %s, sourceRole=%s): peerRole=%s, isSourcePP=%v, isPeerPP=%v",
		deviceName, portName, sourceRole, peerRole, isSourcePP, isPeerPP)

	// Port selection rules:
	// - Both patch panels: use rearport (backbone cable)
	// - Only peer is patch panel: use frontport (access cable)
	// - Otherwise: use interface (device-to-device)

	if isSourcePP && isPeerPP {
		dr.logger.Debug("    → Searching for REARPORT (both devices are patch-panels)")
		// Patchpanel ↔ Patchpanel = Rear ↔ Rear (Backbone)
		rearPorts, err := dr.client.Filter("dcim", "rear-ports", map[string]interface{}{
			"device_id": deviceID,
			"name":      portName,
		})
		if err == nil && len(rearPorts) > 0 {
			dr.logger.Debug("    ✓ Found rearport ID %d", utils.GetIDFromObject(rearPorts[0]))
			return &portInfo{
				objectType: "dcim.rearport",
				objectID:   utils.GetIDFromObject(rearPorts[0]),
				device:     deviceName,
				port:       portName,
			}
		}
		dr.logger.Debug("    ✗ Rearport not found (err=%v, count=%d)", err, len(rearPorts))
	} else if isPeerPP {
		dr.logger.Debug("    → Searching for FRONTPORT (only peer is patch-panel)")
		// Device → Patchpanel = FrontPort (Server/Switch Access)
		frontPorts, err := dr.client.Filter("dcim", "front-ports", map[string]interface{}{
			"device_id": deviceID,
			"name":      portName,
		})
		if err == nil && len(frontPorts) > 0 {
			dr.logger.Debug("    ✓ Found frontport ID %d", utils.GetIDFromObject(frontPorts[0]))
			return &portInfo{
				objectType: "dcim.frontport",
				objectID:   utils.GetIDFromObject(frontPorts[0]),
				device:     deviceName,
				port:       portName,
			}
		}
		dr.logger.Debug("    ✗ Frontport not found (err=%v, count=%d)", err, len(frontPorts))
	} else {
		dr.logger.Debug("    → Searching for INTERFACE (neither device is patch-panel)")
		// Device → Device (Interface)
		interfaces, err := dr.client.Filter("dcim", "interfaces", map[string]interface{}{
			"device_id": deviceID,
			"name":      portName,
		})
		if err == nil && len(interfaces) > 0 {
			dr.logger.Debug("    ✓ Found interface ID %d", utils.GetIDFromObject(interfaces[0]))
			return &portInfo{
				objectType: "dcim.interface",
				objectID:   utils.GetIDFromObject(interfaces[0]),
				device:     deviceName,
				port:       portName,
			}
		}
		dr.logger.Debug("    ✗ Interface not found (err=%v, count=%d)", err, len(interfaces))
	}

	dr.logger.Warning("    ✗ Port not found with any type (interface/frontport/rearport)")
	return nil
}
