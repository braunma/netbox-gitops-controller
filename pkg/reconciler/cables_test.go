package reconciler

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

func TestCreatePairID(t *testing.T) {
	cr := &CableReconciler{
		processedPairs: make(map[string]bool),
	}

	tests := []struct {
		name     string
		aEnd     *CableEndpoint
		bEnd     *CableEndpoint
		expected string
	}{
		{
			name: "forward direction",
			aEnd: &CableEndpoint{
				DeviceName: "device-a",
				PortName:   "eth0",
				ObjectType: "dcim.interface",
				ObjectID:   100,
			},
			bEnd: &CableEndpoint{
				DeviceName: "device-b",
				PortName:   "eth1",
				ObjectType: "dcim.interface",
				ObjectID:   200,
			},
			expected: "dcim.interface:device-a:100 <-> dcim.interface:device-b:200",
		},
		{
			name: "reverse direction (should match forward)",
			aEnd: &CableEndpoint{
				DeviceName: "device-b",
				PortName:   "eth1",
				ObjectType: "dcim.interface",
				ObjectID:   200,
			},
			bEnd: &CableEndpoint{
				DeviceName: "device-a",
				PortName:   "eth0",
				ObjectType: "dcim.interface",
				ObjectID:   100,
			},
			expected: "dcim.interface:device-a:100 <-> dcim.interface:device-b:200",
		},
		{
			name: "different port types",
			aEnd: &CableEndpoint{
				DeviceName: "switch-01",
				PortName:   "Eth1/1",
				ObjectType: "dcim.interface",
				ObjectID:   50,
			},
			bEnd: &CableEndpoint{
				DeviceName: "patchpanel-01",
				PortName:   "1",
				ObjectType: "dcim.frontport",
				ObjectID:   60,
			},
			expected: "dcim.frontport:patchpanel-01:60 <-> dcim.interface:switch-01:50",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cr.createPairID(tt.aEnd, tt.bEnd)
			if result != tt.expected {
				t.Errorf("createPairID() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestCreatePairID_Bidirectional(t *testing.T) {
	cr := &CableReconciler{
		processedPairs: make(map[string]bool),
	}

	// Create endpoints
	endpointA := &CableEndpoint{
		DeviceName: "device-a",
		PortName:   "eth0",
		ObjectType: "dcim.interface",
		ObjectID:   100,
	}

	endpointB := &CableEndpoint{
		DeviceName: "device-b",
		PortName:   "eth1",
		ObjectType: "dcim.interface",
		ObjectID:   200,
	}

	// A→B and B→A should produce the same pair ID
	pairAB := cr.createPairID(endpointA, endpointB)
	pairBA := cr.createPairID(endpointB, endpointA)

	if pairAB != pairBA {
		t.Errorf("Bidirectional pair IDs don't match: A→B=%q, B→A=%q", pairAB, pairBA)
	}
}

func TestMatchesEndpoint(t *testing.T) {
	cr := &CableReconciler{
		processedPairs: make(map[string]bool),
	}

	tests := []struct {
		name     string
		cable    map[string]interface{}
		side     string
		endpoint *CableEndpoint
		expected bool
	}{
		{
			name: "matching interface endpoint",
			cable: map[string]interface{}{
				"termination_b_type": "dcim.interface",
				"termination_b_id":   float64(100),
			},
			side: "b",
			endpoint: &CableEndpoint{
				ObjectType: "dcim.interface",
				ObjectID:   100,
			},
			expected: true,
		},
		{
			name: "mismatched type",
			cable: map[string]interface{}{
				"termination_b_type": "dcim.frontport",
				"termination_b_id":   float64(100),
			},
			side: "b",
			endpoint: &CableEndpoint{
				ObjectType: "dcim.interface",
				ObjectID:   100,
			},
			expected: false,
		},
		{
			name: "mismatched ID",
			cable: map[string]interface{}{
				"termination_b_type": "dcim.interface",
				"termination_b_id":   float64(999),
			},
			side: "b",
			endpoint: &CableEndpoint{
				ObjectType: "dcim.interface",
				ObjectID:   100,
			},
			expected: false,
		},
		{
			name: "nested ID structure",
			cable: map[string]interface{}{
				"termination_a_type": "dcim.interface",
				"termination_a_id": map[string]interface{}{
					"id": float64(42),
				},
			},
			side: "a",
			endpoint: &CableEndpoint{
				ObjectType: "dcim.interface",
				ObjectID:   42,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cr.matchesEndpoint(tt.cable, tt.side, tt.endpoint)
			if result != tt.expected {
				t.Errorf("matchesEndpoint() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestVerifyCable(t *testing.T) {
	cr := &CableReconciler{
		processedPairs: make(map[string]bool),
	}

	tests := []struct {
		name     string
		cable    map[string]interface{}
		link     *models.LinkConfig
		expected bool
	}{
		{
			name: "matching cable type",
			cable: map[string]interface{}{
				"type": "dac-active",
			},
			link: &models.LinkConfig{
				CableType: "dac-active",
			},
			expected: true,
		},
		{
			name: "mismatched cable type",
			cable: map[string]interface{}{
				"type": "dac-passive",
			},
			link: &models.LinkConfig{
				CableType: "dac-active",
			},
			expected: false,
		},
		{
			name: "matching color",
			cable: map[string]interface{}{
				"type":  "dac-active",
				"color": "blue",
			},
			link: &models.LinkConfig{
				CableType: "dac-active",
				Color:     "blue",
			},
			expected: true,
		},
		{
			name: "mismatched color",
			cable: map[string]interface{}{
				"type":  "dac-active",
				"color": "red",
			},
			link: &models.LinkConfig{
				CableType: "dac-active",
				Color:     "blue",
			},
			expected: false,
		},
		{
			name: "matching length",
			cable: map[string]interface{}{
				"type":   "cat6a",
				"length": float64(5.0),
			},
			link: &models.LinkConfig{
				CableType: "cat6a",
				Length:    5.0,
			},
			expected: true,
		},
		{
			name: "mismatched length",
			cable: map[string]interface{}{
				"type":   "cat6a",
				"length": float64(10.0),
			},
			link: &models.LinkConfig{
				CableType: "cat6a",
				Length:    5.0,
			},
			expected: false,
		},
		{
			name:     "nil link config (no verification needed)",
			cable:    map[string]interface{}{},
			link:     nil,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cr.verifyCable(tt.cable, &CableEndpoint{}, &CableEndpoint{}, tt.link)
			if result != tt.expected {
				t.Errorf("verifyCable() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestReset(t *testing.T) {
	cr := &CableReconciler{
		processedPairs: make(map[string]bool),
	}

	// Add some processed pairs
	cr.processedPairs["pair1"] = true
	cr.processedPairs["pair2"] = true
	cr.processedPairs["pair3"] = true

	if len(cr.processedPairs) != 3 {
		t.Errorf("Expected 3 processed pairs, got %d", len(cr.processedPairs))
	}

	// Reset
	cr.Reset()

	if len(cr.processedPairs) != 0 {
		t.Errorf("After reset, expected 0 processed pairs, got %d", len(cr.processedPairs))
	}
}

func TestCableEndpoint(t *testing.T) {
	endpoint := &CableEndpoint{
		DeviceName: "test-device",
		PortName:   "eth0",
		ObjectType: "dcim.interface",
		ObjectID:   42,
	}

	if endpoint.DeviceName != "test-device" {
		t.Errorf("DeviceName = %q, expected %q", endpoint.DeviceName, "test-device")
	}

	if endpoint.PortName != "eth0" {
		t.Errorf("PortName = %q, expected %q", endpoint.PortName, "eth0")
	}

	if endpoint.ObjectType != "dcim.interface" {
		t.Errorf("ObjectType = %q, expected %q", endpoint.ObjectType, "dcim.interface")
	}

	if endpoint.ObjectID != 42 {
		t.Errorf("ObjectID = %d, expected %d", endpoint.ObjectID, 42)
	}
}

func TestLinkConfigFields(t *testing.T) {
	link := &models.LinkConfig{
		PeerDevice: "switch-01",
		PeerPort:   "Eth1/1",
		CableType:  "dac-active",
		Color:      "blue",
		Length:     2.5,
		LengthUnit: "m",
	}

	if link.PeerDevice != "switch-01" {
		t.Errorf("PeerDevice = %q, expected %q", link.PeerDevice, "switch-01")
	}

	if link.PeerPort != "Eth1/1" {
		t.Errorf("PeerPort = %q, expected %q", link.PeerPort, "Eth1/1")
	}

	if link.CableType != "dac-active" {
		t.Errorf("CableType = %q, expected %q", link.CableType, "dac-active")
	}

	if link.Color != "blue" {
		t.Errorf("Color = %q, expected %q", link.Color, "blue")
	}

	if link.Length != 2.5 {
		t.Errorf("Length = %f, expected %f", link.Length, 2.5)
	}

	if link.LengthUnit != "m" {
		t.Errorf("LengthUnit = %q, expected %q", link.LengthUnit, "m")
	}
}
