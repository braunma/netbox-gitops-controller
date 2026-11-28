// SPDX-License-Identifier: Apache-2.0

package discovery

import (
	"fmt"
	"sort"
	"strings"
)

// StorageSummary groups drives by whole-GB capacity, e.g. "2x745GB,
// 16x14306GB". Every source that reports drives renders them through this, so
// a device documented by any of them reads the same in NetBox.
func StorageSummary(capacitiesGB []int) string {
	groups := make(map[int]int, len(capacitiesGB))
	for _, c := range capacitiesGB {
		groups[c]++
	}
	capacities := make([]int, 0, len(groups))
	for c := range groups {
		capacities = append(capacities, c)
	}
	sort.Ints(capacities)

	parts := make([]string, 0, len(capacities))
	for _, c := range capacities {
		parts = append(parts, fmt.Sprintf("%dx%dGB", groups[c], c))
	}
	return strings.Join(parts, ", ")
}

// GPU is one graphics card or accelerator as a controller reports it.
type GPU struct {
	Model    string
	MemoryGB int
}

// GPUSummary groups cards by model and memory in the order they were
// reported, e.g. "4× NVIDIA A100 (80 GB)" or "2× NVIDIA H100, 2× NVIDIA A30".
// The memory is left out where the controller did not report it.
func GPUSummary(gpus []GPU) string {
	counts := make(map[GPU]int, len(gpus))
	var order []GPU
	for _, g := range gpus {
		if counts[g] == 0 {
			order = append(order, g)
		}
		counts[g]++
	}

	parts := make([]string, 0, len(order))
	for _, g := range order {
		entry := fmt.Sprintf("%d× %s", counts[g], g.Model)
		if g.MemoryGB > 0 {
			entry += fmt.Sprintf(" (%d GB)", g.MemoryGB)
		}
		parts = append(parts, entry)
	}
	return strings.Join(parts, ", ")
}
