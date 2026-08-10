// SPDX-License-Identifier: Apache-2.0

package reconciler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// DeviceTypeReconciler handles device type and module type reconciliation
type DeviceTypeReconciler struct {
	client *client.NetBoxClient
	logger *utils.Logger
}

// NewDeviceTypeReconciler creates a new device type reconciler
func NewDeviceTypeReconciler(c *client.NetBoxClient) *DeviceTypeReconciler {
	return &DeviceTypeReconciler{
		client: c,
		logger: c.Logger(),
	}
}

// templateOwner identifies what a component template belongs to. NetBox hangs
// the same template endpoints off either a device type or a module type,
// differing only in which owner field carries the ID.
type templateOwner struct {
	field  string // payload key:  "device_type" or "module_type"
	filter string // lookup key:   "device_type_id" or "module_type_id"
	id     int
}

func deviceTypeOwner(id int) templateOwner {
	return templateOwner{field: "device_type", filter: "device_type_id", id: id}
}

func moduleTypeOwner(id int) templateOwner {
	return templateOwner{field: "module_type", filter: "module_type_id", id: id}
}

// payload seeds a component template payload with its owner.
func (o templateOwner) payload(name string) map[string]interface{} {
	return map[string]interface{}{o.field: o.id, "name": name}
}

// lookup builds the filter that finds an existing template by name.
func (o templateOwner) lookup(name string) map[string]interface{} {
	return map[string]interface{}{o.filter: o.id, "name": name}
}

// reconcileComponentTemplates applies every component template a device type
// or module type declares, in the order NetBox's own references require: rear
// ports before the front ports that terminate on them, and power ports before
// the outlets they feed.
func (dtr *DeviceTypeReconciler) reconcileComponentTemplates(owner templateOwner, c componentTemplates, label string) error {
	if err := dtr.reconcileRearPortTemplates(owner, c.RearPorts); err != nil {
		return fmt.Errorf("failed to reconcile rear port templates for %s: %w", label, err)
	}
	if err := dtr.reconcileFrontPortTemplates(owner, c.FrontPorts); err != nil {
		return fmt.Errorf("failed to reconcile front port templates for %s: %w", label, err)
	}
	if err := dtr.reconcileInterfaceTemplates(owner, c.Interfaces); err != nil {
		return fmt.Errorf("failed to reconcile interface templates for %s: %w", label, err)
	}
	if err := dtr.reconcileConsolePortTemplates(owner, "console-port-templates", c.ConsolePorts); err != nil {
		return fmt.Errorf("failed to reconcile console port templates for %s: %w", label, err)
	}
	if err := dtr.reconcileConsolePortTemplates(owner, "console-server-port-templates", c.ConsoleServerPorts); err != nil {
		return fmt.Errorf("failed to reconcile console server port templates for %s: %w", label, err)
	}
	if err := dtr.reconcilePowerPortTemplates(owner, c.PowerPorts); err != nil {
		return fmt.Errorf("failed to reconcile power port templates for %s: %w", label, err)
	}
	if err := dtr.reconcilePowerOutletTemplates(owner, c.PowerOutlets); err != nil {
		return fmt.Errorf("failed to reconcile power outlet templates for %s: %w", label, err)
	}
	if err := dtr.reconcileModuleBayTemplates(owner, c.ModuleBays); err != nil {
		return fmt.Errorf("failed to reconcile module bay templates for %s: %w", label, err)
	}
	return nil
}

// componentTemplates is the set of templates shared by device types and module
// types. Device bays are absent: NetBox only allows them on a device type.
type componentTemplates struct {
	Interfaces         []models.InterfaceTemplate
	FrontPorts         []models.PortTemplate
	RearPorts          []models.PortTemplate
	ConsolePorts       []models.ConsolePortTemplate
	ConsoleServerPorts []models.ConsolePortTemplate
	PowerPorts         []models.PowerPortTemplate
	PowerOutlets       []models.PowerOutletTemplate
	ModuleBays         []models.ModuleBayTemplate
}

// ambiguousModuleTypeSlugs returns the slugs that more than one manufacturer
// claims. NetBox identifies a module type by manufacturer and model together
// and gives it no slug of its own, so two vendors may legitimately ship the
// same model name — but the reference cache is a flat map, and registering
// both under the bare slug would let `module_type_slug` silently resolve to
// whichever happened to be reconciled last. Those slugs are registered only in
// their manufacturer-qualified form instead, so an ambiguous reference fails
// loudly rather than installing the wrong module.
func ambiguousModuleTypeSlugs(moduleTypes []*models.ModuleType) map[string][]string {
	owners := make(map[string]map[string]bool, len(moduleTypes))
	for _, mt := range moduleTypes {
		if owners[mt.Slug] == nil {
			owners[mt.Slug] = make(map[string]bool, 1)
		}
		owners[mt.Slug][mt.Manufacturer] = true
	}

	ambiguous := make(map[string][]string)
	for slug, manufacturers := range owners {
		if len(manufacturers) < 2 {
			continue
		}
		qualified := make([]string, 0, len(manufacturers))
		for mfg := range manufacturers {
			qualified = append(qualified, qualifiedModuleTypeSlug(mfg, slug))
		}
		sort.Strings(qualified)
		ambiguous[slug] = qualified
	}
	return ambiguous
}

// qualifiedModuleTypeSlug is the manufacturer-scoped cache key for a module
// type, and the form `module_type_slug` accepts to disambiguate.
func qualifiedModuleTypeSlug(manufacturer, slug string) string {
	return utils.Slugify(manufacturer) + "/" + slug
}

// ReconcileModuleTypes reconciles module type definitions
func (dtr *DeviceTypeReconciler) ReconcileModuleTypes(moduleTypes []*models.ModuleType) error {
	dtr.logger.Info("Reconciling %d module types...", len(moduleTypes))

	ambiguous := ambiguousModuleTypeSlugs(moduleTypes)
	for slug, qualified := range ambiguous {
		dtr.logger.Warning(
			"Module type slug %q is claimed by %d manufacturers; reference these by their qualified slug (%s), a bare %q will not resolve",
			slug, len(qualified), strings.Join(qualified, ", "), slug)
	}
	// A previous run left these module types in NetBox, so the cache loaded at
	// startup already holds the colliding bare keys — indexed by the raw model
	// string, not the slug. Drop both, or the warning above would be advice the
	// tool itself does not follow.
	for _, mt := range moduleTypes {
		if _, clash := ambiguous[mt.Slug]; clash {
			dtr.client.Cache().Unregister("module_types", mt.Slug, mt.Model)
		}
	}

	for _, mt := range moduleTypes {
		// Get manufacturer ID
		mfgID, ok := dtr.client.Cache().GetGlobalID("manufacturers", mt.Manufacturer)
		if !ok {
			// Create manufacturer if it doesn't exist
			mfgPayload := map[string]interface{}{
				"name": mt.Manufacturer,
				"slug": utils.Slugify(mt.Manufacturer),
			}
			mfgObj, err := dtr.client.Apply("dcim", "manufacturers", map[string]interface{}{"slug": utils.Slugify(mt.Manufacturer)}, mfgPayload)
			if err != nil {
				return fmt.Errorf("failed to create manufacturer %s: %w", mt.Manufacturer, err)
			}
			mfgID = utils.GetIDFromObject(mfgObj)
			// Register so a manufacturer created here is reused (not re-created)
			// by later types in this same run, including in --dry-run.
			dtr.client.Cache().Register("manufacturers", mfgID, utils.Slugify(mt.Manufacturer), mt.Manufacturer)
		}

		// NetBox module types are identified by manufacturer + model and have no
		// `slug` field of their own; sending one is silently dropped by the API
		// and would otherwise show up as a phantom diff on every run. The slug is
		// still used locally as the lookup/cache key.
		payload := map[string]interface{}{
			"model":        mt.Model,
			"manufacturer": mfgID,
		}

		if mt.Description != "" {
			payload["description"] = mt.Description
		}
		if mt.PartNumber != "" {
			payload["part_number"] = mt.PartNumber
		}
		if mt.Airflow != "" {
			payload["airflow"] = mt.Airflow
		}
		if mt.Comments != "" {
			payload["comments"] = mt.Comments
		}
		// NetBox rejects a weight without its unit, so only send the pair.
		switch {
		case mt.Weight != 0 && mt.WeightUnit != "":
			payload["weight"] = mt.Weight
			payload["weight_unit"] = mt.WeightUnit
		case mt.Weight != 0:
			dtr.logger.Warning("Module type %s declares weight %v without weight_unit; not setting a weight", mt.Model, mt.Weight)
		case mt.WeightUnit != "":
			dtr.logger.Warning("Module type %s declares weight_unit %q without a weight; not setting a weight", mt.Model, mt.WeightUnit)
		}

		// Match on the real NetBox identity for a module type (manufacturer +
		// model); there is no slug to filter on.
		lookup := map[string]interface{}{"model": mt.Model}
		if mfgID != 0 {
			lookup["manufacturer_id"] = mfgID
		}
		// mfgID is 0 only in --dry-run, when the manufacturer itself is still
		// just planned. NetBox rejects manufacturer_id=0 as an invalid choice,
		// which used to abort the very first dry-run against an empty NetBox;
		// matching on model alone is enough to plan against.
		lookup, err := dtr.client.RenamedLookup("dcim", "module-types", mt.Model, lookup, "model", nonEmpty(mt.RenameFrom))
		if err != nil {
			return err
		}
		mtObj, err := dtr.client.Apply("dcim", "module-types", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile module type %s: %w", mt.Model, err)
		}
		mtID := utils.GetIDFromObject(mtObj)
		// Register so the device phase can resolve a module type created this
		// run. The qualified key is always available; the bare slug and model
		// are withheld when another manufacturer claims the same name, so that
		// an ambiguous reference reports "not found" instead of resolving to an
		// arbitrary vendor's module.
		identifiers := []string{qualifiedModuleTypeSlug(mt.Manufacturer, mt.Slug)}
		if _, clash := ambiguous[mt.Slug]; !clash {
			identifiers = append(identifiers, mt.Slug, mt.Model)
		}
		dtr.client.Cache().Register("module_types", mtID, identifiers...)
		if mtID == 0 {
			// --dry-run: nothing was written, so there is no owner to hang the
			// component templates off yet.
			continue
		}

		// A module type carries the same components as a device type. Their
		// names may contain the literal "{module}" placeholder, which NetBox
		// substitutes with the bay position when the module is installed, so
		// they are sent through unchanged.
		if err := dtr.reconcileComponentTemplates(moduleTypeOwner(mtID), componentTemplates{
			Interfaces:         mt.Interfaces,
			FrontPorts:         mt.FrontPorts,
			RearPorts:          mt.RearPorts,
			ConsolePorts:       mt.ConsolePorts,
			ConsoleServerPorts: mt.ConsoleServerPorts,
			PowerPorts:         mt.PowerPorts,
			PowerOutlets:       mt.PowerOutlets,
			ModuleBays:         mt.ModuleBays,
		}, mt.Model); err != nil {
			return err
		}
	}

	return nil
}

// ReconcileDeviceTypes reconciles device type definitions
func (dtr *DeviceTypeReconciler) ReconcileDeviceTypes(deviceTypes []*models.DeviceType) error {
	dtr.logger.Info("Reconciling %d device types...", len(deviceTypes))

	for _, dt := range deviceTypes {
		// Get manufacturer ID
		mfgID, ok := dtr.client.Cache().GetGlobalID("manufacturers", dt.Manufacturer)
		if !ok {
			// Create manufacturer if it doesn't exist
			mfgPayload := map[string]interface{}{
				"name": dt.Manufacturer,
				"slug": utils.Slugify(dt.Manufacturer),
			}
			mfgObj, err := dtr.client.Apply("dcim", "manufacturers", map[string]interface{}{"slug": utils.Slugify(dt.Manufacturer)}, mfgPayload)
			if err != nil {
				return fmt.Errorf("failed to create manufacturer %s: %w", dt.Manufacturer, err)
			}
			mfgID = utils.GetIDFromObject(mfgObj)
			dtr.client.Cache().Register("manufacturers", mfgID, utils.Slugify(dt.Manufacturer), dt.Manufacturer)
		}

		payload := map[string]interface{}{
			"model":         dt.Model,
			"slug":          dt.Slug,
			"manufacturer":  mfgID,
			"u_height":      dt.UHeight,
			"is_full_depth": dt.IsFullDepth,
		}

		if dt.SubdeviceRole != "" {
			payload["subdevice_role"] = dt.SubdeviceRole
		}
		if dt.PartNumber != "" {
			payload["part_number"] = dt.PartNumber
		}
		if dt.Airflow != "" {
			payload["airflow"] = dt.Airflow
		}
		if dt.Description != "" {
			payload["description"] = dt.Description
		}
		if dt.Comments != "" {
			payload["comments"] = dt.Comments
		}
		// NetBox rejects a weight without its unit, so only send the pair —
		// but say so rather than dropping a declared value in silence.
		switch {
		case dt.Weight != 0 && dt.WeightUnit != "":
			payload["weight"] = dt.Weight
			payload["weight_unit"] = dt.WeightUnit
		case dt.Weight != 0:
			dtr.logger.Warning("Device type %s declares weight %v without weight_unit; not setting a weight", dt.Slug, dt.Weight)
		case dt.WeightUnit != "":
			dtr.logger.Warning("Device type %s declares weight_unit %q without a weight; not setting a weight", dt.Slug, dt.WeightUnit)
		}

		lookup := map[string]interface{}{"slug": dt.Slug}
		lookup, err := dtr.client.RenamedLookup("dcim", "device-types", dt.Model, lookup, "slug", client.SlugifiedRename(dt.RenameFrom))
		if err != nil {
			return err
		}
		dtObj, err := dtr.client.Apply("dcim", "device-types", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile device type %s: %w", dt.Model, err)
		}

		dtID := utils.GetIDFromObject(dtObj)
		// Register before the dry-run short-circuit below so the device phase can
		// resolve a device type added in this same run (id is 0 in --dry-run; the
		// key's presence is what lets a dependent device validate).
		dtr.client.Cache().Register("device_types", dtID, dt.Slug, dt.Model)
		if dtID == 0 {
			continue
		}

		owner := deviceTypeOwner(dtID)
		if err := dtr.reconcileComponentTemplates(owner, componentTemplates{
			Interfaces:         dt.Interfaces,
			FrontPorts:         dt.FrontPorts,
			RearPorts:          dt.RearPorts,
			ConsolePorts:       dt.ConsolePorts,
			ConsoleServerPorts: dt.ConsoleServerPorts,
			PowerPorts:         dt.PowerPorts,
			PowerOutlets:       dt.PowerOutlets,
			ModuleBays:         dt.ModuleBays,
		}, dt.Model); err != nil {
			return err
		}

		if err := dtr.reconcileDeviceBayTemplates(owner, dt.DeviceBays); err != nil {
			return fmt.Errorf("failed to reconcile device bay templates for %s: %w", dt.Model, err)
		}
	}

	return nil
}

// reconcileInterfaceTemplates reconciles interface templates for a device type
func (dtr *DeviceTypeReconciler) reconcileInterfaceTemplates(owner templateOwner, templates []models.InterfaceTemplate) error {
	for _, tmpl := range templates {
		payload := owner.payload(tmpl.Name)
		payload["type"] = tmpl.Type
		payload["mgmt_only"] = tmpl.MgmtOnly
		setIfPresent(payload, map[string]string{
			"label": tmpl.Label, "description": tmpl.Description,
			"poe_mode": tmpl.PoEMode, "poe_type": tmpl.PoEType, "rf_role": tmpl.RFRole,
		})
		// NetBox defaults enabled to true, so only send an explicit choice.
		if tmpl.Enabled != nil {
			payload["enabled"] = *tmpl.Enabled
		}

		lookup := owner.lookup(tmpl.Name)

		// Remove tags from templates (they don't support tags)
		delete(payload, "tags")

		_, err := dtr.client.Apply("dcim", "interface-templates", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile interface template %s: %w", tmpl.Name, err)
		}
	}

	return nil
}

// reconcileFrontPortTemplates reconciles front port templates
func (dtr *DeviceTypeReconciler) reconcileFrontPortTemplates(owner templateOwner, templates []models.PortTemplate) error {
	// First, we need rear ports to exist
	for _, tmpl := range templates {
		payload := owner.payload(tmpl.Name)
		payload["type"] = tmpl.Type
		setIfPresent(payload, map[string]string{
			"label": tmpl.Label, "description": tmpl.Description, "color": tmpl.Color,
		})

		if tmpl.RearPort != "" {
			// Find rear port
			rearPorts, err := dtr.client.Filter("dcim", "rear-port-templates", owner.lookup(tmpl.RearPort))
			if err == nil && len(rearPorts) > 0 {
				// A multi-position rear port (e.g. an MPO trunk broken out to
				// several front ports) needs the specific position; single
				// position panels leave it unset and get 1.
				setFrontPortTermination(dtr.client, payload,
					utils.GetIDFromObject(rearPorts[0]), defaultPosition(tmpl.RearPortPosition))
			}
		}

		lookup := owner.lookup(tmpl.Name)

		delete(payload, "tags")

		_, err := dtr.client.Apply("dcim", "front-port-templates", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile front port template %s: %w", tmpl.Name, err)
		}
	}

	return nil
}

// reconcileRearPortTemplates reconciles rear port templates
func (dtr *DeviceTypeReconciler) reconcileRearPortTemplates(owner templateOwner, templates []models.PortTemplate) error {
	for _, tmpl := range templates {
		payload := owner.payload(tmpl.Name)
		payload["type"] = tmpl.Type
		payload["positions"] = defaultPosition(tmpl.Positions)
		setIfPresent(payload, map[string]string{
			"label": tmpl.Label, "description": tmpl.Description, "color": tmpl.Color,
		})

		lookup := owner.lookup(tmpl.Name)

		delete(payload, "tags")

		_, err := dtr.client.Apply("dcim", "rear-port-templates", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile rear port template %s: %w", tmpl.Name, err)
		}
	}

	return nil
}

// setFrontPortTermination writes a front port's rear-port termination in the
// shape the connected NetBox expects. NetBox 4.6 replaced the singular
// rear_port/rear_port_position fields with a rear_ports list of mappings, and
// accepts the old fields silently rather than rejecting them — so sending the
// wrong shape leaves the port created but unwired, and re-PATCHed forever.
func setFrontPortTermination(c *client.NetBoxClient, payload map[string]interface{}, rearPortID, rearPortPosition int) {
	if c.UsesPortMappings() {
		payload["positions"] = 1
		payload["rear_ports"] = []interface{}{
			map[string]interface{}{
				"position":           1,
				"rear_port":          rearPortID,
				"rear_port_position": rearPortPosition,
			},
		}
		return
	}
	payload["rear_port"] = rearPortID
	payload["rear_port_position"] = rearPortPosition
}

// setIfPresent copies the non-empty optional fields shared by every component
// template onto a payload.
func setIfPresent(payload map[string]interface{}, fields map[string]string) {
	for key, value := range fields {
		if value != "" {
			payload[key] = value
		}
	}
}

// defaultPosition maps an unset (zero) position to NetBox's default of 1.
func defaultPosition(p int) int {
	if p <= 0 {
		return 1
	}
	return p
}

// reconcileConsolePortTemplates reconciles console port or console server port
// templates, which share an identical payload shape and differ only by
// endpoint.
func (dtr *DeviceTypeReconciler) reconcileConsolePortTemplates(owner templateOwner, endpoint string, templates []models.ConsolePortTemplate) error {
	for _, tmpl := range templates {
		payload := owner.payload(tmpl.Name)
		if tmpl.Type != "" {
			payload["type"] = tmpl.Type
		}
		setIfPresent(payload, map[string]string{"label": tmpl.Label, "description": tmpl.Description})

		lookup := owner.lookup(tmpl.Name)

		if _, err := dtr.client.Apply("dcim", endpoint, lookup, payload); err != nil {
			return fmt.Errorf("failed to reconcile %s %s: %w", endpoint, tmpl.Name, err)
		}
	}

	return nil
}

// reconcilePowerPortTemplates reconciles power port (inlet) templates.
func (dtr *DeviceTypeReconciler) reconcilePowerPortTemplates(owner templateOwner, templates []models.PowerPortTemplate) error {
	for _, tmpl := range templates {
		payload := owner.payload(tmpl.Name)
		if tmpl.Type != "" {
			payload["type"] = tmpl.Type
		}
		setIfPresent(payload, map[string]string{"label": tmpl.Label, "description": tmpl.Description})
		if tmpl.MaximumDraw > 0 {
			payload["maximum_draw"] = tmpl.MaximumDraw
		}
		if tmpl.AllocatedDraw > 0 {
			payload["allocated_draw"] = tmpl.AllocatedDraw
		}

		lookup := owner.lookup(tmpl.Name)

		if _, err := dtr.client.Apply("dcim", "power-port-templates", lookup, payload); err != nil {
			return fmt.Errorf("failed to reconcile power port template %s: %w", tmpl.Name, err)
		}
	}

	return nil
}

// reconcilePowerOutletTemplates reconciles power outlet templates. An outlet
// may name the power port template that feeds it; that port is resolved by
// name on the same device type, so power ports must be reconciled first.
func (dtr *DeviceTypeReconciler) reconcilePowerOutletTemplates(owner templateOwner, templates []models.PowerOutletTemplate) error {
	for _, tmpl := range templates {
		payload := owner.payload(tmpl.Name)
		if tmpl.Type != "" {
			payload["type"] = tmpl.Type
		}
		setIfPresent(payload, map[string]string{
			"label": tmpl.Label, "description": tmpl.Description, "feed_leg": tmpl.FeedLeg,
		})

		if tmpl.PowerPort != "" {
			powerPorts, err := dtr.client.Filter("dcim", "power-port-templates", owner.lookup(tmpl.PowerPort))
			if err != nil {
				return fmt.Errorf("failed to look up power port %s for outlet %s: %w", tmpl.PowerPort, tmpl.Name, err)
			}
			if len(powerPorts) > 0 {
				payload["power_port"] = utils.GetIDFromObject(powerPorts[0])
			} else {
				dtr.logger.Warning("Power outlet %s references unknown power port %s; leaving it unfed", tmpl.Name, tmpl.PowerPort)
			}
		}

		lookup := owner.lookup(tmpl.Name)

		if _, err := dtr.client.Apply("dcim", "power-outlet-templates", lookup, payload); err != nil {
			return fmt.Errorf("failed to reconcile power outlet template %s: %w", tmpl.Name, err)
		}
	}

	return nil
}

// reconcileModuleBayTemplates reconciles module bay templates
func (dtr *DeviceTypeReconciler) reconcileModuleBayTemplates(owner templateOwner, templates []models.ModuleBayTemplate) error {
	for _, tmpl := range templates {
		payload := owner.payload(tmpl.Name)

		if tmpl.Label != "" {
			payload["label"] = tmpl.Label
		}
		if tmpl.Description != "" {
			payload["description"] = tmpl.Description
		}
		if tmpl.Position != "" {
			payload["position"] = tmpl.Position
		}

		lookup := owner.lookup(tmpl.Name)

		delete(payload, "tags")

		_, err := dtr.client.Apply("dcim", "module-bay-templates", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile module bay template %s: %w", tmpl.Name, err)
		}
	}

	return nil
}

// reconcileDeviceBayTemplates reconciles device bay templates
// Device bays exist only on device types; NetBox rejects module_type here.
func (dtr *DeviceTypeReconciler) reconcileDeviceBayTemplates(owner templateOwner, templates []models.DeviceBayTemplate) error {
	for _, tmpl := range templates {
		payload := owner.payload(tmpl.Name)

		if tmpl.Label != "" {
			payload["label"] = tmpl.Label
		}
		if tmpl.Description != "" {
			payload["description"] = tmpl.Description
		}

		lookup := owner.lookup(tmpl.Name)

		delete(payload, "tags")

		_, err := dtr.client.Apply("dcim", "device-bay-templates", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile device bay template %s: %w", tmpl.Name, err)
		}
	}

	return nil
}
