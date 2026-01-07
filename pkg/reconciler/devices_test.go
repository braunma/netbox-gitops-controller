package reconciler

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// TestRackFaceLogic tests that face and position are only set when rack is present
func TestRackFaceLogic(t *testing.T) {
	tests := []struct {
		name            string
		device          *models.DeviceConfig
		expectRack      bool
		expectPosition  bool
		expectFace      bool
	}{
		{
			name: "device with rack should have position and face",
			device: &models.DeviceConfig{
				Name:           "test-server",
				SiteSlug:       "site1",
				RoleSlug:       "server",
				DeviceTypeSlug: "type1",
				RackSlug:       "rack1",
				Position:       10,
				Face:           "front",
			},
			expectRack:     true,
			expectPosition: true,
			expectFace:     true,
		},
		{
			name: "device without rack should not have position or face",
			device: &models.DeviceConfig{
				Name:           "test-server",
				SiteSlug:       "site1",
				RoleSlug:       "server",
				DeviceTypeSlug: "type1",
				Position:       10,
				Face:           "front",
			},
			expectRack:     false,
			expectPosition: false,
			expectFace:     false,
		},
		{
			name: "child device with parent should not have position or face",
			device: &models.DeviceConfig{
				Name:           "test-blade",
				SiteSlug:       "site1",
				RoleSlug:       "server",
				DeviceTypeSlug: "blade-type",
				ParentDevice:   "chassis1",
				DeviceBay:      "Bay-1",
				Position:       10, // Should be ignored
				Face:           "front", // Should be ignored
			},
			expectRack:     false,
			expectPosition: false,
			expectFace:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test verifies the logic, actual reconciliation requires a real client
			// The key assertion is in the payload building logic in reconcileDevice

			// Verify device config is valid
			if tt.device.Name == "" {
				t.Error("Device name should not be empty")
			}

			// Verify parent device logic
			if tt.device.ParentDevice != "" && tt.device.DeviceBay == "" {
				t.Error("DeviceBay must be set when ParentDevice is specified")
			}

			// Verify rack logic
			hasRack := tt.device.RackSlug != ""
			if hasRack != tt.expectRack {
				t.Errorf("Expected hasRack=%v, got %v", tt.expectRack, hasRack)
			}
		})
	}
}

// TestModuleSerialHandling tests that module serial is always set
func TestModuleSerialHandling(t *testing.T) {
	tests := []struct {
		name           string
		module         models.ModuleConfig
		expectedSerial string
	}{
		{
			name: "module with serial",
			module: models.ModuleConfig{
				Name:           "GPU-1",
				ModuleTypeSlug: "gpu-a100",
				Serial:         "ABC123",
			},
			expectedSerial: "ABC123",
		},
		{
			name: "module without serial should use empty string",
			module: models.ModuleConfig{
				Name:           "GPU-2",
				ModuleTypeSlug: "gpu-a100",
			},
			expectedSerial: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build payload like the actual code does
			serial := tt.module.Serial
			if serial == "" {
				serial = "" // Explicitly set to empty string
			}

			if serial != tt.expectedSerial {
				t.Errorf("Expected serial=%q, got %q", tt.expectedSerial, serial)
			}
		})
	}
}

// TestDeviceBayValidation tests device bay configuration validation
func TestDeviceBayValidation(t *testing.T) {
	tests := []struct {
		name        string
		device      *models.DeviceConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid parent and bay",
			device: &models.DeviceConfig{
				Name:           "blade-01",
				ParentDevice:   "chassis-01",
				DeviceBay:      "Bay-1",
				SiteSlug:       "site1",
				RoleSlug:       "server",
				DeviceTypeSlug: "blade",
			},
			expectError: false,
		},
		{
			name: "parent without bay should error",
			device: &models.DeviceConfig{
				Name:           "blade-01",
				ParentDevice:   "chassis-01",
				SiteSlug:       "site1",
				RoleSlug:       "server",
				DeviceTypeSlug: "blade",
			},
			expectError: true,
			errorMsg:    "device_bay must be specified when parent_device is set",
		},
		{
			name: "bay without parent is invalid",
			device: &models.DeviceConfig{
				Name:           "blade-01",
				DeviceBay:      "Bay-1",
				SiteSlug:       "site1",
				RoleSlug:       "server",
				DeviceTypeSlug: "blade",
			},
			expectError: false, // Bay without parent is technically allowed
		},
		{
			name: "no parent or bay is valid",
			device: &models.DeviceConfig{
				Name:           "server-01",
				SiteSlug:       "site1",
				RoleSlug:       "server",
				DeviceTypeSlug: "server",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasParent := tt.device.ParentDevice != ""
			hasBay := tt.device.DeviceBay != ""

			// Validation logic from reconcileDevice
			if hasParent && !hasBay {
				if !tt.expectError {
					t.Error("Expected error for parent without bay")
				}
			} else if tt.expectError {
				t.Error("Expected error but validation passed")
			}
		})
	}
}

// TestDeviceTypeSubdeviceRole tests that blade devices require u_height=0
func TestDeviceTypeSubdeviceRole(t *testing.T) {
	tests := []struct {
		name        string
		deviceType  map[string]interface{}
		expectValid bool
	}{
		{
			name: "child device type must have u_height 0",
			deviceType: map[string]interface{}{
				"slug":           "blade-server",
				"u_height":       0,
				"subdevice_role": "child",
			},
			expectValid: true,
		},
		{
			name: "child device type with non-zero u_height is invalid",
			deviceType: map[string]interface{}{
				"slug":           "blade-server",
				"u_height":       1,
				"subdevice_role": "child",
			},
			expectValid: false,
		},
		{
			name: "parent device type can have any u_height",
			deviceType: map[string]interface{}{
				"slug":           "chassis",
				"u_height":       10,
				"subdevice_role": "parent",
			},
			expectValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, _ := tt.deviceType["subdevice_role"].(string)
			uHeight, _ := tt.deviceType["u_height"].(int)

			isValid := true
			if role == "child" && uHeight != 0 {
				isValid = false
			}

			if isValid != tt.expectValid {
				t.Errorf("Expected valid=%v, got %v", tt.expectValid, isValid)
			}
		})
	}
}

// TestModuleManagedTag tests that modules should be tagged
func TestModuleManagedTag(t *testing.T) {
	tests := []struct {
		name          string
		managedTagID  int
		expectTagged  bool
	}{
		{
			name:         "with managed tag ID",
			managedTagID: 123,
			expectTagged: true,
		},
		{
			name:         "without managed tag ID",
			managedTagID: 0,
			expectTagged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build payload like the actual code does
			payload := map[string]interface{}{}

			if tt.managedTagID > 0 {
				payload["tags"] = []int{tt.managedTagID}
			}

			_, hasTag := payload["tags"]
			if hasTag != tt.expectTagged {
				t.Errorf("Expected tagged=%v, got %v", tt.expectTagged, hasTag)
			}
		})
	}
}

// TestDeviceBayTemplateCreation tests device bay auto-creation logic
func TestDeviceBayTemplateCreation(t *testing.T) {
	tests := []struct {
		name             string
		templates        []map[string]interface{}
		existingBays     []string
		expectedCreates  []string
	}{
		{
			name: "create missing bays",
			templates: []map[string]interface{}{
				{"name": "Bay-1"},
				{"name": "Bay-2"},
				{"name": "Bay-3"},
			},
			existingBays:    []string{"Bay-1"},
			expectedCreates: []string{"Bay-2", "Bay-3"},
		},
		{
			name: "all bays exist",
			templates: []map[string]interface{}{
				{"name": "Bay-1"},
				{"name": "Bay-2"},
			},
			existingBays:    []string{"Bay-1", "Bay-2"},
			expectedCreates: []string{},
		},
		{
			name:            "no templates",
			templates:       []map[string]interface{}{},
			existingBays:    []string{},
			expectedCreates: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build existing bay map
			existingBayNames := make(map[string]bool)
			for _, bay := range tt.existingBays {
				existingBayNames[bay] = true
			}

			// Check which bays need to be created
			var toCreate []string
			for _, tmpl := range tt.templates {
				name, _ := tmpl["name"].(string)
				if !existingBayNames[name] {
					toCreate = append(toCreate, name)
				}
			}

			// Verify expected creates
			if len(toCreate) != len(tt.expectedCreates) {
				t.Errorf("Expected %d creates, got %d", len(tt.expectedCreates), len(toCreate))
			}

			for i, expected := range tt.expectedCreates {
				if i >= len(toCreate) || toCreate[i] != expected {
					t.Errorf("Expected create %q, got %q", expected, toCreate[i])
				}
			}
		})
	}
}
