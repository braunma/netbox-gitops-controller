package utils

import (
	"fmt"
	"strings"
	"time"
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
	}

	return 0
}

// ExtractTagIDsAndSlugs extracts tag IDs and slugs from a mixed tag list
func ExtractTagIDsAndSlugs(tags []interface{}) (ids []int, slugs []string) {
	for _, tag := range tags {
		switch v := tag.(type) {
		case int:
			ids = append(ids, v)
		case string:
			slugs = append(slugs, v)
		case map[string]interface{}:
			if id := GetIDFromObject(v); id != 0 {
				ids = append(ids, id)
			}
			if slug, ok := v["slug"].(string); ok {
				slugs = append(slugs, slug)
			}
		}
	}
	return ids, slugs
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

// SafeSleep sleeps for the specified duration unless in dry-run mode
func SafeSleep(durationMs int, dryRun bool) {
	if !dryRun && durationMs > 0 {
		time.Sleep(time.Duration(durationMs) * time.Millisecond)
	}
}

// GetTerminationType determines the termination type from an endpoint name
func GetTerminationType(endpoint string) string {
	switch endpoint {
	case "interfaces":
		return "dcim.interface"
	case "front_ports":
		return "dcim.frontport"
	case "rear_ports":
		return "dcim.rearport"
	default:
		return ""
	}
}

// Contains checks if a string slice contains a specific string
func Contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ContainsInt checks if an int slice contains a specific int
func ContainsInt(slice []int, item int) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
