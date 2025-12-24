package utils

import (
	"strings"
)

// NormalizeColor converts various color formats to NetBox format (6-char hex without #)
func NormalizeColor(input string) string {
	if input == "" {
		return ""
	}

	// Remove # prefix if present
	input = strings.TrimPrefix(input, "#")

	// Convert to lowercase
	input = strings.ToLower(input)

	// If it's 3 characters, expand to 6 (e.g., "f00" -> "ff0000")
	if len(input) == 3 {
		return string([]byte{
			input[0], input[0],
			input[1], input[1],
			input[2], input[2],
		})
	}

	// If it's already 6 characters, return as-is
	if len(input) == 6 {
		return input
	}

	// Invalid format, return empty
	return ""
}

// GetCableColor returns the default color for a cable type
func GetCableColor(cableType string) string {
	colors := map[string]string{
		"cat6":  "f44336",
		"cat6a": "ffeb3b",
		"cat7":  "ff9800",
		"dac":   "000000",
		"fiber": "00bcd4",
		"om3":   "00bcd4",
		"om4":   "2196f3",
		"os2":   "9c27b0",
	}

	if color, ok := colors[strings.ToLower(cableType)]; ok {
		return color
	}

	return ""
}
