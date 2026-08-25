// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"path"
	"sort"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// deviceTypes imports device types and module types with every component
// template. Templates are fetched once per endpoint and joined in memory, so a
// large catalogue costs a fixed handful of requests, not one per type.
func (rc *runContext) deviceTypes() error {
	if err := rc.importDeviceTypes(); err != nil {
		return err
	}
	return rc.importModuleTypes()
}

// templateSet holds the component templates of one owner (device or module
// type), grouped by the owner's NetBox id.
type templateSet struct {
	interfaces         map[int][]client.Object
	frontPorts         map[int][]client.Object
	rearPorts          map[int][]client.Object
	consolePorts       map[int][]client.Object
	consoleServerPorts map[int][]client.Object
	powerPorts         map[int][]client.Object
	powerOutlets       map[int][]client.Object
	moduleBays         map[int][]client.Object
	deviceBays         map[int][]client.Object
}

// fetchTemplates loads every component-template endpoint and groups it by the
// given owner reference ("device_type" or "module_type").
func (rc *runContext) fetchTemplates(ownerRef string, withDeviceBays bool) (templateSet, error) {
	get := func(endpoint string) (map[int][]client.Object, error) {
		objs, err := rc.f.list("dcim", endpoint, nil)
		if err != nil {
			return nil, err
		}
		return groupBy(objs, ownerRef), nil
	}
	var ts templateSet
	var err error
	if ts.interfaces, err = get("interface-templates"); err != nil {
		return ts, err
	}
	if ts.frontPorts, err = get("front-port-templates"); err != nil {
		return ts, err
	}
	if ts.rearPorts, err = get("rear-port-templates"); err != nil {
		return ts, err
	}
	if ts.consolePorts, err = get("console-port-templates"); err != nil {
		return ts, err
	}
	if ts.consoleServerPorts, err = get("console-server-port-templates"); err != nil {
		return ts, err
	}
	if ts.powerPorts, err = get("power-port-templates"); err != nil {
		return ts, err
	}
	if ts.powerOutlets, err = get("power-outlet-templates"); err != nil {
		return ts, err
	}
	if ts.moduleBays, err = get("module-bay-templates"); err != nil {
		return ts, err
	}
	if withDeviceBays {
		if ts.deviceBays, err = get("device-bay-templates"); err != nil {
			return ts, err
		}
	}
	return ts, nil
}

func (rc *runContext) importDeviceTypes() error {
	objs, err := rc.f.list("dcim", "device-types", nil)
	if err != nil {
		return err
	}
	ts, err := rc.fetchTemplates("device_type", true)
	if err != nil {
		return err
	}

	exported := 0
	for _, o := range objs {
		if !rc.keep(o) {
			continue
		}
		id := idOf(o)
		mfg := refName(o, "manufacturer")
		dt := &models.DeviceType{
			Model:              str(o, "model"),
			Slug:               str(o, "slug"),
			Manufacturer:       mfg,
			PartNumber:         str(o, "part_number"),
			UHeight:            floatOf(o, "u_height"),
			IsFullDepth:        boolOf(o, "is_full_depth"),
			SubdeviceRole:      choiceValue(o, "subdevice_role"),
			Airflow:            choiceValue(o, "airflow"),
			Description:        str(o, "description"),
			Comments:           str(o, "comments"),
			Weight:             floatOf(o, "weight"),
			WeightUnit:         choiceValue(o, "weight_unit"),
			Tags:               rc.tags(o),
			Interfaces:         interfaceTemplates(ts.interfaces[id]),
			FrontPorts:         portTemplates(ts.frontPorts[id]),
			RearPorts:          portTemplates(ts.rearPorts[id]),
			ConsolePorts:       consoleTemplates(ts.consolePorts[id]),
			ConsoleServerPorts: consoleTemplates(ts.consoleServerPorts[id]),
			PowerPorts:         powerPortTemplates(ts.powerPorts[id]),
			PowerOutlets:       powerOutletTemplates(ts.powerOutlets[id]),
			ModuleBays:         moduleBayTemplates(ts.moduleBays[id]),
			DeviceBays:         deviceBayTemplates(ts.deviceBays[id]),
		}
		file := path.Join("definitions/device_types", fileSeg(mfg), fileSeg(dt.Slug)+".yaml")
		if err := rc.emit(file,
			genHeader("Device type \""+dt.Model+"\" and its component templates."),
			"items", []interface{}{dt}); err != nil {
			return err
		}
		exported++
	}
	rc.report.count("dcim/device-types", len(objs), exported)
	return nil
}

func (rc *runContext) importModuleTypes() error {
	objs, err := rc.f.list("dcim", "module-types", nil)
	if err != nil {
		return err
	}
	ts, err := rc.fetchTemplates("module_type", false)
	if err != nil {
		return err
	}

	// Group module types into one file per manufacturer.
	byMfg := map[string][]interface{}{}
	exported := 0
	for _, o := range objs {
		if !rc.keep(o) {
			continue
		}
		id := idOf(o)
		mfg := refName(o, "manufacturer")
		mt := &models.ModuleType{
			Model: str(o, "model"),
			// NetBox has no slug for a module type (its identity is
			// manufacturer + model), but this schema references one by slug and
			// --strict rejects a module type without it. Synthesise the same
			// slug the loader derives, so the definition and every module
			// reference agree.
			Slug:               moduleTypeSlug(o),
			Manufacturer:       mfg,
			PartNumber:         str(o, "part_number"),
			Airflow:            choiceValue(o, "airflow"),
			Description:        str(o, "description"),
			Comments:           str(o, "comments"),
			Weight:             floatOf(o, "weight"),
			WeightUnit:         choiceValue(o, "weight_unit"),
			Tags:               rc.tags(o),
			Interfaces:         interfaceTemplates(ts.interfaces[id]),
			FrontPorts:         portTemplates(ts.frontPorts[id]),
			RearPorts:          portTemplates(ts.rearPorts[id]),
			ConsolePorts:       consoleTemplates(ts.consolePorts[id]),
			ConsoleServerPorts: consoleTemplates(ts.consoleServerPorts[id]),
			PowerPorts:         powerPortTemplates(ts.powerPorts[id]),
			PowerOutlets:       powerOutletTemplates(ts.powerOutlets[id]),
			ModuleBays:         moduleBayTemplates(ts.moduleBays[id]),
		}
		byMfg[mfg] = append(byMfg[mfg], mt)
		exported++
	}
	rc.report.count("dcim/module-types", len(objs), exported)

	for mfg, items := range byMfg {
		sort.Slice(items, func(i, j int) bool {
			return items[i].(*models.ModuleType).Model < items[j].(*models.ModuleType).Model
		})
		file := path.Join("definitions/module_types", fileSeg(mfg)+".yaml")
		if err := rc.emit(file,
			genHeader("Module types manufactured by \""+mfg+"\"."), "items", items); err != nil {
			return err
		}
	}
	return nil
}

// The component-template mappers. Each sorts by name so output is byte-stable.

func interfaceTemplates(objs []client.Object) []models.InterfaceTemplate {
	sortByName(objs)
	var out []models.InterfaceTemplate
	for _, o := range objs {
		out = append(out, models.InterfaceTemplate{
			Name:        str(o, "name"),
			Type:        choiceValue(o, "type"),
			Label:       str(o, "label"),
			Description: str(o, "description"),
			MgmtOnly:    boolOf(o, "mgmt_only"),
			Enabled:     enabledPtr(o),
			PoEMode:     choiceValue(o, "poe_mode"),
			PoEType:     choiceValue(o, "poe_type"),
			RFRole:      choiceValue(o, "rf_role"),
		})
	}
	return out
}

func portTemplates(objs []client.Object) []models.PortTemplate {
	sortByName(objs)
	var out []models.PortTemplate
	for _, o := range objs {
		out = append(out, models.PortTemplate{
			Name:             str(o, "name"),
			Type:             choiceValue(o, "type"),
			Label:            str(o, "label"),
			Description:      str(o, "description"),
			Color:            str(o, "color"),
			RearPort:         refName(o, "rear_port"),
			Positions:        intOf(o, "positions"),
			RearPortPosition: intOf(o, "rear_port_position"),
		})
	}
	return out
}

func consoleTemplates(objs []client.Object) []models.ConsolePortTemplate {
	sortByName(objs)
	var out []models.ConsolePortTemplate
	for _, o := range objs {
		out = append(out, models.ConsolePortTemplate{
			Name:        str(o, "name"),
			Type:        choiceValue(o, "type"),
			Label:       str(o, "label"),
			Description: str(o, "description"),
		})
	}
	return out
}

func powerPortTemplates(objs []client.Object) []models.PowerPortTemplate {
	sortByName(objs)
	var out []models.PowerPortTemplate
	for _, o := range objs {
		out = append(out, models.PowerPortTemplate{
			Name:          str(o, "name"),
			Type:          choiceValue(o, "type"),
			Label:         str(o, "label"),
			Description:   str(o, "description"),
			MaximumDraw:   intOf(o, "maximum_draw"),
			AllocatedDraw: intOf(o, "allocated_draw"),
		})
	}
	return out
}

func powerOutletTemplates(objs []client.Object) []models.PowerOutletTemplate {
	sortByName(objs)
	var out []models.PowerOutletTemplate
	for _, o := range objs {
		out = append(out, models.PowerOutletTemplate{
			Name:        str(o, "name"),
			Type:        choiceValue(o, "type"),
			Label:       str(o, "label"),
			Description: str(o, "description"),
			PowerPort:   refName(o, "power_port"),
			FeedLeg:     choiceValue(o, "feed_leg"),
		})
	}
	return out
}

func moduleBayTemplates(objs []client.Object) []models.ModuleBayTemplate {
	sortByName(objs)
	var out []models.ModuleBayTemplate
	for _, o := range objs {
		out = append(out, models.ModuleBayTemplate{
			Name:        str(o, "name"),
			Label:       str(o, "label"),
			Description: str(o, "description"),
			Position:    str(o, "position"),
		})
	}
	return out
}

func deviceBayTemplates(objs []client.Object) []models.DeviceBayTemplate {
	sortByName(objs)
	var out []models.DeviceBayTemplate
	for _, o := range objs {
		out = append(out, models.DeviceBayTemplate{
			Name:        str(o, "name"),
			Label:       str(o, "label"),
			Description: str(o, "description"),
		})
	}
	return out
}

// enabledPtr returns a pointer to false when the template disables the port,
// and nil when it is enabled. NetBox defaults an interface template to enabled,
// and the sync sends `enabled` only when the YAML sets it, so leaving nil for
// the common (enabled) case both round-trips and keeps the output terse.
func enabledPtr(o client.Object) *bool {
	if v, ok := o["enabled"].(bool); ok && !v {
		f := false
		return &f
	}
	return nil
}

// sortByName sorts template objects by their name field.
func sortByName(objs []client.Object) {
	sort.SliceStable(objs, func(i, j int) bool { return str(objs[i], "name") < str(objs[j], "name") })
}

// moduleTypeSlug is the slug this schema references a module type by: NetBox's
// own slug when present (it has none today), else the model slugified, matching
// what the loader derives for a module type declared without a slug.
func moduleTypeSlug(o client.Object) string {
	if s := str(o, "slug"); s != "" {
		return s
	}
	return utils.Slugify(str(o, "model"))
}

// fileSeg makes a value safe as a path segment, slugifying and defaulting an
// empty value so a type with no manufacturer still lands somewhere legible.
func fileSeg(value string) string {
	slug := utils.Slugify(value)
	if slug == "" {
		return "unknown"
	}
	return slug
}
