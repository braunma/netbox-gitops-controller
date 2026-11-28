// SPDX-License-Identifier: Apache-2.0

package collectors

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// Assignment is the result of resolving every scan source against the
// declared inventory: which hosts each source reads, and which devices fall
// through.
type Assignment struct {
	// Targets holds, per source name, the hosts that source scans, sorted by
	// device name.
	Targets map[string][]Target
	// NoAddress holds, per source name, the devices its selector picks that
	// have the source's interface — on the device or on its device type — but
	// declare no address on it. They cannot be scanned. A device without such
	// a port at all (a patch panel, a switch) is not this source's business
	// and is not listed.
	NoAddress map[string][]string
	// Unassigned lists devices that declare an address on a scanned interface
	// and that no source selects. They are a gap in the configuration, not an
	// error: a new rack is declared before its source is.
	Unassigned []string
}

// AssignTargets resolves the scan sources against the declared devices.
//
// It is given every scan source of the configuration, not only the ones a run
// selects, because the check that matters is across sources: a device that two
// sources select would be sent two different sets of credentials, so that is
// an error before anything is scanned.
func AssignTargets(sources []Source, devices []*models.DeviceConfig, deviceTypes []*models.DeviceType) (*Assignment, error) {
	out := &Assignment{
		Targets:   make(map[string][]Target, len(sources)),
		NoAddress: make(map[string][]string, len(sources)),
	}

	prefixes := make(map[string]netip.Prefix, len(sources))
	interfaces := make(map[string]bool, len(sources))
	for _, src := range sources {
		interfaces[src.Interface] = true
		if src.Select.Prefix == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(src.Select.Prefix)
		if err != nil {
			return nil, fmt.Errorf("source %q: select.prefix %q is not a CIDR", src.Name, src.Select.Prefix)
		}
		prefixes[src.Name] = prefix
	}

	// The ports a device has are mostly declared once, on its device type.
	typePorts := make(map[string]map[string]bool, len(deviceTypes))
	for _, dt := range deviceTypes {
		ports := make(map[string]bool, len(dt.Interfaces))
		for _, iface := range dt.Interfaces {
			ports[iface.Name] = true
		}
		typePorts[dt.Slug] = ports
	}

	var errs []error
	for _, dev := range devices {
		var owners []string
		for _, src := range sources {
			if !selectsDevice(src.Select, dev) {
				continue
			}
			raw, declared := interfaceAddress(dev, src.Interface)
			if !declared {
				// A selector without a prefix picks the device whatever its
				// address, so a BMC port with no address is worth naming.
				// With a prefix there is nothing to compare, and the device
				// simply does not match.
				if src.Select.Prefix == "" && (typePorts[dev.DeviceTypeSlug][src.Interface] || hasInterface(dev, src.Interface)) {
					out.NoAddress[src.Name] = append(out.NoAddress[src.Name], dev.Name)
				}
				continue
			}
			addr, err := hostAddress(raw)
			if err != nil {
				errs = append(errs, fmt.Errorf("device %s: interface %s: address %q is not an IP address", dev.Name, src.Interface, raw))
				continue
			}
			if prefix, ok := prefixes[src.Name]; ok && !prefix.Contains(addr) {
				continue
			}
			owners = append(owners, src.Name)
			out.Targets[src.Name] = append(out.Targets[src.Name], Target{Device: dev.Name, Address: addr.String()})
		}

		switch {
		case len(owners) > 1:
			errs = append(errs, fmt.Errorf("device %s is selected by %d sources (%s); narrow their `select` so each device belongs to one source, because each source carries its own credentials",
				dev.Name, len(owners), strings.Join(owners, ", ")))
		case len(owners) == 0 && hasScannedAddress(dev, interfaces):
			out.Unassigned = append(out.Unassigned, dev.Name)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	for name := range out.Targets {
		targets := out.Targets[name]
		sort.Slice(targets, func(i, j int) bool { return targets[i].Device < targets[j].Device })
	}
	for name := range out.NoAddress {
		sort.Strings(out.NoAddress[name])
	}
	sort.Strings(out.Unassigned)
	return out, nil
}

// selectsDevice reports whether every key the selector sets matches the
// device. The prefix is checked by the caller, which knows the address.
func selectsDevice(sel Selector, dev *models.DeviceConfig) bool {
	if sel.Site != "" && sel.Site != dev.SiteSlug {
		return false
	}
	if sel.Rack != "" && sel.Rack != dev.RackSlug {
		return false
	}
	if sel.Role != "" && sel.Role != dev.RoleSlug {
		return false
	}
	for _, want := range sel.Tags {
		found := false
		for _, have := range dev.Tags {
			if have == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// interfaceAddress returns the address a device declares on the named
// interface.
func interfaceAddress(dev *models.DeviceConfig, name string) (string, bool) {
	for i := range dev.Interfaces {
		iface := &dev.Interfaces[i]
		if iface.Name == name && iface.IP != nil && iface.IP.Address != "" {
			return iface.IP.Address, true
		}
	}
	return "", false
}

// hasInterface reports whether the device itself lists the named interface.
func hasInterface(dev *models.DeviceConfig, name string) bool {
	for i := range dev.Interfaces {
		if dev.Interfaces[i].Name == name {
			return true
		}
	}
	return false
}

// hasScannedAddress reports whether the device declares an address on any
// interface a scan source reads.
func hasScannedAddress(dev *models.DeviceConfig, interfaces map[string]bool) bool {
	for name := range interfaces {
		if _, ok := interfaceAddress(dev, name); ok {
			return true
		}
	}
	return false
}

// hostAddress strips the prefix length the inventory writes on an address
// ("192.0.2.10/24") and returns the host part.
func hostAddress(raw string) (netip.Addr, error) {
	if prefix, err := netip.ParsePrefix(raw); err == nil {
		return prefix.Addr(), nil
	}
	return netip.ParseAddr(raw)
}
