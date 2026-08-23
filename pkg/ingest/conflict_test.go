// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// runTracked plans one snapshot with a shared tracker and, when asked,
// applies it — the shape of one source's turn inside a `collect` run.
func runTracked(t *testing.T, root string, tracker *ConflictTracker, snap *discovery.Snapshot, apply bool) *Plan {
	t.Helper()
	dl := loader.NewDataLoader(root, utils.NewLogger(true))
	inv, err := dl.LoadInventoryWithProvenance()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	plan, err := BuildPlan(snap, inv, Options{DataDir: root, Conflicts: tracker})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if apply {
		if err := Apply(plan); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	return plan
}

func vmSnapshot(source string, memoryMB int) *discovery.Snapshot {
	return &discovery.Snapshot{Source: source, VMs: []discovery.VM{
		{Name: "web-01", Cluster: "berlin-prod-cluster", MemoryMB: memoryMB},
	}}
}

// Two sources of one run disagreeing about a declared VM's memory: the later
// source's value ends up in the file — configuration order is precedence —
// and the disagreement is reported instead of silently winning.
func TestCrossSourceConflictIsReportedAndLaterSourceWins(t *testing.T) {
	root := repo(t, map[string]string{
		"inventory/virtual/prod/web-01.yaml": "- name: \"web-01\"\n  cluster: \"berlin-prod-cluster\"\n  memory: 1024\n",
	})
	tracker := NewConflictTracker()

	first := runTracked(t, root, tracker, vmSnapshot("pve-prod", 4096), true)
	if len(first.Conflicts) != 0 || first.Summary.CrossSourceConflicts != 0 {
		t.Fatalf("the first source of a run has nobody to conflict with: %+v", first.Conflicts)
	}

	second := runTracked(t, root, tracker, vmSnapshot("openstack-prod", 2048), true)
	if len(second.Conflicts) != 1 || second.Summary.CrossSourceConflicts != 1 {
		t.Fatalf("conflicts = %+v (summary %+v), want exactly one", second.Conflicts, second.Summary)
	}

	c := second.Conflicts[0]
	if !strings.Contains(c.Object, "web-01") || c.Key != KeyVMMemory {
		t.Errorf("conflict names %q key %q, want the VM's memory", c.Object, c.Key)
	}
	if c.EarlierSource != "pve-prod" || c.EarlierValue != "4096" {
		t.Errorf("earlier write recorded as %s=%s from %s, want 4096 from pve-prod", c.Key, c.EarlierValue, c.EarlierSource)
	}
	if c.LaterSource != "openstack-prod" || c.LaterValue != "2048" {
		t.Errorf("later write recorded as %s=%s from %s, want 2048 from openstack-prod", c.Key, c.LaterValue, c.LaterSource)
	}

	if got := read(t, root, "inventory/virtual/prod/web-01.yaml"); !strings.Contains(got, "memory: 2048") {
		t.Errorf("the later source's value is not the one in the file:\n%s", got)
	}
}

// Two sources reporting the same value are agreeing, not conflicting. Planned
// without applying, so both snapshots actually write the key and the tracker
// sees the value twice — the applied case never even plans the second write.
func TestAgreeingSourcesRaiseNoConflict(t *testing.T) {
	root := repo(t, map[string]string{
		"inventory/virtual/prod/web-01.yaml": "- name: \"web-01\"\n  cluster: \"berlin-prod-cluster\"\n  memory: 1024\n",
	})
	tracker := NewConflictTracker()

	runTracked(t, root, tracker, vmSnapshot("pve-prod", 4096), false)
	second := runTracked(t, root, tracker, vmSnapshot("openstack-prod", 4096), false)
	if len(second.Conflicts) != 0 || second.Summary.CrossSourceConflicts != 0 {
		t.Errorf("identical values from two sources reported as a conflict: %+v", second.Conflicts)
	}
}
