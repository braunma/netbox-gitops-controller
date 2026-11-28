// SPDX-License-Identifier: Apache-2.0

package utils

import "testing"

func TestCanonicalMAC(t *testing.T) {
	for in, want := range map[string]string{
		"02:00:00:aa:bb:01": "02:00:00:AA:BB:01",
		"02-00-00-AA-BB-01": "02:00:00:AA:BB:01",
		"0200.00aa.bb01":    "02:00:00:AA:BB:01",
		" 020000aabb01 ":    "02:00:00:AA:BB:01",
		"not-a-mac":         "NOT-A-MAC",
	} {
		if got := CanonicalMAC(in); got != want {
			t.Errorf("CanonicalMAC(%q) = %q, want %q", in, got, want)
		}
	}
}
