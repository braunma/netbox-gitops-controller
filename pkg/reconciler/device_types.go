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
		mfgID, ok := dtr.client.Cache().GetID("manufacturers", mt.Manufacturer)
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
		}

		payload := map[string]interface{}{
			"model":        mt.Model,
			"slug":         mt.Slug,
			"manufacturer": mfgID,
		}

		if mt.Description != "" {
			payload["description"] = mt.Description
		}

		lookup := map[string]interface{}{"slug": mt.Slug}
		_, err := dtr.client.Apply("dcim", "module-types", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile module type %s: %w", mt.Model, err)
		}
	}

	return nil
}

// ReconcileDeviceTypes reconciles device type definitions
func (dtr *DeviceTypeReconciler) ReconcileDeviceTypes(deviceTypes []*models.DeviceType) error {
	dtr.logger.Info("Reconciling %d device types...", len(deviceTypes))

	for _, dt := range deviceTypes {
		// Get manufacturer ID
		mfgID, ok := dtr.client.Cache().GetID("manufacturers", dt.Manufacturer)
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

		lookup := map[string]interface{}{"slug": dt.Slug}
		dtObj, err := dtr.client.Apply("dcim", "device-types", lookup, payload)
		if err != nil {
			return fmt.Errorf("failed to reconcile device type %s: %w", dt.Model, err)
		}

		dtID := utils.GetIDFromObject(dtObj)
		if dtID == 0 {
			continue
		}

		// CRITICAL: Order matters! (matches Python device_types.py lines 52-112)
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
				payload["rear_port_position"] = 1
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
			"positions":   1,
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
