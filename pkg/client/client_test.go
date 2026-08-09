// SPDX-License-Identifier: Apache-2.0

package client

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

func TestFormatLookup(t *testing.T) {
	logger := utils.NewLogger(true)
	client := &NetBoxClient{
		logger: logger,
	}

	tests := []struct {
		name     string
		lookup   map[string]interface{}
		expected string
	}{
		{
			name: "lookup with name",
			lookup: map[string]interface{}{
				"name": "test-device",
			},
			expected: "name=test-device",
		},
		{
			name: "lookup with slug",
			lookup: map[string]interface{}{
				"slug": "test-slug",
			},
			expected: "slug=test-slug",
		},
		{
			name: "lookup with custom field",
			lookup: map[string]interface{}{
				"device_id": 42,
			},
			expected: "device_id=42",
		},
		{
			name:     "empty lookup",
			lookup:   map[string]interface{}{},
			expected: "{}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.formatLookup(tt.lookup)
			if result != tt.expected {
				t.Errorf("formatLookup() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestFormatValue(t *testing.T) {
	logger := utils.NewLogger(true)
	client := &NetBoxClient{
		logger: logger,
	}

	tests := []struct {
		name     string
		value    interface{}
		expected string
	}{
		{
			name:     "nil value",
			value:    nil,
			expected: "<nil>",
		},
		{
			name:     "string value",
			value:    "test",
			expected: "\"test\"",
		},
		{
			name:     "integer value",
			value:    42,
			expected: "42",
		},
		{
			name:     "float value",
			value:    3.14,
			expected: "3.14",
		},
		{
			name:     "boolean true",
			value:    true,
			expected: "true",
		},
		{
			name:     "boolean false",
			value:    false,
			expected: "false",
		},
		{
			name:     "empty slice",
			value:    []interface{}{},
			expected: "[]",
		},
		{
			name:     "slice with items",
			value:    []interface{}{"a", "b", "c"},
			expected: "[...3 items]",
		},
		{
			name: "map with id",
			value: map[string]interface{}{
				"id":   123,
				"name": "test",
			},
			expected: "{id: 123}",
		},
		{
			name: "map without id",
			value: map[string]interface{}{
				"name": "test",
			},
			expected: "{...}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.formatValue(tt.value)
			if result != tt.expected {
				t.Errorf("formatValue() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestCalculateDiff(t *testing.T) {
	logger := utils.NewLogger(true)
	client := &NetBoxClient{
		logger: logger,
	}

	tests := []struct {
		name     string
		existing Object
		desired  map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name: "no changes",
			existing: Object{
				"name":    "test-device",
				"enabled": true,
			},
			desired: map[string]interface{}{
				"name":    "test-device",
				"enabled": true,
			},
			expected: map[string]interface{}{},
		},
		{
			name: "field value change",
			existing: Object{
				"name":    "test-device",
				"enabled": false,
			},
			desired: map[string]interface{}{
				"name":    "test-device",
				"enabled": true,
			},
			expected: map[string]interface{}{
				"enabled": true,
			},
		},
		{
			name: "new field added",
			existing: Object{
				"name": "test-device",
			},
			desired: map[string]interface{}{
				"name":    "test-device",
				"enabled": true,
			},
			expected: map[string]interface{}{
				"enabled": true,
			},
		},
		{
			name: "nested object ID extraction",
			existing: Object{
				"device": map[string]interface{}{
					"id":   42,
					"name": "parent-device",
				},
			},
			desired: map[string]interface{}{
				"device": 42,
			},
			expected: map[string]interface{}{},
		},
		{
			name: "nested object ID change",
			existing: Object{
				"device": map[string]interface{}{
					"id": 42,
				},
			},
			desired: map[string]interface{}{
				"device": 99,
			},
			expected: map[string]interface{}{
				"device": 99,
			},
		},
		{
			name: "nil value ignored",
			existing: Object{
				"name":        "test-device",
				"description": "old description",
			},
			desired: map[string]interface{}{
				"name":        "test-device",
				"description": nil,
			},
			expected: map[string]interface{}{},
		},
		{
			name: "int to float conversion",
			existing: Object{
				"mtu": float64(1500),
			},
			desired: map[string]interface{}{
				"mtu": 1500,
			},
			expected: map[string]interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.calculateDiff(tt.existing, tt.desired)
			if len(result) != len(tt.expected) {
				t.Errorf("calculateDiff() returned %d changes, expected %d", len(result), len(tt.expected))
			}

			for key, expectedVal := range tt.expected {
				actualVal, exists := result[key]
				if !exists {
					t.Errorf("Expected key %q in diff, but it was missing", key)
					continue
				}
				if actualVal != expectedVal {
					t.Errorf("For key %q: got %v, expected %v", key, actualVal, expectedVal)
				}
			}
		})
	}
}

func TestValuesEqual(t *testing.T) {
	tests := []struct {
		name     string
		a        interface{}
		b        interface{}
		expected bool
	}{
		{
			name:     "equal strings",
			a:        "test",
			b:        "test",
			expected: true,
		},
		{
			name:     "unequal strings",
			a:        "test1",
			b:        "test2",
			expected: false,
		},
		{
			name:     "equal integers",
			a:        42,
			b:        42,
			expected: true,
		},
		{
			name:     "int to float conversion",
			a:        float64(42),
			b:        42,
			expected: true,
		},
		{
			name:     "float to int conversion",
			a:        42,
			b:        float64(42),
			expected: true,
		},
		{
			name:     "equal floats",
			a:        3.14,
			b:        3.14,
			expected: true,
		},
		{
			name:     "unequal floats",
			a:        3.14,
			b:        2.71,
			expected: false,
		},
		{
			name:     "equal bools",
			a:        true,
			b:        true,
			expected: true,
		},
		{
			name:     "unequal bools",
			a:        true,
			b:        false,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := valuesEqual(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("valuesEqual(%v, %v) = %v, expected %v", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestExtractTagIDs(t *testing.T) {
	logger := utils.NewLogger(true)
	client := &NetBoxClient{
		logger: logger,
	}

	tests := []struct {
		name     string
		tags     interface{}
		expected []int
	}{
		{
			name: "array of tag objects",
			tags: []interface{}{
				map[string]interface{}{"id": float64(1), "name": "tag1"},
				map[string]interface{}{"id": float64(2), "name": "tag2"},
			},
			expected: []int{1, 2},
		},
		{
			name:     "array of int IDs",
			tags:     []int{1, 2, 3},
			expected: []int{1, 2, 3},
		},
		{
			name:     "empty array",
			tags:     []interface{}{},
			expected: []int{},
		},
		{
			name: "mixed types in array",
			tags: []interface{}{
				map[string]interface{}{"id": float64(10)},
				"invalid",
				map[string]interface{}{"id": float64(20)},
			},
			expected: []int{10, 20},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.extractTagIDs(tt.tags)
			if len(result) != len(tt.expected) {
				t.Errorf("extractTagIDs() returned %d IDs, expected %d", len(result), len(tt.expected))
			}

			for i, expectedID := range tt.expected {
				if i >= len(result) {
					t.Errorf("Missing expected ID at index %d: %d", i, expectedID)
					continue
				}
				if result[i] != expectedID {
					t.Errorf("At index %d: got %d, expected %d", i, result[i], expectedID)
				}
			}
		})
	}
}

func TestIDSetEqual(t *testing.T) {
	logger := utils.NewLogger(true)
	client := &NetBoxClient{
		logger: logger,
	}

	tests := []struct {
		name     string
		existing interface{}
		desired  interface{}
		expected bool
	}{
		{
			name: "equal tag arrays",
			existing: []interface{}{
				map[string]interface{}{"id": float64(1)},
				map[string]interface{}{"id": float64(2)},
			},
			desired:  []int{1, 2},
			expected: true,
		},
		{
			name: "unequal tag arrays - different IDs",
			existing: []interface{}{
				map[string]interface{}{"id": float64(1)},
				map[string]interface{}{"id": float64(2)},
			},
			desired:  []int{1, 3},
			expected: false,
		},
		{
			name: "unequal tag arrays - different lengths",
			existing: []interface{}{
				map[string]interface{}{"id": float64(1)},
			},
			desired:  []int{1, 2},
			expected: false,
		},
		{
			name:     "empty tag arrays",
			existing: []interface{}{},
			desired:  []int{},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.idSetEqual(tt.existing, tt.desired)
			if result != tt.expected {
				t.Errorf("idSetEqual() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

// TestCalculateDiffListFields guards the idempotency of slice-valued fields
// such as tagged_vlans: NetBox returns them as nested objects while we send
// plain []int, so an unchanged list must not be reported as a change.
func TestCalculateDiffListFields(t *testing.T) {
	client := &NetBoxClient{logger: utils.NewLogger(true)}

	existing := Object{
		"tagged_vlans": []interface{}{
			map[string]interface{}{"id": float64(10), "name": "vlan10"},
			map[string]interface{}{"id": float64(20), "name": "vlan20"},
		},
	}

	// Same VLANs, different order and representation: must be a no-op.
	if changes := client.calculateDiff(existing, map[string]interface{}{
		"tagged_vlans": []int{20, 10},
	}); len(changes) != 0 {
		t.Errorf("expected no changes for equal tagged_vlans set, got %v", changes)
	}

	// A genuinely different set must still be detected.
	changes := client.calculateDiff(existing, map[string]interface{}{
		"tagged_vlans": []int{10, 30},
	})
	if _, ok := changes["tagged_vlans"]; !ok {
		t.Errorf("expected tagged_vlans change to be detected, got %v", changes)
	}
}

func TestVIDRangesEqual(t *testing.T) {
	// The desired payload the reconciler builds: a list with one [min, max] pair.
	desired := []interface{}{[]interface{}{1, 4094}}

	tests := []struct {
		name     string
		existing interface{}
		want     bool
	}{
		{
			name:     "array form as NetBox returns it (float64)",
			existing: []interface{}{[]interface{}{float64(1), float64(4094)}},
			want:     true,
		},
		{
			name:     "string min-max form",
			existing: []interface{}{"1-4094"},
			want:     true,
		},
		{
			name:     "start/end object form",
			existing: []interface{}{map[string]interface{}{"start": float64(1), "end": float64(4094)}},
			want:     true,
		},
		{
			name:     "different bounds",
			existing: []interface{}{[]interface{}{float64(1), float64(100)}},
			want:     false,
		},
		{
			name:     "missing entirely",
			existing: nil,
			want:     false,
		},
		{
			name: "multiple ranges out of order still equal",
			existing: []interface{}{
				[]interface{}{float64(200), float64(299)},
				[]interface{}{float64(1), float64(99)},
			},
			want: false, // desired declares only one range, so the sets differ
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vidRangesEqual(tt.existing, desired); got != tt.want {
				t.Errorf("vidRangesEqual(%v, %v) = %v, want %v", tt.existing, desired, got, tt.want)
			}
		})
	}

	// calculateDiff must route vid_ranges through the canonical comparison, not
	// the ID-set path (which would treat any two range lists as equal).
	c := &NetBoxClient{logger: utils.NewLogger(true)}
	existing := Object{"vid_ranges": []interface{}{[]interface{}{float64(1), float64(4094)}}}
	if changes := c.calculateDiff(existing, map[string]interface{}{"vid_ranges": desired}); len(changes) != 0 {
		t.Errorf("expected no vid_ranges change for equal ranges, got %v", changes)
	}
	if changes := c.calculateDiff(existing, map[string]interface{}{
		"vid_ranges": []interface{}{[]interface{}{1, 100}},
	}); len(changes) == 0 {
		t.Error("expected vid_ranges change to be detected for differing bounds")
	}
}

// TestValuesEqualNumericStrings covers NetBox's decimal serialization: fields
// such as u_height and weight come back as JSON strings when the serializer
// coerces decimals, while the payload carries a number. Without a numeric
// comparison every device type carrying one is re-PATCHed on every run.
func TestValuesEqualNumericStrings(t *testing.T) {
	equal := []struct {
		name string
		a, b interface{}
	}{
		{"string decimal vs float", "1.0", 1.0},
		{"float vs string decimal", 1.0, "1.0"},
		{"string decimal vs int", "2.0", 2},
		{"int vs string decimal", 2, "2.0"},
		{"fractional height", "0.5", 0.5},
		{"weight", "19.40", 19.4},
		{"zero", "0.0", 0},
		{"padded by the serializer", " 4.0 ", 4.0},
		// Pre-existing numeric conversions must keep working.
		{"float vs int", 3.0, 3},
		{"int vs float", 3, 3.0},
		{"string vs string", "active", "active"},
	}
	for _, tt := range equal {
		t.Run(tt.name, func(t *testing.T) {
			if !valuesEqual(tt.a, tt.b) {
				t.Errorf("valuesEqual(%#v, %#v) = false, want true", tt.a, tt.b)
			}
		})
	}

	// The numeric comparison must not collapse genuinely different values, and
	// must not fire for strings that are not numbers.
	notEqual := []struct {
		name string
		a, b interface{}
	}{
		{"different decimals", "1.0", 2.0},
		{"different ints", 1, 2},
		{"non-numeric string vs number", "active", 1.0},
		{"empty string vs zero", "", 0},
		{"number-ish word vs number", "one", 1},
		{"different strings", "active", "offline"},
		{"nil vs zero", nil, 0},
		{"bool vs number", true, 1},
	}
	for _, tt := range notEqual {
		t.Run(tt.name, func(t *testing.T) {
			if valuesEqual(tt.a, tt.b) {
				t.Errorf("valuesEqual(%#v, %#v) = true, want false", tt.a, tt.b)
			}
		})
	}
}

// TestCalculateChangesToleratesStringifiedDecimals is the end-to-end form of
// the above: an object whose decimals NetBox returns as strings must produce
// no changes when the desired payload already matches.
func TestCalculateChangesToleratesStringifiedDecimals(t *testing.T) {
	c := &NetBoxClient{}

	existing := Object{"u_height": "1.0", "weight": "19.40", "model": "R650"}
	desired := map[string]interface{}{"u_height": 1.0, "weight": 19.4, "model": "R650"}

	if changes := c.calculateDiff(existing, desired); len(changes) != 0 {
		t.Errorf("calculateDiff() = %v, want no changes for equal stringified decimals", changes)
	}

	// A real difference must still be detected.
	changed := map[string]interface{}{"u_height": 2.0, "weight": 19.4, "model": "R650"}
	if changes := c.calculateDiff(existing, changed); len(changes) != 1 {
		t.Errorf("calculateDiff() = %v, want the changed u_height detected", changes)
	}
}

// TestPortMappingsEqual covers NetBox 4.6 front port terminations, a list of
// {position, rear_port, rear_port_position} maps. Entries carry no "id", so
// the generic ID-set comparison reads both sides as empty and reports them
// permanently equal — a changed mapping would never be written.
func TestPortMappingsEqual(t *testing.T) {
	mapping := func(pos, rear, rearPos interface{}) interface{} {
		return map[string]interface{}{"position": pos, "rear_port": rear, "rear_port_position": rearPos}
	}

	// NetBox returns rear_port as a nested object and numbers as float64;
	// the payload sends a plain ID and Go ints.
	existing := []interface{}{mapping(1.0, map[string]interface{}{"id": 7.0}, 3.0)}
	desired := []interface{}{mapping(1, 7, 3)}
	if !portMappingsEqual(existing, desired) {
		t.Error("portMappingsEqual() = false for equal mappings in NetBox's and our representations")
	}

	// Order must not matter.
	twoA := []interface{}{mapping(1, 7, 1), mapping(2, 8, 1)}
	twoB := []interface{}{mapping(2, 8, 1), mapping(1, 7, 1)}
	if !portMappingsEqual(twoA, twoB) {
		t.Error("portMappingsEqual() = false for the same set in a different order")
	}

	for _, tc := range []struct {
		name             string
		existing, desire []interface{}
	}{
		{"different rear port", []interface{}{mapping(1, 7, 1)}, []interface{}{mapping(1, 9, 1)}},
		{"different position", []interface{}{mapping(1, 7, 1)}, []interface{}{mapping(2, 7, 1)}},
		{"different rear position", []interface{}{mapping(1, 7, 1)}, []interface{}{mapping(1, 7, 4)}},
		{"extra mapping", []interface{}{mapping(1, 7, 1)}, twoA},
		{"empty vs one", nil, []interface{}{mapping(1, 7, 1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if portMappingsEqual(tc.existing, tc.desire) {
				t.Error("portMappingsEqual() = true, want the difference detected")
			}
		})
	}
}

// TestUnresolvedReference covers the --dry-run bootstrap path: an object
// planned but never written is registered with id 0, and NetBox rejects
// "site_id=0" as an invalid choice rather than returning no results.
func TestUnresolvedReference(t *testing.T) {
	if _, ok := unresolvedReference(map[string]interface{}{"name": "sw-01", "site_id": 0}); !ok {
		t.Error("unresolvedReference() = false for a zero reference id")
	}
	if key, _ := unresolvedReference(map[string]interface{}{"site_id": 0}); key != "site_id" {
		t.Errorf("unresolvedReference() key = %q, want site_id", key)
	}
	if _, ok := unresolvedReference(map[string]interface{}{"name": "sw-01", "site_id": 4}); ok {
		t.Error("unresolvedReference() = true for a resolved reference")
	}
	// A zero that is not a reference must not trigger it: 0 is a legitimate
	// value for fields like vid or position.
	if _, ok := unresolvedReference(map[string]interface{}{"vid": 0, "position": 0}); ok {
		t.Error("unresolvedReference() = true for a non-reference zero")
	}
	if _, ok := unresolvedReference(map[string]interface{}{"slug": "berlin"}); ok {
		t.Error("unresolvedReference() = true for a lookup with no reference")
	}
}
