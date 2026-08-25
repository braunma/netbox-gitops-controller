// SPDX-License-Identifier: Apache-2.0

package importer

import "sort"

// sortedUnique returns the input sorted with duplicates removed. Every list
// the importer emits goes through this (tags, filenames, object order) so the
// output is byte-stable regardless of the order NetBox returned things in.
func sortedUnique(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
