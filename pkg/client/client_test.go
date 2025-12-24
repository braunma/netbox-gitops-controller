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

func TestTagsEqual(t *testing.T) {
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
			desired: []int{1, 2},
			expected: true,
		},
		{
			name: "unequal tag arrays - different IDs",
			existing: []interface{}{
				map[string]interface{}{"id": float64(1)},
				map[string]interface{}{"id": float64(2)},
			},
			desired: []int{1, 3},
			expected: false,
		},
		{
			name: "unequal tag arrays - different lengths",
			existing: []interface{}{
				map[string]interface{}{"id": float64(1)},
			},
			desired: []int{1, 2},
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
			result := client.tagsEqual(tt.existing, tt.desired)
			if result != tt.expected {
				t.Errorf("tagsEqual() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
