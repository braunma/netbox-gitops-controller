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

func TestExtractTagIDsAndSlugs(t *testing.T) {
	tests := []struct {
		name         string
		input        []interface{}
		expectedIDs  []int
		expectedSlugs []string
	}{
		{
			name:         "empty",
			input:        []interface{}{},
			expectedIDs:  nil,
			expectedSlugs: nil,
		},
		{
			name:         "integers only",
			input:        []interface{}{1, 2, 3},
			expectedIDs:  []int{1, 2, 3},
			expectedSlugs: nil,
		},
		{
			name:         "strings only",
			input:        []interface{}{"tag1", "tag2"},
			expectedIDs:  nil,
			expectedSlugs: []string{"tag1", "tag2"},
		},
		{
			name:         "mixed",
			input:        []interface{}{1, "gitops", map[string]interface{}{"id": 5, "slug": "managed"}},
			expectedIDs:  []int{1, 5},
			expectedSlugs: []string{"gitops", "managed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids, slugs := ExtractTagIDsAndSlugs(tt.input)

			if len(ids) != len(tt.expectedIDs) {
				t.Errorf("ExtractTagIDsAndSlugs() ids length = %d, expected %d", len(ids), len(tt.expectedIDs))
			} else {
				for i, id := range ids {
					if id != tt.expectedIDs[i] {
						t.Errorf("ExtractTagIDsAndSlugs() ids[%d] = %d, expected %d", i, id, tt.expectedIDs[i])
					}
				}
			}

			if len(slugs) != len(tt.expectedSlugs) {
				t.Errorf("ExtractTagIDsAndSlugs() slugs length = %d, expected %d", len(slugs), len(tt.expectedSlugs))
			} else {
				for i, slug := range slugs {
					if slug != tt.expectedSlugs[i] {
						t.Errorf("ExtractTagIDsAndSlugs() slugs[%d] = %q, expected %q", i, slug, tt.expectedSlugs[i])
					}
				}
			}
		})
	}
}

func TestIsManaged(t *testing.T) {
	tests := []struct {
		name         string
		obj          map[string]interface{}
		managedTagID int
		expected     bool
	}{
		{
			name: "managed by ID",
			obj: map[string]interface{}{
				"tags": []interface{}{1, 5, 10},
			},
			managedTagID: 5,
			expected:     true,
		},
		{
			name: "managed by slug",
			obj: map[string]interface{}{
				"tags": []interface{}{
					map[string]interface{}{"slug": "gitops", "id": 1},
				},
			},
			managedTagID: 99,
			expected:     true,
		},
		{
			name: "not managed",
			obj: map[string]interface{}{
				"tags": []interface{}{1, 2, 3},
			},
			managedTagID: 5,
			expected:     false,
		},
		{
			name:         "no tags",
			obj:          map[string]interface{}{},
			managedTagID: 5,
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsManaged(tt.obj, tt.managedTagID)
			if result != tt.expected {
				t.Errorf("IsManaged() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		name     string
		slice    []string
		item     string
		expected bool
	}{
		{
			name:     "found",
			slice:    []string{"a", "b", "c"},
			item:     "b",
			expected: true,
		},
		{
			name:     "not found",
			slice:    []string{"a", "b", "c"},
			item:     "d",
			expected: false,
		},
		{
			name:     "empty slice",
			slice:    []string{},
			item:     "a",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Contains(tt.slice, tt.item)
			if result != tt.expected {
				t.Errorf("Contains(%v, %q) = %v, expected %v", tt.slice, tt.item, result, tt.expected)
			}
		})
	}
}

func TestContainsInt(t *testing.T) {
	tests := []struct {
		name     string
		slice    []int
		item     int
		expected bool
	}{
		{
			name:     "found",
			slice:    []int{1, 2, 3},
			item:     2,
			expected: true,
		},
		{
			name:     "not found",
			slice:    []int{1, 2, 3},
			item:     4,
			expected: false,
		},
		{
			name:     "empty slice",
			slice:    []int{},
			item:     1,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ContainsInt(tt.slice, tt.item)
			if result != tt.expected {
				t.Errorf("ContainsInt(%v, %d) = %v, expected %v", tt.slice, tt.item, result, tt.expected)
			}
		})
	}
}
