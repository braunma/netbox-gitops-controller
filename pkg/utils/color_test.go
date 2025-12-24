package utils

import "testing"

func TestNormalizeColor(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "6-char hex with hash",
			input:    "#ff0000",
			expected: "ff0000",
		},
		{
			name:     "6-char hex without hash",
			input:    "00ff00",
			expected: "00ff00",
		},
		{
			name:     "3-char hex",
			input:    "f00",
			expected: "ff0000",
		},
		{
			name:     "3-char hex with hash",
			input:    "#0f0",
			expected: "00ff00",
		},
		{
			name:     "uppercase",
			input:    "#FF00AA",
			expected: "ff00aa",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "invalid length",
			input:    "12345",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeColor(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizeColor(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetCableColor(t *testing.T) {
	tests := []struct {
		name       string
		cableType  string
		expected   string
	}{
		{
			name:       "cat6",
			cableType:  "cat6",
			expected:   "f44336",
		},
		{
			name:       "cat6a",
			cableType:  "cat6a",
			expected:   "ffeb3b",
		},
		{
			name:       "fiber",
			cableType:  "fiber",
			expected:   "00bcd4",
		},
		{
			name:       "unknown type",
			cableType:  "unknown",
			expected:   "",
		},
		{
			name:       "case insensitive",
			cableType:  "CAT6A",
			expected:   "ffeb3b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetCableColor(tt.cableType)
			if result != tt.expected {
				t.Errorf("GetCableColor(%q) = %q, expected %q", tt.cableType, result, tt.expected)
			}
		})
	}
}
