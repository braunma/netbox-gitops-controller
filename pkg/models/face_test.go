// SPDX-License-Identifier: Apache-2.0

package models

import "testing"

// A rack position without a face is sent as front-mounted; a declared face,
// or no position at all, is left as written.
func TestEffectiveFace(t *testing.T) {
	cases := []struct {
		name     string
		position int
		face     string
		want     string
	}{
		{"position, no face", 10, "", "front"},
		{"position, rear", 10, "rear", "rear"},
		{"position, front", 10, "front", "front"},
		{"no position, no face", 0, "", ""},
		{"no position, rear", 0, "rear", "rear"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &DeviceConfig{Name: "srv", Position: tc.position, Face: tc.face}
			if got := d.EffectiveFace(); got != tc.want {
				t.Errorf("EffectiveFace() = %q, want %q", got, tc.want)
			}
		})
	}
}
