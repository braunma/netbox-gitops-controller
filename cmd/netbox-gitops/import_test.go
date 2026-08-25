// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/internal/nbtest"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
)

// TestImportCommandWritesAndDiffs drives the import subcommand end to end
// against a fake NetBox: it writes files, then a --diff of the same instance
// against what it wrote reports no drift.
func TestImportCommandWritesAndDiffs(t *testing.T) {
	f, _ := nbtest.New(t)
	f.Seed("dcim", "sites", client.Object{"name": "Berlin", "slug": "berlin",
		"status": map[string]interface{}{"value": "active"}})
	f.Seed("dcim", "sites", client.Object{"name": "Munich", "slug": "munich",
		"status": map[string]interface{}{"value": "active"}})
	f.Seed("dcim", "device-roles", client.Object{"name": "Server", "slug": "server", "color": "00ff00"})
	f.Seed("extras", "tags", client.Object{"name": "GitOps Managed", "slug": constants.ManagedTagSlug, "color": "4caf50"})

	t.Setenv("NETBOX_URL", f.URL())
	t.Setenv("NETBOX_TOKEN", "test-token")

	dir := t.TempDir()

	// Reset the package-level state the command reads.
	dataDir = dir
	configFile = ".env"

	// Write phase.
	cmd := newImportCommand()
	cmd.SetArgs([]string{"--report", "-"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("import execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "definitions/sites/sites.yaml")); err != nil {
		t.Fatalf("sites file not written: %v", err)
	}

	// Diff phase: the same instance against what we just wrote → no drift,
	// exit code 0.
	exitCode = 0
	dcmd := newImportCommand()
	dcmd.SetArgs([]string{"--diff", dir, "--report", "-"})
	if err := dcmd.Execute(); err != nil {
		t.Fatalf("import --diff execute: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("diff of an unchanged import reported drift (exit %d)", exitCode)
	}
}
