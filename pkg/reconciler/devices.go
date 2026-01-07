package reconciler

import (
	"fmt"

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

// reconcileDevice reconciles a single device
func (dr *DeviceReconciler) reconcileDevice(device *models.DeviceConfig) error {
	// Get required IDs
	siteID, ok := dr.client.Cache().GetID("sites", device.SiteSlug)
	if !ok {
		return fmt.Errorf("site %s not found", device.SiteSlug)
	}

	roleID, ok := dr.client.Cache().GetID("roles", device.RoleSlug)
	if !ok {
		return fmt.Errorf("role %s not found", device.RoleSlug)
	}

	deviceTypeID, ok := dr.client.Cache().GetID("device_types", device.DeviceTypeSlug)
	if !ok {
		return fmt.Errorf("device type %s not found", device.DeviceTypeSlug)
	}

	// Build device payload
	payload := map[string]interface{}{
		"name":        device.Name,
		"site":        siteID,
		"role":        roleID,
		"device_type": deviceTypeID,
		"status":      device.Status,
	}

	if device.RackSlug != "" {
		rackID, ok := dr.client.Cache().GetID("racks", device.RackSlug)
		if ok {
			payload["rack"] = rackID
		}
	}

	if device.Position > 0 {
		payload["position"] = device.Position
	}

	if device.Face != "" {
		payload["face"] = device.Face
	}

	if device.Serial != "" {
		payload["serial"] = device.Serial
	}

	if device.AssetTag != "" {
		payload["asset_tag"] = device.AssetTag
	}

	// Create or update device
	lookup := map[string]interface{}{
		"name":    device.Name,
		"site_id": siteID,
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

	// Reconcile components
	dr.logger.Debug("  Reconciling interfaces for %s...", device.Name)
	if err := dr.reconcileInterfaces(deviceID, device); err != nil {
		return fmt.Errorf("failed to reconcile interfaces: %w", err)
	}

	dr.logger.Debug("  Reconciling front ports for %s...", device.Name)
	if err := dr.reconcileFrontPorts(deviceID, device); err != nil {
		return fmt.Errorf("failed to reconcile front ports: %w", err)
	}

	dr.logger.Debug("  Reconciling rear ports for %s...", device.Name)
	if err := dr.reconcileRearPorts(deviceID, device); err != nil {
		return fmt.Errorf("failed to reconcile rear ports: %w", err)
	}

	dr.logger.Debug("  Reconciling modules for %s...", device.Name)
	if err := dr.reconcileModules(deviceID, device); err != nil {
		return fmt.Errorf("failed to reconcile modules: %w", err)
	}

	return nil
}

// reconcileInterfaces reconciles device interfaces
func (dr *DeviceReconciler) reconcileInterfaces(deviceID int, device *models.DeviceConfig) error {
	for i, iface := range device.Interfaces {
		dr.logger.Debug("    Interface %d/%d: %s", i+1, len(device.Interfaces), iface.Name)

		payload := map[string]interface{}{
			"device":  deviceID,
			"name":    iface.Name,
			"type":    iface.Type,
			"enabled": iface.Enabled,
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

		if iface.UntaggedVLAN != "" {
			vlanID, ok := dr.client.Cache().GetID("vlans", iface.UntaggedVLAN)
			if ok {
				payload["untagged_vlan"] = vlanID
				dr.logger.Debug("      Untagged VLAN: %s (ID: %d)", iface.UntaggedVLAN, vlanID)
			}
		}

		if len(iface.TaggedVLANs) > 0 {
			var vlanIDs []int
			for _, vlanName := range iface.TaggedVLANs {
				if vlanID, ok := dr.client.Cache().GetID("vlans", vlanName); ok {
					vlanIDs = append(vlanIDs, vlanID)
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
				link:         iface.Link,
			})
		}
	}

	return nil
}

// reconcileIPAddress reconciles an IP address for an interface
func (dr *DeviceReconciler) reconcileIPAddress(deviceID, ifaceID int, iface *models.InterfaceConfig) error {
	ipConfig := iface.IP

	payload := map[string]interface{}{
		"address":              ipConfig.Address,
		"assigned_object_type": "dcim.interface",
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
		vrfID, ok := dr.client.Cache().GetID("vrfs", ipConfig.VRF)
		if ok {
			payload["vrf"] = vrfID
		}
	}

	lookup := map[string]interface{}{
		"address": ipConfig.Address,
	}

	if ipConfig.VRF != "" {
		if vrfID, ok := dr.client.Cache().GetID("vrfs", ipConfig.VRF); ok {
			lookup["vrf_id"] = vrfID
		}
	}

	ipObj, err := dr.client.Apply("ipam", "ip-addresses", lookup, payload)
	if err != nil {
		return fmt.Errorf("failed to apply IP address: %w", err)
	}

	// Set as primary IP if requested
	if iface.AddressRole == "primary" {
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
		moduleTypeID, ok := dr.client.Cache().GetID("module_types", module.ModuleTypeSlug)
		if !ok {
			dr.logger.Warning("Module type %s not found, skipping", module.ModuleTypeSlug)
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
			continue
		}

		bayID := utils.GetIDFromObject(bays[0])

		payload := map[string]interface{}{
			"device":      deviceID,
			"module_bay":  bayID,
			"module_type": moduleTypeID,
			"status":      module.Status,
		}

		if module.Serial != "" {
			payload["serial"] = module.Serial
		}
		if module.AssetTag != "" {
			payload["asset_tag"] = module.AssetTag
		}
		if module.Description != "" {
			payload["description"] = module.Description
		}

		lookup := map[string]interface{}{
			"device_id":    deviceID,
			"module_bay_id": bayID,
		}

		_, err = dr.client.Apply("dcim", "modules", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to apply module %s: %w", module.Name, err)
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
			continue
		}

		rearPortID := utils.GetIDFromObject(rearPorts[0])

		payload := map[string]interface{}{
			"device":   deviceID,
			"name":     port.Name,
			"type":     port.Type,
			"rear_port": rearPortID,
		}

		if port.RearPortPosition > 0 {
			payload["rear_port_position"] = port.RearPortPosition
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
			"type":   port.Type,
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
				link:         port.Link,
			})
		}
	}

	return nil
}

// reconcilePendingCables processes all pending cable connections
func (dr *DeviceReconciler) reconcilePendingCables() error {
	// Build a lookup map for all ports by device+name
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

		// Also try to find the peer port
		peerKey := fmt.Sprintf("%s::%s", pc.link.PeerDevice, pc.link.PeerPort)
		if _, exists := portLookup[peerKey]; !exists {
			// Query NetBox for peer port
			if peerInfo := dr.findPort(pc.link.PeerDevice, pc.link.PeerPort); peerInfo != nil {
				portLookup[peerKey] = *peerInfo
			}
		}
	}

	dr.logger.Debug("Port lookup table built with %d entries", len(portLookup))

	// Now reconcile each cable
	for _, pc := range dr.pendingCables {
		sourceKey := fmt.Sprintf("%s::%s", pc.sourceDevice, pc.sourcePort)
		peerKey := fmt.Sprintf("%s::%s", pc.link.PeerDevice, pc.link.PeerPort)

		source, sourceOK := portLookup[sourceKey]
		peer, peerOK := portLookup[peerKey]

		if !sourceOK {
			dr.logger.Warning("Source port not found: %s", sourceKey)
			continue
		}

		if !peerOK {
			dr.logger.Warning("Peer port not found: %s (from %s)", peerKey, sourceKey)
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
			DeviceName: peer.device,
			PortName:   peer.port,
			ObjectType: peer.objectType,
			ObjectID:   peer.objectID,
		}

		// Reconcile the cable
		if err := dr.cableReconciler.ReconcileCable(aEnd, bEnd, pc.link); err != nil {
			return fmt.Errorf("failed to reconcile cable %s[%s] <-> %s[%s]: %w",
				source.device, source.port, peer.device, peer.port, err)
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

// findPort searches for a port by device and port name
func (dr *DeviceReconciler) findPort(deviceName, portName string) *portInfo {
	// Get device ID
	deviceID, ok := dr.client.Cache().GetID("devices", deviceName)
	if !ok {
		dr.logger.Debug("    Device %s not found in cache", deviceName)
		return nil
	}

	// Try to find as interface
	interfaces, err := dr.client.Filter("dcim", "interfaces", map[string]interface{}{
		"device_id": deviceID,
		"name":      portName,
	})
	if err == nil && len(interfaces) > 0 {
		return &portInfo{
			objectType: "dcim.interface",
			objectID:   utils.GetIDFromObject(interfaces[0]),
			device:     deviceName,
			port:       portName,
		}
	}

	// Try to find as front port
	frontPorts, err := dr.client.Filter("dcim", "front-ports", map[string]interface{}{
		"device_id": deviceID,
		"name":      portName,
	})
	if err == nil && len(frontPorts) > 0 {
		return &portInfo{
			objectType: "dcim.frontport",
			objectID:   utils.GetIDFromObject(frontPorts[0]),
			device:     deviceName,
			port:       portName,
		}
	}

	// Try to find as rear port
	rearPorts, err := dr.client.Filter("dcim", "rear-ports", map[string]interface{}{
		"device_id": deviceID,
		"name":      portName,
	})
	if err == nil && len(rearPorts) > 0 {
		return &portInfo{
			objectType: "dcim.rearport",
			objectID:   utils.GetIDFromObject(rearPorts[0]),
			device:     deviceName,
			port:       portName,
		}
	}

	return nil
}
