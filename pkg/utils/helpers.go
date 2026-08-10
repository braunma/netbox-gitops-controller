// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"fmt"
	"reflect"
	"strings"
)

// Slugify converts a string to a URL-safe slug
func Slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	// Remove any characters that aren't alphanumeric or hyphens
	var result strings.Builder
	for _, char := range s {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			result.WriteRune(char)
		}
	}
	return result.String()
}

// GetIDFromObject extracts an ID from various NetBox object formats
func GetIDFromObject(obj interface{}) int {
	if obj == nil {
		return 0
	}

	switch v := obj.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		// Handle string IDs by attempting to parse
		var id int
		if _, err := fmt.Sscanf(v, "%d", &id); err == nil {
			return id
		}
		return 0
	case map[string]interface{}:
		if id, ok := v["id"].(int); ok {
			return id
		}
		if id, ok := v["id"].(float64); ok {
			return int(id)
		}
		if id, ok := v["id"].(string); ok {
			var parsedID int
			if _, err := fmt.Sscanf(id, "%d", &parsedID); err == nil {
				return parsedID
			}
		}
	default:
		// Handle named types with underlying map[string]interface{} type
		// This handles types like "client.Object" which are defined as named types
		val := reflect.ValueOf(obj)
		if val.Kind() == reflect.Map {
			// Try to convert to map[string]interface{}
			if mapVal, ok := obj.(map[string]interface{}); ok {
				return GetIDFromObject(mapVal)
			}
			// For named types, we need to extract the value differently
			idVal := val.MapIndex(reflect.ValueOf("id"))
			if idVal.IsValid() {
				return GetIDFromObject(idVal.Interface())
			}
		}
	}

	return 0
}

// IsManaged checks if an object is managed by gitops
func IsManaged(obj map[string]interface{}, managedTagID int) bool {
	tags, ok := obj["tags"].([]interface{})
	if !ok {
		return false
	}

	for _, tag := range tags {
		if id := GetIDFromObject(tag); id == managedTagID {
			return true
		}
		if tagMap, ok := tag.(map[string]interface{}); ok {
			if slug, ok := tagMap["slug"].(string); ok && slug == "gitops" {
				return true
			}
		}
	}

	return false
}
