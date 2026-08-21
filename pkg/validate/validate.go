// SPDX-License-Identifier: Apache-2.0

// Package validate checks a loaded dataset against a live NetBox without
// writing to it.
//
// The typed models and pkg/lint decide what is well-formed and what is
// consistent within the repository. Neither can decide what *this* server will
// accept: the choices an instance offers depend on its release and its
// plugins — a NetBox 4.6 has 216 interface types — and the objects a reference
// resolves to may exist only in NetBox. Those two questions have one authority
// each, the endpoint's OPTIONS response and the endpoint itself, and this asks
// them before a sync writes anything.
package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/lint"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// SchemaSource reports what an endpoint accepts. *client.NetBoxClient
// implements it; tests supply their own.
type SchemaSource interface {
	Schema(app, endpoint string) (client.EndpointSchema, error)
}

// ReferenceSource reports whether NetBox holds an object matching a filter.
type ReferenceSource interface {
	Filter(app, endpoint string, filters map[string]interface{}) ([]client.Object, error)
}

// Options tunes what is checked.
type Options struct {
	// SkipReferences checks values only, making no existence queries. Useful
	// against a large instance, or a token that may not read every endpoint.
	SkipReferences bool
}

// Check validates the dataset against the live instance and returns findings
// in the same shape pkg/lint produces, so both report identically.
//
// An error is returned only when the instance could not be questioned at all;
// anything it answered becomes a finding.
func Check(ds lint.Dataset, schemas SchemaSource, refs ReferenceSource, opts Options) ([]lint.Finding, error) {
	v := &validator{ds: ds, schemas: schemas, refs: refs}

	if err := v.checkValues(); err != nil {
		return nil, err
	}
	if !opts.SkipReferences && refs != nil {
		v.checkReferences()
	}

	sort.SliceStable(v.findings, func(i, j int) bool {
		a, b := v.findings[i], v.findings[j]
		if a.Severity != b.Severity {
			return a.Severity > b.Severity
		}
		if a.Object != b.Object {
			return a.Object < b.Object
		}
		return a.Check < b.Check
	})
	return v.findings, nil
}

type validator struct {
	ds       lint.Dataset
	schemas  SchemaSource
	refs     ReferenceSource
	findings []lint.Finding
}

// valueCheck is one declared value and the endpoint field NetBox will store it
// in. Collecting them first means each endpoint's schema is fetched once, and
// an instance that cannot answer for one endpoint does not stop the rest.
type valueCheck struct {
	app, endpoint string
	field         string
	value         string
	object        string
}

func (v *validator) checkValues() error {
	checks := v.collectValueChecks()

	byEndpoint := make(map[string][]valueCheck)
	for _, check := range checks {
		byEndpoint[check.app+"/"+check.endpoint] = append(byEndpoint[check.app+"/"+check.endpoint], check)
	}

	endpoints := make([]string, 0, len(byEndpoint))
	for endpoint := range byEndpoint {
		endpoints = append(endpoints, endpoint)
	}
	sort.Strings(endpoints)

	var unreachable int
	for _, key := range endpoints {
		group := byEndpoint[key]
		schema, err := v.schemas.Schema(group[0].app, group[0].endpoint)
		if err != nil {
			// One endpoint a token cannot read is a gap in coverage, not a
			// reason to fail the run; every endpoint failing is a real error.
			unreachable++
			v.add(lint.Warning, "schema-unavailable", key,
				"could not be read, so its values were not checked: %v", err)
			continue
		}
		for _, check := range group {
			v.checkValue(check, schema)
		}
	}

	if unreachable > 0 && unreachable == len(endpoints) {
		return fmt.Errorf("no endpoint schema could be read from NetBox; is the token allowed to read the API?")
	}
	return nil
}

func (v *validator) checkValue(check valueCheck, schema client.EndpointSchema) {
	if !schema.Allows(check.field, check.value) {
		message := fmt.Sprintf("%s %q is not a value this NetBox accepts", check.field, check.value)
		if suggestion := nearest(check.value, schema.Choices[check.field]); suggestion != "" {
			message += fmt.Sprintf("; did you mean %q?", suggestion)
		} else {
			message += fmt.Sprintf(" (it offers %d: %s)",
				len(schema.Choices[check.field]), preview(schema.Choices[check.field]))
		}
		v.add(lint.Error, "invalid-choice", check.object, "%s", message)
		return
	}

	if max, limited := schema.MaxLength[check.field]; limited && len([]rune(check.value)) > max {
		v.add(lint.Error, "value-too-long", check.object,
			"%s is %d characters; this NetBox stores at most %d",
			check.field, len([]rune(check.value)), max)
	}
}

// collectValueChecks walks the dataset and pairs every value NetBox constrains
// with the endpoint that constrains it.
func (v *validator) collectValueChecks() []valueCheck {
	var checks []valueCheck
	add := func(app, endpoint, field, value, object string) {
		if value == "" {
			return
		}
		checks = append(checks, valueCheck{app: app, endpoint: endpoint, field: field, value: value, object: object})
	}

	for _, site := range v.ds.Sites {
		ref := "site " + site.Slug
		add("dcim", "sites", "status", site.Status, ref)
		add("dcim", "sites", "name", site.Name, ref)
		add("dcim", "sites", "slug", site.Slug, ref)
	}

	for _, rack := range v.ds.Racks {
		ref := "rack " + rack.Slug
		add("dcim", "racks", "status", rack.Status, ref)
		add("dcim", "racks", "name", rack.Name, ref)
	}

	for _, dt := range v.ds.DeviceTypes {
		ref := "device type " + dt.Slug
		add("dcim", "device-types", "subdevice_role", dt.SubdeviceRole, ref)
		add("dcim", "device-types", "airflow", dt.Airflow, ref)
		add("dcim", "device-types", "weight_unit", dt.WeightUnit, ref)
		add("dcim", "device-types", "model", dt.Model, ref)
		add("dcim", "device-types", "slug", dt.Slug, ref)
		add("dcim", "device-types", "part_number", dt.PartNumber, ref)
		for _, tmpl := range dt.Interfaces {
			add("dcim", "interface-templates", "type", tmpl.Type,
				fmt.Sprintf("device type %s interface template %s", dt.Slug, tmpl.Name))
		}
		for _, port := range dt.FrontPorts {
			add("dcim", "front-port-templates", "type", port.Type,
				fmt.Sprintf("device type %s front port template %s", dt.Slug, port.Name))
		}
		for _, port := range dt.RearPorts {
			add("dcim", "rear-port-templates", "type", port.Type,
				fmt.Sprintf("device type %s rear port template %s", dt.Slug, port.Name))
		}
	}

	for _, mt := range v.ds.ModuleTypes {
		ref := "module type " + mt.Model
		add("dcim", "module-types", "airflow", mt.Airflow, ref)
		add("dcim", "module-types", "weight_unit", mt.WeightUnit, ref)
		add("dcim", "module-types", "model", mt.Model, ref)
		add("dcim", "module-types", "part_number", mt.PartNumber, ref)
		for _, tmpl := range mt.Interfaces {
			add("dcim", "interface-templates", "type", tmpl.Type,
				fmt.Sprintf("module type %s interface template %s", mt.Model, tmpl.Name))
		}
	}

	for _, vlan := range v.ds.VLANs {
		ref := fmt.Sprintf("VLAN %d", vlan.VID)
		add("ipam", "vlans", "status", vlan.Status, ref)
		add("ipam", "vlans", "name", vlan.Name, ref)
	}

	for _, prefix := range v.ds.Prefixes {
		add("ipam", "prefixes", "status", prefix.Status, "prefix "+prefix.Prefix)
	}

	for _, device := range v.ds.Devices {
		ref := "device " + device.Name
		add("dcim", "devices", "status", device.Status, ref)
		add("dcim", "devices", "face", device.Face, ref)
		add("dcim", "devices", "name", device.Name, ref)
		add("dcim", "devices", "serial", device.Serial, ref)
		add("dcim", "devices", "asset_tag", device.AssetTag, ref)

		for _, module := range device.Modules {
			add("dcim", "modules", "status", module.Status,
				fmt.Sprintf("device %s module %s", device.Name, module.Name))
			add("dcim", "modules", "serial", module.Serial,
				fmt.Sprintf("device %s module %s", device.Name, module.Name))
		}

		for _, iface := range device.Interfaces {
			iref := fmt.Sprintf("device %s interface %s", device.Name, iface.Name)
			add("dcim", "interfaces", "type", iface.Type, iref)
			add("dcim", "interfaces", "mode", iface.Mode, iref)
			add("dcim", "interfaces", "name", iface.Name, iref)
			add("dcim", "interfaces", "label", iface.Label, iref)
			v.addIPChecks(add, iface.IP, iref)
			v.addLinkChecks(add, iface.Link, iref)
		}
		for _, port := range device.FrontPorts {
			pref := fmt.Sprintf("device %s front port %s", device.Name, port.Name)
			add("dcim", "front-ports", "type", port.Type, pref)
			v.addLinkChecks(add, port.Link, pref)
		}
		for _, port := range device.RearPorts {
			pref := fmt.Sprintf("device %s rear port %s", device.Name, port.Name)
			add("dcim", "rear-ports", "type", port.Type, pref)
			v.addLinkChecks(add, port.Link, pref)
		}
	}

	for _, cluster := range v.ds.Clusters {
		add("virtualization", "clusters", "status", cluster.Status, "cluster "+cluster.Name)
	}

	for _, vm := range v.ds.VMs {
		ref := "virtual machine " + vm.Name
		add("virtualization", "virtual-machines", "status", vm.Status, ref)
		add("virtualization", "virtual-machines", "name", vm.Name, ref)
		for _, iface := range vm.Interfaces {
			iref := fmt.Sprintf("virtual machine %s interface %s", vm.Name, iface.Name)
			add("virtualization", "interfaces", "mode", iface.Mode, iref)
			add("virtualization", "interfaces", "name", iface.Name, iref)
			v.addIPChecks(add, iface.IP, iref)
		}
	}

	return checks
}

type addFunc func(app, endpoint, field, value, object string)

func (v *validator) addIPChecks(add addFunc, ip *models.IPConfig, object string) {
	if ip == nil {
		return
	}
	add("ipam", "ip-addresses", "status", ip.Status, object)
	add("ipam", "ip-addresses", "dns_name", ip.DNSName, object)
}

// addLinkChecks validates a cable's own fields. They are named after the port
// that declares the link, not the port itself, so a rejected cable type is not
// mistaken for a rejected interface type — NetBox calls both "type".
func (v *validator) addLinkChecks(add addFunc, link *models.LinkConfig, object string) {
	if link == nil {
		return
	}
	cable := "cable declared on " + object
	add("dcim", "cables", "type", link.CableType, cable)
	add("dcim", "cables", "length_unit", link.LengthUnit, cable)
}

// reference is one lookup to make against NetBox, deduplicated across the
// dataset so a hundred devices at one site ask about that site once.
type reference struct {
	app, endpoint string
	filters       map[string]interface{}
	kind          string
	value         string
}

func (v *validator) checkReferences() {
	pending := v.collectReferences()

	keys := make([]string, 0, len(pending))
	for key := range pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		ref := pending[key]
		objects, err := v.refs.Filter(ref.app, ref.endpoint, ref.filters)
		if err != nil {
			v.add(lint.Warning, "reference-unchecked", ref.kind+" "+ref.value,
				"could not be looked up in NetBox: %v", err)
			continue
		}
		if len(objects) == 0 {
			v.add(lint.Error, "missing-reference", ref.kind+" "+ref.value,
				"is declared nowhere in this repository and does not exist in NetBox either")
		}
	}
}

// collectReferences gathers the references this repository does not declare
// itself. Anything declared here will be created by the run that follows, so
// only the rest is worth asking NetBox about.
func (v *validator) collectReferences() map[string]reference {
	declared := v.declaredIdentifiers()
	pending := make(map[string]reference)

	need := func(kind, value, app, endpoint, field string) {
		if value == "" || declared[kind][value] {
			return
		}
		key := kind + "\x00" + value
		pending[key] = reference{
			app: app, endpoint: endpoint,
			filters: map[string]interface{}{field: value},
			kind:    kind, value: value,
		}
	}

	for _, device := range v.ds.Devices {
		need("site", device.SiteSlug, "dcim", "sites", "slug")
		need("device role", device.RoleSlug, "dcim", "device-roles", "slug")
		need("device type", device.DeviceTypeSlug, "dcim", "device-types", "slug")
		need("rack", device.RackSlug, "dcim", "racks", "slug")
		need("device", device.ParentDevice, "dcim", "devices", "name")
		for _, iface := range device.Interfaces {
			if iface.IP != nil {
				need("VRF", iface.IP.VRF, "ipam", "vrfs", "name")
			}
			if iface.Link != nil {
				need("device", iface.Link.PeerDevice, "dcim", "devices", "name")
			}
		}
		for _, port := range device.FrontPorts {
			if port.Link != nil {
				need("device", port.Link.PeerDevice, "dcim", "devices", "name")
			}
		}
		for _, port := range device.RearPorts {
			if port.Link != nil {
				need("device", port.Link.PeerDevice, "dcim", "devices", "name")
			}
		}
	}

	for _, vm := range v.ds.VMs {
		need("site", vm.SiteSlug, "dcim", "sites", "slug")
		need("device role", vm.RoleSlug, "dcim", "device-roles", "slug")
		need("cluster", vm.Cluster, "virtualization", "clusters", "name")
		need("platform", vm.Platform, "dcim", "platforms", "slug")
		need("tenant", vm.Tenant, "tenancy", "tenants", "slug")
	}

	return pending
}

// declaredIdentifiers indexes what this repository creates, by the same
// identifier the reference uses.
func (v *validator) declaredIdentifiers() map[string]map[string]bool {
	declared := map[string]map[string]bool{
		"site": {}, "device role": {}, "device type": {}, "rack": {},
		"device": {}, "VRF": {}, "cluster": {}, "platform": {}, "tenant": {},
	}
	for _, s := range v.ds.Sites {
		declared["site"][s.Slug] = true
	}
	for _, r := range v.ds.Roles {
		declared["device role"][r.Slug] = true
	}
	for _, dt := range v.ds.DeviceTypes {
		declared["device type"][dt.Slug] = true
	}
	for _, r := range v.ds.Racks {
		declared["rack"][r.Slug] = true
	}
	for _, d := range v.ds.Devices {
		declared["device"][d.Name] = true
	}
	for _, vrf := range v.ds.VRFs {
		declared["VRF"][vrf.Name] = true
	}
	for _, c := range v.ds.Clusters {
		declared["cluster"][c.Name] = true
	}
	for _, p := range v.ds.Platforms {
		declared["platform"][p.Slug] = true
		declared["platform"][p.Name] = true
	}
	for _, t := range v.ds.Tenants {
		declared["tenant"][t.Slug] = true
		declared["tenant"][t.Name] = true
	}
	return declared
}

func (v *validator) add(severity lint.Severity, check, object, format string, args ...interface{}) {
	v.findings = append(v.findings, lint.Finding{
		Severity: severity,
		Check:    check,
		Object:   object,
		Message:  fmt.Sprintf(format, args...),
	})
}

// preview renders the first few choices of a long list, so a message about an
// interface type does not print all 216.
func preview(choices []string) string {
	const shown = 5
	sorted := append([]string(nil), choices...)
	sort.Strings(sorted)
	if len(sorted) <= shown {
		return strings.Join(sorted, ", ")
	}
	return strings.Join(sorted[:shown], ", ") + ", …"
}

// nearest returns the choice closest to value, when one is close enough to be
// worth suggesting. A typo in an interface type is one or two characters away
// from what was meant; anything further is a different value, and guessing
// would mislead.
func nearest(value string, choices []string) string {
	const maxDistance = 3

	best, bestDistance := "", maxDistance+1
	for _, choice := range choices {
		if distance := editDistance(value, choice); distance < bestDistance {
			best, bestDistance = choice, distance
		}
	}
	if bestDistance > maxDistance {
		return ""
	}
	return best
}

// editDistance is the Levenshtein distance between two strings.
func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}
