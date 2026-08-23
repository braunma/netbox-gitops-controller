// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"testing"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple lowercase",
			input:    "simple",
			expected: "simple",
		},
		{
			name:     "uppercase to lowercase",
			input:    "UPPERCASE",
			expected: "uppercase",
		},
		{
			name:     "spaces to hyphens",
			input:    "hello world",
			expected: "hello-world",
		},
		{
			name:     "mixed case with spaces",
			input:    "Hello World Test",
			expected: "hello-world-test",
		},
		{
			name:     "special characters removed",
			input:    "test@#$%123",
			expected: "test123",
		},
		{
			name:     "hyphens preserved",
			input:    "test-case-one",
			expected: "test-case-one",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Slugify(tt.input)
			if result != tt.expected {
				t.Errorf("Slugify(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestObject is a named type to test GetIDFromObject with named map types
type TestObject map[string]interface{}

func TestGetIDFromObject(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected int
	}{
		{
			name:     "integer",
			input:    42,
			expected: 42,
		},
		{
			name:     "float64",
			input:    27.0,
			expected: 27,
		},
		{
			name:     "map with id int",
			input:    map[string]interface{}{"id": 100},
			expected: 100,
		},
		{
			name:     "map with id float64",
			input:    map[string]interface{}{"id": 200.0},
			expected: 200,
		},
		{
			name:     "named type with id float64",
			input:    TestObject{"id": 27.0},
			expected: 27,
		},
		{
			name:     "named type with id int",
			input:    TestObject{"id": 42},
			expected: 42,
		},
		{
			name:     "nil",
			input:    nil,
			expected: 0,
		},
		{
			name:     "map without id",
			input:    map[string]interface{}{"name": "test"},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetIDFromObject(tt.input)
			if result != tt.expected {
				t.Errorf("GetIDFromObject(%v) = %d, expected %d", tt.input, result, tt.expected)
			}
		})
	}
}
