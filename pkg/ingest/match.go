// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// DeviceMatch pairs a discovered device with the declaration it resolved to.
// A nil Declared means the repository has never heard of this machine.
type DeviceMatch struct {
	Discovered discovery.Device
	Declared   *loader.Declared[*models.DeviceConfig]
	// Rule names the rule that resolved the match ("mgmt-ip", "serial",
	// "service-tag", "name"), so the summary can say how a device was
	// recognised and a surprising match is traceable.
	Rule string
}

// VMMatch pairs a discovered VM with the declaration it resolved to.
type VMMatch struct {
	Discovered discovery.VM
	Declared   *loader.Declared[*models.VMConfig]
	Rule       string
}

// Matches is the outcome of resolving a whole snapshot.
type Matches struct {
	Devices []DeviceMatch
	VMs     []VMMatch
	// Superseded lists declarations in a generated file that describe an object
	// a hand-managed file now declares too. They are dropped from the generated
	// file, which is how adopting a discovered object works: put the block
	// where you want it, and the next run takes it out of the machine's file.
	Superseded []loader.Provenance
}

// isGenerated reports whether a declaration lives in a file ingest wrote.
//
// This is the one place the distinction exists, and it is deliberately narrow:
// a generated file is an ordinary declaration in every respect except that a
// hand-managed file outranks it for the same object.
func isGenerated(prov loader.Provenance) bool {
	normalized := filepath.ToSlash(prov.File)
	return strings.Contains(normalized, DiscoveredRoot+"/")
}

// preferHandManaged splits candidates into the hand-managed ones and the
// generated ones.
func preferHandManaged[T any](candidates []loader.Declared[T]) (hand, generated []loader.Declared[T]) {
	for _, c := range candidates {
		if isGenerated(c.Prov) {
			generated = append(generated, c)
		} else {
			hand = append(hand, c)
		}
	}
	return hand, generated
}

// Match resolves every object in a snapshot against the declared inventory.
//
// The rules are tried in order of how much they prove. The management address
// is first because it is not a claim the machine makes about itself: it is how
// the scan reached this machine and not another one. Serial and service tag
// come next, and the name last, because a name is the thing humans most often
// reuse.
//
// A rule that matches more than one declaration is an error listing the
// candidates, never a guess — the same bargain rename_from strikes. Writing a
// discovered serial into the wrong device's block would be a quiet, plausible
// corruption of the inventory, and no heuristic is worth that.
func Match(snap *discovery.Snapshot, inv loader.Inventory) (Matches, error) {
	var out Matches
	var errs []error

	// A declaration may be claimed by at most one discovered object: two
	// machines resolving to one block means the inventory cannot tell them
	// apart, which is the operator's problem to fix, not ours to pick a winner
	// for.
	claimedDevice := map[*models.DeviceConfig]discovery.Device{}
	claimedVM := map[*models.VMConfig]discovery.VM{}

	for _, dev := range snap.Devices {
		declared, rule, superseded, err := matchDevice(dev, inv.Devices)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out.Superseded = append(out.Superseded, superseded...)
		if declared != nil {
			if previous, taken := claimedDevice[declared.Object]; taken {
				errs = append(errs, fmt.Errorf(
					"discovered devices %s and %s both resolve to the declared device %q (%s); "+
						"give them distinct serials, asset tags or management addresses in the YAML",
					describeDevice(previous), describeDevice(dev), declared.Object.Name, declared.Prov.Ref()))
				continue
			}
			claimedDevice[declared.Object] = dev
		}
		out.Devices = append(out.Devices, DeviceMatch{Discovered: dev, Declared: declared, Rule: rule})
	}

	for _, vm := range snap.VMs {
		declared, rule, superseded, err := matchVM(vm, inv.VMs)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out.Superseded = append(out.Superseded, superseded...)
		if declared != nil {
			if previous, taken := claimedVM[declared.Object]; taken {
				errs = append(errs, fmt.Errorf(
					"discovered VMs %q and %q both resolve to the declared VM %q (%s)",
					previous.Name, vm.Name, declared.Object.Name, declared.Prov.Ref()))
				continue
			}
			claimedVM[declared.Object] = vm
		}
		out.VMs = append(out.VMs, VMMatch{Discovered: vm, Declared: declared, Rule: rule})
	}

	if err := errors.Join(errs...); err != nil {
		return Matches{}, err
	}
	return out, nil
}

// deviceRule is one way of recognising a scanned machine in the YAML.
type deviceRule struct {
	name string
	// candidates returns the declarations this rule considers equal to the
	// discovered device.
	candidates func(discovery.Device, []loader.Declared[*models.DeviceConfig]) []loader.Declared[*models.DeviceConfig]
}

// deviceRules are tried in this order; the first that finds exactly one
// candidate wins.
var deviceRules = []deviceRule{
	{"mgmt-ip", candidatesByMgmtIP},
	{"serial", candidatesBySerial},
	{"service-tag", candidatesByServiceTag},
	{"name", candidatesByDeviceName},
}

func matchDevice(dev discovery.Device, declared []loader.Declared[*models.DeviceConfig]) (*loader.Declared[*models.DeviceConfig], string, []loader.Provenance, error) {
	for _, rule := range deviceRules {
		found := rule.candidates(dev, declared)
		if len(found) == 0 {
			continue
		}
		if len(found) == 1 {
			return &found[0], rule.name, nil, nil
		}

		// Several candidates. If exactly one of them is hand-managed, the
		// ambiguity is not a mistake: it is somebody adopting a machine this
		// tool wrote a file for. The hand-managed declaration wins and the
		// generated one is dropped.
		hand, generated := preferHandManaged(found)
		if len(hand) == 1 {
			return &hand[0], rule.name, provenancesOf(generated), nil
		}

		return nil, "", nil, fmt.Errorf(
			"discovered device %s matches %d declared devices by %s: %s; "+
				"the repository cannot say which one it is, so nothing was written",
			describeDevice(dev), len(found), rule.name, describeCandidates(found))
	}
	return nil, "", nil, nil
}

// provenancesOf extracts the provenance of each declaration.
func provenancesOf[T any](declared []loader.Declared[T]) []loader.Provenance {
	provs := make([]loader.Provenance, 0, len(declared))
	for _, d := range declared {
		provs = append(provs, d.Prov)
	}
	return provs
}

// candidatesByMgmtIP finds the declarations carrying the address the device was
// scanned on. When several do, the management-looking interfaces win: a server
// and the switch it hangs off may both list the same address in a description,
// but only one of them answers on it as a BMC.
func candidatesByMgmtIP(dev discovery.Device, declared []loader.Declared[*models.DeviceConfig]) []loader.Declared[*models.DeviceConfig] {
	if dev.MgmtIP == "" {
		return nil
	}
	want := hostOfAddress(dev.MgmtIP)
	if want == "" {
		return nil
	}

	var all, mgmtOnly []loader.Declared[*models.DeviceConfig]
	for _, d := range declared {
		matched, onMgmt := false, false
		for _, iface := range d.Object.Interfaces {
			if iface.IP == nil || hostOfAddress(iface.IP.Address) != want {
				continue
			}
			matched = true
			if isManagementInterface(iface.Name) {
				onMgmt = true
			}
		}
		if matched {
			all = append(all, d)
			if onMgmt {
				mgmtOnly = append(mgmtOnly, d)
			}
		}
	}

	if len(mgmtOnly) > 0 {
		return mgmtOnly
	}
	return all
}

func candidatesBySerial(dev discovery.Device, declared []loader.Declared[*models.DeviceConfig]) []loader.Declared[*models.DeviceConfig] {
	return candidatesByField(dev.Serial, declared, func(d *models.DeviceConfig) string { return d.Serial })
}

// candidatesByServiceTag matches the scanned service tag against the declared
// asset tag, which is where a Dell service tag belongs in NetBox.
func candidatesByServiceTag(dev discovery.Device, declared []loader.Declared[*models.DeviceConfig]) []loader.Declared[*models.DeviceConfig] {
	return candidatesByField(dev.ServiceTag, declared, func(d *models.DeviceConfig) string { return d.AssetTag })
}

func candidatesByDeviceName(dev discovery.Device, declared []loader.Declared[*models.DeviceConfig]) []loader.Declared[*models.DeviceConfig] {
	// A BMC commonly reports a fully qualified name while the YAML holds the
	// short one; compare both spellings of what the scan said.
	names := []string{dev.Name}
	if short, _, found := strings.Cut(dev.Name, "."); found && short != "" {
		names = append(names, short)
	}

	var found []loader.Declared[*models.DeviceConfig]
	seen := map[*models.DeviceConfig]bool{}
	for _, name := range names {
		for _, d := range candidatesByField(name, declared, func(d *models.DeviceConfig) string { return d.Name }) {
			if !seen[d.Object] {
				seen[d.Object] = true
				found = append(found, d)
			}
		}
	}
	return found
}

// candidatesByField is the shared body of the identifier rules: an empty
// discovered value matches nothing (absence is not a value), and comparison is
// case-insensitive because serials and tags are transcribed by hand.
func candidatesByField(value string, declared []loader.Declared[*models.DeviceConfig], get func(*models.DeviceConfig) string) []loader.Declared[*models.DeviceConfig] {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var found []loader.Declared[*models.DeviceConfig]
	for _, d := range declared {
		if strings.EqualFold(strings.TrimSpace(get(d.Object)), value) {
			found = append(found, d)
		}
	}
	return found
}

func matchVM(vm discovery.VM, declared []loader.Declared[*models.VMConfig]) (*loader.Declared[*models.VMConfig], string, []loader.Provenance, error) {
	byName := make([]loader.Declared[*models.VMConfig], 0, 1)
	for _, d := range declared {
		if strings.EqualFold(strings.TrimSpace(d.Object.Name), strings.TrimSpace(vm.Name)) {
			byName = append(byName, d)
		}
	}
	if len(byName) == 0 {
		return nil, "", nil, nil
	}

	// A VM name is only unique within a cluster, so try the pair first.
	if vm.Cluster != "" {
		var inCluster []loader.Declared[*models.VMConfig]
		for _, d := range byName {
			if strings.EqualFold(d.Object.Cluster, vm.Cluster) {
				inCluster = append(inCluster, d)
			}
		}
		switch len(inCluster) {
		case 1:
			return &inCluster[0], "name+cluster", nil, nil
		case 0:
			// Fall through: the VM may be declared without a cluster.
		default:
			if hand, generated := preferHandManaged(inCluster); len(hand) == 1 {
				return &hand[0], "name+cluster", provenancesOf(generated), nil
			}
			return nil, "", nil, fmt.Errorf(
				"discovered VM %q matches %d declared VMs of the same name in cluster %q: %s",
				vm.Name, len(inCluster), vm.Cluster, describeVMCandidates(inCluster))
		}
	}

	if len(byName) > 1 {
		if hand, generated := preferHandManaged(byName); len(hand) == 1 {
			return &hand[0], "name", provenancesOf(generated), nil
		}
		return nil, "", nil, fmt.Errorf(
			"discovered VM %q matches %d declared VMs by name: %s; "+
				"declare the cluster on each so they can be told apart",
			vm.Name, len(byName), describeVMCandidates(byName))
	}
	return &byName[0], "name", nil, nil
}

// isManagementInterface reports whether an interface name reads like an
// out-of-band management port. It only ever breaks a tie between interfaces
// that already carry the scanned address, so a name this does not recognise
// costs nothing but the tie-break.
func isManagementInterface(name string) bool {
	name = strings.ToLower(name)
	for _, marker := range []string{"idrac", "drac", "ilo", "ipmi", "bmc", "mgmt", "management", "oob", "cimc"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

// hostOfAddress strips a prefix length, so "10.0.0.5/24" and "10.0.0.5"
// compare equal.
func hostOfAddress(address string) string {
	host, _, _ := strings.Cut(strings.TrimSpace(address), "/")
	return host
}

// describeDevice names a discovered device by whatever it actually carries.
func describeDevice(dev discovery.Device) string {
	var parts []string
	if dev.Name != "" {
		parts = append(parts, fmt.Sprintf("%q", dev.Name))
	}
	if dev.MgmtIP != "" {
		parts = append(parts, "at "+dev.MgmtIP)
	}
	if dev.Serial != "" {
		parts = append(parts, "serial "+dev.Serial)
	}
	if dev.ServiceTag != "" && dev.ServiceTag != dev.Serial {
		parts = append(parts, "service tag "+dev.ServiceTag)
	}
	if len(parts) == 0 {
		return "(an entry carrying no identifiers)"
	}
	return strings.Join(parts, " ")
}

// describeCandidates lists ambiguous declarations with their locations, so the
// error says where to go and fix it.
func describeCandidates(candidates []loader.Declared[*models.DeviceConfig]) string {
	described := make([]string, 0, len(candidates))
	for _, c := range candidates {
		described = append(described, fmt.Sprintf("%q (%s)", c.Object.Name, c.Prov.Ref()))
	}
	sort.Strings(described)
	return strings.Join(described, ", ")
}

func describeVMCandidates(candidates []loader.Declared[*models.VMConfig]) string {
	described := make([]string, 0, len(candidates))
	for _, c := range candidates {
		described = append(described, fmt.Sprintf("%q (%s)", c.Object.Name, c.Prov.Ref()))
	}
	sort.Strings(described)
	return strings.Join(described, ", ")
}
