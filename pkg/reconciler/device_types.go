package reconciler

import (
	"fmt"

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

// ReconcileModuleTypes reconciles module type definitions
func (dtr *DeviceTypeReconciler) ReconcileModuleTypes(moduleTypes []*models.ModuleType) error {
	dtr.logger.Info("Reconciling %d module types...", len(moduleTypes))

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

		// Match on the real NetBox identity for a module type (manufacturer +
		// model); there is no slug to filter on.
		lookup := map[string]interface{}{
			"manufacturer_id": mfgID,
			"model":           mt.Model,
		}
		mtObj, err := dtr.client.Apply("dcim", "module-types", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile module type %s: %w", mt.Model, err)
		}
		// Register so the device phase can resolve a module type created this run.
		dtr.client.Cache().Register("module_types", utils.GetIDFromObject(mtObj), mt.Slug, mt.Model)
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
		// NetBox rejects a weight without its unit, so only send the pair.
		if dt.Weight != 0 && dt.WeightUnit != "" {
			payload["weight"] = dt.Weight
			payload["weight_unit"] = dt.WeightUnit
		}

		lookup := map[string]interface{}{"slug": dt.Slug}
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

		// CRITICAL: Order matters!
		// 1. REAR PORTS FIRST - they must exist before front ports
		if err := dtr.reconcileRearPortTemplates(dtID, dt.RearPorts); err != nil {
			return fmt.Errorf("failed to reconcile rear port templates for %s: %w", dt.Model, err)
		}

		// 2. FRONT PORTS SECOND - they reference rear ports by ID
		if err := dtr.reconcileFrontPortTemplates(dtID, dt.FrontPorts); err != nil {
			return fmt.Errorf("failed to reconcile front port templates for %s: %w", dt.Model, err)
		}

		// 3. INTERFACES LAST
		if err := dtr.reconcileInterfaceTemplates(dtID, dt.Interfaces); err != nil {
			return fmt.Errorf("failed to reconcile interface templates for %s: %w", dt.Model, err)
		}

		if err := dtr.reconcileConsolePortTemplates(dtID, "console-port-templates", dt.ConsolePorts); err != nil {
			return fmt.Errorf("failed to reconcile console port templates for %s: %w", dt.Model, err)
		}

		if err := dtr.reconcileConsolePortTemplates(dtID, "console-server-port-templates", dt.ConsoleServerPorts); err != nil {
			return fmt.Errorf("failed to reconcile console server port templates for %s: %w", dt.Model, err)
		}

		// Power ports before outlets: an outlet names the port that feeds it.
		if err := dtr.reconcilePowerPortTemplates(dtID, dt.PowerPorts); err != nil {
			return fmt.Errorf("failed to reconcile power port templates for %s: %w", dt.Model, err)
		}

		if err := dtr.reconcilePowerOutletTemplates(dtID, dt.PowerOutlets); err != nil {
			return fmt.Errorf("failed to reconcile power outlet templates for %s: %w", dt.Model, err)
		}

		if err := dtr.reconcileModuleBayTemplates(dtID, dt.ModuleBays); err != nil {
			return fmt.Errorf("failed to reconcile module bay templates for %s: %w", dt.Model, err)
		}

		if err := dtr.reconcileDeviceBayTemplates(dtID, dt.DeviceBays); err != nil {
			return fmt.Errorf("failed to reconcile device bay templates for %s: %w", dt.Model, err)
		}
	}

	return nil
}

// reconcileInterfaceTemplates reconciles interface templates for a device type
func (dtr *DeviceTypeReconciler) reconcileInterfaceTemplates(deviceTypeID int, templates []models.InterfaceTemplate) error {
	for _, tmpl := range templates {
		payload := map[string]interface{}{
			"device_type": deviceTypeID,
			"name":        tmpl.Name,
			"type":        tmpl.Type,
			"mgmt_only":   tmpl.MgmtOnly,
		}

		lookup := map[string]interface{}{
			"device_type_id": deviceTypeID,
			"name":           tmpl.Name,
		}

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
func (dtr *DeviceTypeReconciler) reconcileFrontPortTemplates(deviceTypeID int, templates []models.PortTemplate) error {
	// First, we need rear ports to exist
	for _, tmpl := range templates {
		payload := map[string]interface{}{
			"device_type": deviceTypeID,
			"name":        tmpl.Name,
			"type":        tmpl.Type,
		}

		if tmpl.RearPort != "" {
			// Find rear port
			rearPorts, err := dtr.client.Filter("dcim", "rear-port-templates", map[string]interface{}{
				"device_type_id": deviceTypeID,
				"name":           tmpl.RearPort,
			})
			if err == nil && len(rearPorts) > 0 {
				payload["rear_port"] = utils.GetIDFromObject(rearPorts[0])
				// A multi-position rear port (e.g. an MPO trunk broken out to
				// several front ports) needs the specific position; single
				// position panels leave it unset and get 1.
				payload["rear_port_position"] = defaultPosition(tmpl.RearPortPosition)
			}
		}

		lookup := map[string]interface{}{
			"device_type_id": deviceTypeID,
			"name":           tmpl.Name,
		}

		delete(payload, "tags")

		_, err := dtr.client.Apply("dcim", "front-port-templates", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile front port template %s: %w", tmpl.Name, err)
		}
	}

	return nil
}

// reconcileRearPortTemplates reconciles rear port templates
func (dtr *DeviceTypeReconciler) reconcileRearPortTemplates(deviceTypeID int, templates []models.PortTemplate) error {
	for _, tmpl := range templates {
		payload := map[string]interface{}{
			"device_type": deviceTypeID,
			"name":        tmpl.Name,
			"type":        tmpl.Type,
			"positions":   defaultPosition(tmpl.Positions),
		}

		lookup := map[string]interface{}{
			"device_type_id": deviceTypeID,
			"name":           tmpl.Name,
		}

		delete(payload, "tags")

		_, err := dtr.client.Apply("dcim", "rear-port-templates", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile rear port template %s: %w", tmpl.Name, err)
		}
	}

	return nil
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
func (dtr *DeviceTypeReconciler) reconcileConsolePortTemplates(deviceTypeID int, endpoint string, templates []models.ConsolePortTemplate) error {
	for _, tmpl := range templates {
		payload := map[string]interface{}{
			"device_type": deviceTypeID,
			"name":        tmpl.Name,
		}
		if tmpl.Type != "" {
			payload["type"] = tmpl.Type
		}

		lookup := map[string]interface{}{
			"device_type_id": deviceTypeID,
			"name":           tmpl.Name,
		}

		if _, err := dtr.client.Apply("dcim", endpoint, lookup, payload); err != nil {
			return fmt.Errorf("failed to reconcile %s %s: %w", endpoint, tmpl.Name, err)
		}
	}

	return nil
}

// reconcilePowerPortTemplates reconciles power port (inlet) templates.
func (dtr *DeviceTypeReconciler) reconcilePowerPortTemplates(deviceTypeID int, templates []models.PowerPortTemplate) error {
	for _, tmpl := range templates {
		payload := map[string]interface{}{
			"device_type": deviceTypeID,
			"name":        tmpl.Name,
		}
		if tmpl.Type != "" {
			payload["type"] = tmpl.Type
		}
		if tmpl.MaximumDraw > 0 {
			payload["maximum_draw"] = tmpl.MaximumDraw
		}
		if tmpl.AllocatedDraw > 0 {
			payload["allocated_draw"] = tmpl.AllocatedDraw
		}

		lookup := map[string]interface{}{
			"device_type_id": deviceTypeID,
			"name":           tmpl.Name,
		}

		if _, err := dtr.client.Apply("dcim", "power-port-templates", lookup, payload); err != nil {
			return fmt.Errorf("failed to reconcile power port template %s: %w", tmpl.Name, err)
		}
	}

	return nil
}

// reconcilePowerOutletTemplates reconciles power outlet templates. An outlet
// may name the power port template that feeds it; that port is resolved by
// name on the same device type, so power ports must be reconciled first.
func (dtr *DeviceTypeReconciler) reconcilePowerOutletTemplates(deviceTypeID int, templates []models.PowerOutletTemplate) error {
	for _, tmpl := range templates {
		payload := map[string]interface{}{
			"device_type": deviceTypeID,
			"name":        tmpl.Name,
		}
		if tmpl.Type != "" {
			payload["type"] = tmpl.Type
		}
		if tmpl.FeedLeg != "" {
			payload["feed_leg"] = tmpl.FeedLeg
		}

		if tmpl.PowerPort != "" {
			powerPorts, err := dtr.client.Filter("dcim", "power-port-templates", map[string]interface{}{
				"device_type_id": deviceTypeID,
				"name":           tmpl.PowerPort,
			})
			if err != nil {
				return fmt.Errorf("failed to look up power port %s for outlet %s: %w", tmpl.PowerPort, tmpl.Name, err)
			}
			if len(powerPorts) > 0 {
				payload["power_port"] = utils.GetIDFromObject(powerPorts[0])
			} else {
				dtr.logger.Warning("Power outlet %s references unknown power port %s; leaving it unfed", tmpl.Name, tmpl.PowerPort)
			}
		}

		lookup := map[string]interface{}{
			"device_type_id": deviceTypeID,
			"name":           tmpl.Name,
		}

		if _, err := dtr.client.Apply("dcim", "power-outlet-templates", lookup, payload); err != nil {
			return fmt.Errorf("failed to reconcile power outlet template %s: %w", tmpl.Name, err)
		}
	}

	return nil
}

// reconcileModuleBayTemplates reconciles module bay templates
func (dtr *DeviceTypeReconciler) reconcileModuleBayTemplates(deviceTypeID int, templates []models.ModuleBayTemplate) error {
	for _, tmpl := range templates {
		payload := map[string]interface{}{
			"device_type": deviceTypeID,
			"name":        tmpl.Name,
		}

		if tmpl.Label != "" {
			payload["label"] = tmpl.Label
		}
		if tmpl.Description != "" {
			payload["description"] = tmpl.Description
		}
		if tmpl.Position != "" {
			payload["position"] = tmpl.Position
		}

		lookup := map[string]interface{}{
			"device_type_id": deviceTypeID,
			"name":           tmpl.Name,
		}

		delete(payload, "tags")

		_, err := dtr.client.Apply("dcim", "module-bay-templates", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile module bay template %s: %w", tmpl.Name, err)
		}
	}

	return nil
}

// reconcileDeviceBayTemplates reconciles device bay templates
func (dtr *DeviceTypeReconciler) reconcileDeviceBayTemplates(deviceTypeID int, templates []models.DeviceBayTemplate) error {
	for _, tmpl := range templates {
		payload := map[string]interface{}{
			"device_type": deviceTypeID,
			"name":        tmpl.Name,
		}

		if tmpl.Label != "" {
			payload["label"] = tmpl.Label
		}
		if tmpl.Description != "" {
			payload["description"] = tmpl.Description
		}

		lookup := map[string]interface{}{
			"device_type_id": deviceTypeID,
			"name":           tmpl.Name,
		}

		delete(payload, "tags")

		_, err := dtr.client.Apply("dcim", "device-bay-templates", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile device bay template %s: %w", tmpl.Name, err)
		}
	}

	return nil
}
