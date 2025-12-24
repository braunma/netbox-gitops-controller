package reconciler

import (
	"fmt"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// DeviceReconciler handles device reconciliation
type DeviceReconciler struct {
	client *client.NetBoxClient
	logger *utils.Logger
}

// NewDeviceReconciler creates a new device reconciler
func NewDeviceReconciler(c *client.NetBoxClient) *DeviceReconciler {
	return &DeviceReconciler{
		client: c,
		logger: c.Logger(),
	}
}

// ReconcileDevices reconciles device configurations
func (dr *DeviceReconciler) ReconcileDevices(devices []*models.DeviceConfig) error {
	dr.logger.Info("Reconciling %d devices...", len(devices))

	for i, device := range devices {
		dr.logger.Debug("──── Device %d/%d: %s ────", i+1, len(devices), device.Name)
		if err := dr.reconcileDevice(device); err != nil {
			return fmt.Errorf("failed to reconcile device %s: %w", device.Name, err)
		}
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
	if err := dr.reconcileInterfaces(deviceID, device); err != nil {
		return fmt.Errorf("failed to reconcile interfaces: %w", err)
	}

	if err := dr.reconcileModules(deviceID, device); err != nil {
		return fmt.Errorf("failed to reconcile modules: %w", err)
	}

	return nil
}

// reconcileInterfaces reconciles device interfaces
func (dr *DeviceReconciler) reconcileInterfaces(deviceID int, device *models.DeviceConfig) error {
	for _, iface := range device.Interfaces {
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

		// Reconcile IP address if configured
		if iface.IP != nil {
			ifaceID := utils.GetIDFromObject(ifaceObj)
			if ifaceID > 0 {
				if err := dr.reconcileIPAddress(deviceID, ifaceID, &iface); err != nil {
					return fmt.Errorf("failed to reconcile IP for %s: %w", iface.Name, err)
				}
			}
		}
	}

	return nil
}

// reconcileIPAddress reconciles an IP address for an interface
func (dr *DeviceReconciler) reconcileIPAddress(deviceID, ifaceID int, iface *models.InterfaceConfig) error {
	ipConfig := iface.IP

	payload := map[string]interface{}{
		"address":              ipConfig.Address,
		"status":               ipConfig.Status,
		"assigned_object_type": "dcim.interface",
		"assigned_object_id":   ifaceID,
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
