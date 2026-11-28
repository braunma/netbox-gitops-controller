// SPDX-License-Identifier: Apache-2.0

package export

import (
	"fmt"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// cableEnd is one termination of a cable.
type cableEnd struct {
	kind     string // dcim.interface, dcim.frontport, dcim.rearport, or other
	portID   int
	deviceID int
	port     string
}

func (c cableEnd) label(e *exporter) string {
	return fmt.Sprintf("%s:%s", str(e.deviceByID[c.deviceID], "name"), c.port)
}

// peerKind is the kind of port the reconciler looks for on the far end of a
// cable declared on a device with role `from` toward a device with role `to`
// (see DeviceReconciler.findPort): rear port between two patch panels, front
// port into a patch panel, interface otherwise.
func peerKind(from, to string) string {
	switch {
	case from == models.PatchPanelRole && to == models.PatchPanelRole:
		return "dcim.rearport"
	case to == models.PatchPanelRole:
		return "dcim.frontport"
	}
	return "dcim.interface"
}

// exportCables declares each cable on one of its two ends, choosing an end
// from which the reconciler would find the other one, and then writes the
// devices.
func (e *exporter) exportCables(devs map[int]*exportedDev) {
	defer e.writeDevices(devs)

	skipped := map[string]int{}
	for _, cable := range e.data["dcim/cables"] {
		a, aok := cableTermination(cable["a_terminations"])
		b, bok := cableTermination(cable["b_terminations"])
		if !aok || !bok {
			skipped["with more than one termination on a side, or to a circuit, power feed or console port"]++
			continue
		}
		if e.sites != nil {
			if !e.inScope(nestedStr(e.deviceByID[a.deviceID], "site", "slug")) &&
				!e.inScope(nestedStr(e.deviceByID[b.deviceID], "site", "slug")) {
				continue
			}
		}
		link := &models.LinkConfig{
			CableType: str(cable, "type"), Color: str(cable, "color"),
			LengthUnit: choice(cable, "length_unit"),
		}
		if l, ok := num(cable, "length"); ok && l > 0 {
			link.Length = l
		} else {
			link.LengthUnit = ""
		}
		if t := tagSlugs(cable); len(t) > 0 {
			e.warn("cable %s - %s has tags %v, which a link cannot declare; they are kept", a.label(e), b.label(e), t)
		}

		// Declare a cable on an interface where possible: that is where a
		// reader looks for it, and how the hand-written files do it.
		order := [][2]cableEnd{{a, b}, {b, a}}
		if a.kind != "dcim.interface" && b.kind == "dcim.interface" {
			order[0], order[1] = order[1], order[0]
		}
		placed := false
		for _, pair := range order {
			src, dst := pair[0], pair[1]
			x := devs[src.deviceID]
			if x == nil {
				continue // the source device is declared by hand, or out of scope
			}
			if peerKind(e.roleOfDevice[src.deviceID], e.roleOfDevice[dst.deviceID]) != dst.kind {
				continue
			}
			// A front port as the source is found the same way as an
			// interface only from a patch panel; anything else is a shape
			// the reconciler does not wire.
			if !x.attach(src, link, str(e.deviceByID[dst.deviceID], "name"), dst.port) {
				continue
			}
			placed = true
			break
		}
		if !placed {
			switch {
			case devs[a.deviceID] == nil && devs[b.deviceID] == nil:
				// Both ends are already declared; the cable is theirs to declare.
			default:
				e.warn("cable %s - %s (%s to %s) was not exported: it can only be declared from a device this export does not write, or the controller would look for a different kind of port on the far end; declare it by hand",
					a.label(e), b.label(e), a.kind, b.kind)
			}
		}
	}
	for why, n := range skipped {
		e.warn("%s %s were not exported", plural(n, "cable"), why)
	}
}

// cableTermination reads a cable side with exactly one device port on it.
func cableTermination(v interface{}) (cableEnd, bool) {
	list, _ := v.([]interface{})
	if len(list) != 1 {
		return cableEnd{}, false
	}
	t := asMap(list[0])
	kind := str(t, "object_type")
	if kind == "" {
		kind = str(t, "termination_type") // NetBox before 4.0
	}
	switch kind {
	case "dcim.interface", "dcim.frontport", "dcim.rearport":
	default:
		return cableEnd{}, false
	}
	obj := nested(t, "object")
	if obj == nil {
		obj = nested(t, "termination")
	}
	end := cableEnd{
		kind:     kind,
		portID:   utils.GetIDFromObject(t["object_id"]),
		deviceID: nestedID(obj, "device"),
		port:     str(obj, "name"),
	}
	if end.portID == 0 {
		end.portID = utils.GetIDFromObject(t["termination_id"])
	}
	if end.portID == 0 {
		end.portID = idOf(obj)
	}
	return end, end.deviceID != 0 && end.port != ""
}

// attach puts a link on the port of this device, listing the port first if
// the device's YAML did not need it so far.
func (x *exportedDev) attach(src cableEnd, link *models.LinkConfig, peerDevice, peerPort string) bool {
	l := *link
	l.PeerDevice, l.PeerPort = peerDevice, peerPort
	switch src.kind {
	case "dcim.interface":
		i, ok := x.ifaceIdx[src.portID]
		if !ok {
			obj := x.ifaces[src.portID]
			if obj == nil {
				return false
			}
			x.cfg.Interfaces = append(x.cfg.Interfaces, models.InterfaceConfig{
				Name: str(obj, "name"), Type: choice(obj, "type"), Tags: tagSlugs(obj),
			})
			i = len(x.cfg.Interfaces) - 1
			x.ifaceIdx[src.portID] = i
		}
		if x.cfg.Interfaces[i].Link != nil {
			return false
		}
		x.cfg.Interfaces[i].Link = &l
	case "dcim.rearport":
		i, ok := x.rearIdx[src.portID]
		if !ok {
			obj := x.rears[src.portID]
			if obj == nil {
				return false
			}
			rc := rearPortConfig(obj)
			rc.Tags = tagSlugs(obj)
			x.cfg.RearPorts = append(x.cfg.RearPorts, rc)
			i = len(x.cfg.RearPorts) - 1
			x.rearIdx[src.portID] = i
		}
		if x.cfg.RearPorts[i].Link != nil {
			return false
		}
		x.cfg.RearPorts[i].Link = &l
	case "dcim.frontport":
		i, ok := x.frontIdx[src.portID]
		if !ok {
			obj := x.fronts[src.portID]
			if obj == nil {
				return false
			}
			fc := frontPortConfig(obj, x.rearNm)
			fc.Tags = tagSlugs(obj)
			x.cfg.FrontPorts = append(x.cfg.FrontPorts, fc)
			i = len(x.cfg.FrontPorts) - 1
			x.frontIdx[src.portID] = i
		}
		if x.cfg.FrontPorts[i].Link != nil {
			return false
		}
		x.cfg.FrontPorts[i].Link = &l
	default:
		return false
	}
	return true
}
