package reconciler

import (
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// Shared builders for reconciler tests. They exist so a test can state only
// the fields it is actually about: the site/role/device type below are the
// ones seedDeviceFoundation puts into the fake NetBox, so a device built with
// testDevice always resolves, and anything a test does care about is layered
// on with an option.

const (
	testSiteSlug       = "berlin-dc"
	testRoleSlug       = "switch"
	testDeviceTypeSlug = "c9300"
)

// deviceOption customises a device built by testDevice.
type deviceOption func(*models.DeviceConfig)

// testDevice builds a device on the seeded site, role and device type.
func testDevice(name string, opts ...deviceOption) *models.DeviceConfig {
	d := &models.DeviceConfig{
		Name:           name,
		SiteSlug:       testSiteSlug,
		RoleSlug:       testRoleSlug,
		DeviceTypeSlug: testDeviceTypeSlug,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// inRack places the device at a position in a rack.
func inRack(rack string, position int) deviceOption {
	return func(d *models.DeviceConfig) {
		d.RackSlug = rack
		d.Position = position
		d.Face = "front"
	}
}

// inBay places the device into a bay on a parent device.
func inBay(parent, bay string) deviceOption {
	return func(d *models.DeviceConfig) {
		d.ParentDevice = parent
		d.DeviceBay = bay
	}
}

// withInterfaces attaches interfaces to the device.
func withInterfaces(ifaces ...models.InterfaceConfig) deviceOption {
	return func(d *models.DeviceConfig) {
		d.Interfaces = append(d.Interfaces, ifaces...)
	}
}

// withStatus sets an explicit device status.
func withStatus(status string) deviceOption {
	return func(d *models.DeviceConfig) {
		d.Status = status
	}
}

// withModules attaches modules to the device.
func withModules(modules ...models.ModuleConfig) deviceOption {
	return func(d *models.DeviceConfig) {
		d.Modules = append(d.Modules, modules...)
	}
}

// interfaceOption customises an interface built by testInterface.
type interfaceOption func(*models.InterfaceConfig)

// testInterface builds a device interface of the given type.
func testInterface(name, ifType string, opts ...interfaceOption) models.InterfaceConfig {
	iface := models.InterfaceConfig{Name: name, Type: ifType}
	for _, opt := range opts {
		opt(&iface)
	}
	return iface
}

// withIP assigns an IP address to the interface.
func withIP(address string) interfaceOption {
	return func(i *models.InterfaceConfig) {
		i.IP = &models.IPConfig{Address: address}
	}
}

// asPrimaryIP marks the interface's IP as the device's primary address.
func asPrimaryIP() interfaceOption {
	return func(i *models.InterfaceConfig) {
		i.AddressRole = "primary"
	}
}

// inAccessVLAN puts the interface in access mode on an untagged VLAN.
func inAccessVLAN(vlan string) interfaceOption {
	return func(i *models.InterfaceConfig) {
		i.Mode = "access"
		i.UntaggedVLAN = vlan
	}
}

// linkedTo cables the interface to a peer device's port.
func linkedTo(peerDevice, peerPort, cableType string) interfaceOption {
	return func(i *models.InterfaceConfig) {
		i.Link = &models.LinkConfig{
			PeerDevice: peerDevice,
			PeerPort:   peerPort,
			CableType:  cableType,
		}
	}
}

// withMTU sets the interface MTU.
func withMTU(mtu int) interfaceOption {
	return func(i *models.InterfaceConfig) {
		i.MTU = mtu
	}
}

// deviceNames returns the device names in slice order, for readable
// assertions about ordering.
func deviceNames(devices []*models.DeviceConfig) []string {
	out := make([]string, len(devices))
	for i, d := range devices {
		out[i] = d.Name
	}
	return out
}
