// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"errors"
	"fmt"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// patchPanelRole is the role a device must have to be a hop on a cable path.
// It is the same role the reconciler keys its front/rear port selection on,
// so a path can only run through devices whose cables it will wire as panel
// cables.
const patchPanelRole = "patch-panel"

// ExpandCablePaths turns every `link.via` path into the cables NetBox holds
// and attaches them to the devices of the dataset, so that the reconciler and
// the lint see plain cables and nothing else has to know the short form.
//
// For a path declared on srv[eth1] as via [A:2, B:2] to leaf[Eth1/2]:
//
//	srv[eth1]      -- A[front 2]      the link itself, peer rewritten to A:2
//	A[rear r]      == B[rear r']      a link on A's rear port r, where r is the
//	                                  rear port A's device type maps front 2 to
//	B[front 2]     -- leaf[Eth1/2]    a link on B's front port 2
//
// The cable attributes of the link apply to every segment; a hop's own
// attributes override them for the segment leaving that hop. A rear or front
// port entry the panel already declares is reused: a port without a link gets
// one, a port whose link already names the same far end is left alone, and a
// port cabled elsewhere is a conflict.
//
// It returns one line per path, every port along it, for the log: with the
// strand written in one place, the log is where the three cables can be seen.
func ExpandCablePaths(ds *Dataset) ([]string, error) {
	devices := make(map[string]*models.DeviceConfig, len(ds.Devices))
	for _, d := range ds.Devices {
		devices[d.Name] = d
	}
	deviceTypes := make(map[string]*models.DeviceType, len(ds.DeviceTypes))
	for _, dt := range ds.DeviceTypes {
		deviceTypes[dt.Slug] = dt
	}

	var paths []string
	var errs []error
	for _, d := range ds.Devices {
		for i := range d.Interfaces {
			iface := &d.Interfaces[i]
			if iface.Link == nil || len(iface.Link.Via) == 0 {
				continue
			}
			path, err := expandPath(d, iface, devices, deviceTypes)
			if err != nil {
				errs = append(errs, fmt.Errorf("device %q interface %q: cable path %s: %w", d.Name, iface.Name, describePath(d, iface), err))
				continue
			}
			paths = append(paths, path)
		}
	}
	return paths, errors.Join(errs...)
}

// describePath renders a via link as the user wrote it, for error messages.
func describePath(d *models.DeviceConfig, iface *models.InterfaceConfig) string {
	parts := []string{d.Name + "[" + iface.Name + "]"}
	for _, hop := range iface.Link.Via {
		parts = append(parts, hop.Device+":"+hop.Port)
	}
	parts = append(parts, iface.Link.PeerDevice+"["+iface.Link.PeerPort+"]")
	return strings.Join(parts, " > ")
}

func expandPath(d *models.DeviceConfig, iface *models.InterfaceConfig, devices map[string]*models.DeviceConfig, deviceTypes map[string]*models.DeviceType) (string, error) {
	link := iface.Link
	// The far end as the user wrote it; the link itself is rewritten below to
	// point at the first panel.
	farDevice, farPort := link.PeerDevice, link.PeerPort

	// Two panels is the shape of a strand between racks: cord in on A's front,
	// backbone A rear to B rear, cord out of B's front. One panel is a cord
	// into its front and the rear port wired straight on. More would need a
	// front-to-front cord between two panels, which the reconciler wires as
	// rear-to-rear (any panel-to-panel cable is a backbone), so it is refused
	// rather than wired wrong.
	if len(link.Via) > 2 {
		return "", fmt.Errorf("a path runs through at most two patch panels (cord in, backbone, cord out); %d given — declare a longer run as separate cables on the panels' ports", len(link.Via))
	}
	if far, ok := devices[farDevice]; ok && far.RoleSlug == patchPanelRole {
		return "", fmt.Errorf("the far end %s is a patch panel; a path ends at a device port, so name the panel under via and the device behind it as the peer", farDevice)
	}

	type panelHop struct {
		panel   *models.DeviceConfig
		front   string
		rear    string
		rearPos int
		hop     models.ViaHop
	}
	resolved := make([]panelHop, 0, len(link.Via))
	for _, hop := range link.Via {
		panel, ok := devices[hop.Device]
		if !ok {
			return "", fmt.Errorf("patch panel %q is not declared in this repository; a path can only run through declared panels", hop.Device)
		}
		if panel.RoleSlug != patchPanelRole {
			return "", fmt.Errorf("%s has role %q, but a path can only run through devices with role %q (that role is how its cables are wired to front and rear ports)", hop.Device, panel.RoleSlug, patchPanelRole)
		}
		rear, pos, err := frontToRear(panel, deviceTypes[panel.DeviceTypeSlug], hop.Port)
		if err != nil {
			return "", err
		}
		resolved = append(resolved, panelHop{panel: panel, front: hop.Port, rear: rear, rearPos: pos, hop: hop})
	}

	// The first cord is the link itself, now pointing at the first panel's
	// front port; the panel's front defaults fill what the link left out.
	base := *link
	first := resolved[0]
	*link = *segmentAttrs{base: base, panel: panelDefaults(first.panel).Front}.link(first.panel.Name, first.front)

	path := []string{d.Name + "[" + iface.Name + "]"}
	for i, hop := range resolved {
		frontPort, rearPort := hop.panel.Name+"[front "+hop.front+"]", hop.panel.Name+"[rear "+hop.rear+"]"
		defaults := panelDefaults(hop.panel)
		switch {
		case i == 0 && len(resolved) == 2:
			// Entered on the front, the backbone leaves the rear.
			next := resolved[1]
			path = append(path, frontPort, rearPort)
			// The backbone is the panels' infrastructure, not part of what
			// was patched: it takes the panel's rear defaults, and only a
			// hop written out can say otherwise. The link's own
			// attributes describe its patch cords.
			backbone := segmentAttrs{hop: hop.hop, panel: defaults.Rear}.link(next.panel.Name, next.rear)
			if err := ensureRearLink(hop.panel, hop.rear, backbone); err != nil {
				return "", err
			}
		case i == 0:
			// A single panel: in on the front, straight on from the rear.
			path = append(path, frontPort, rearPort)
			cord := segmentAttrs{base: base, hop: hop.hop, panel: defaults.Rear}.link(farDevice, farPort)
			if err := ensureRearLink(hop.panel, hop.rear, cord); err != nil {
				return "", err
			}
		default:
			// The second panel: the backbone arrives on the rear, the last
			// cord leaves the front.
			path = append(path, rearPort, frontPort)
			cord := segmentAttrs{base: base, hop: hop.hop, panel: defaults.Front}.link(farDevice, farPort)
			if err := ensureFrontLink(hop.panel, hop.front, hop.rear, hop.rearPos, cord); err != nil {
				return "", err
			}
		}
	}
	path = append(path, farDevice+"["+farPort+"]")
	return strings.Join(path, " > "), nil
}

// segmentAttrs resolves the cable attributes of one segment of a path: the
// hop's own values, then the link's (for the patch cords; a backbone is built
// without them), then the defaults of the patch panel the segment belongs to.
// Only a segment a path produced is filled from a panel's cable_defaults; a
// cable declared on its own is whatever it says it is, since it may well be
// a different kind of cable than the panel's cords.
type segmentAttrs struct {
	base  models.LinkConfig
	hop   models.ViaHop
	panel models.CableAttrs
}

func (s segmentAttrs) link(peerDevice, peerPort string) *models.LinkConfig {
	out := &models.LinkConfig{PeerDevice: peerDevice, PeerPort: peerPort}
	pick := func(hop, link, panel string) string {
		switch {
		case hop != "":
			return hop
		case link != "":
			return link
		}
		return panel
	}
	out.CableType = pick(s.hop.CableType, s.base.CableType, s.panel.CableType)
	out.Color = pick(s.hop.Color, s.base.Color, s.panel.Color)
	switch {
	case s.hop.Length > 0:
		out.Length, out.LengthUnit = s.hop.Length, s.hop.LengthUnit
	case s.base.Length > 0:
		out.Length, out.LengthUnit = s.base.Length, s.base.LengthUnit
	case s.panel.Length > 0:
		out.Length, out.LengthUnit = s.panel.Length, s.panel.LengthUnit
	}
	return out
}

// panelDefaults returns a panel's cable defaults, empty when it declares none.
func panelDefaults(panel *models.DeviceConfig) models.CableDefaults {
	if panel.CableDefaults == nil {
		return models.CableDefaults{}
	}
	return *panel.CableDefaults
}

// frontToRear returns the rear port and position a panel's front port maps to,
// from the device type's template first and the panel's own declaration as a
// fallback.
func frontToRear(panel *models.DeviceConfig, dt *models.DeviceType, front string) (string, int, error) {
	if dt != nil {
		for _, p := range dt.FrontPorts {
			if p.Name == front && p.RearPort != "" {
				return p.RearPort, p.RearPortPosition, nil
			}
		}
	}
	for _, p := range panel.FrontPorts {
		if p.Name == front && p.RearPort != "" {
			return p.RearPort, p.RearPortPosition, nil
		}
	}
	if dt == nil {
		return "", 0, fmt.Errorf("%s has no front port %q declared with a rear_port, and its device type %q is not declared here to look it up", panel.Name, front, panel.DeviceTypeSlug)
	}
	return "", 0, fmt.Errorf("%s has no front port %q: device type %s defines none of that name and the panel declares none", panel.Name, front, dt.Slug)
}

// ensureRearLink puts a cable on a panel's rear port, reusing a declared
// entry when there is one.
func ensureRearLink(panel *models.DeviceConfig, name string, link *models.LinkConfig) error {
	for i := range panel.RearPorts {
		p := &panel.RearPorts[i]
		if p.Name != name {
			continue
		}
		return mergeLink(panel.Name+"[rear "+name+"]", &p.Link, link)
	}
	panel.RearPorts = append(panel.RearPorts, models.RearPortConfig{Name: name, Link: link})
	return nil
}

// ensureFrontLink puts a cable on a panel's front port, reusing a declared
// entry when there is one. A new entry carries the template's rear port
// mapping, which the reconciler needs to apply the port.
func ensureFrontLink(panel *models.DeviceConfig, name, rear string, rearPos int, link *models.LinkConfig) error {
	for i := range panel.FrontPorts {
		p := &panel.FrontPorts[i]
		if p.Name != name {
			continue
		}
		if p.RearPort == "" {
			p.RearPort, p.RearPortPosition = rear, rearPos
		}
		return mergeLink(panel.Name+"[front "+name+"]", &p.Link, link)
	}
	panel.FrontPorts = append(panel.FrontPorts, models.FrontPortConfig{Name: name, RearPort: rear, RearPortPosition: rearPos, Link: link})
	return nil
}

// mergeLink sets a port's link, keeps one that already names the same far end
// (two paths sharing a backbone strand), and rejects one cabled elsewhere.
func mergeLink(port string, existing **models.LinkConfig, link *models.LinkConfig) error {
	if *existing == nil {
		*existing = link
		return nil
	}
	if (*existing).PeerDevice == link.PeerDevice && (*existing).PeerPort == link.PeerPort {
		return nil
	}
	return fmt.Errorf("%s is already cabled to %s[%s]; the path needs it cabled to %s[%s]",
		port, (*existing).PeerDevice, (*existing).PeerPort, link.PeerDevice, link.PeerPort)
}
