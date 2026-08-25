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

// The import target is an output directory. It must be used exactly as given:
// the sync's data-directory discovery falls back to example/ when the working
// directory holds no definitions/, which for an import would silently write the
// whole estate into the example dataset instead of the target.
func TestImportNeverFallsBackToExampleDir(t *testing.T) {
	f, _ := nbtest.New(t)
	f.Seed("dcim", "sites", client.Object{"name": "Berlin", "slug": "berlin"})
	t.Setenv("NETBOX_URL", f.URL())
	t.Setenv("NETBOX_TOKEN", "x")

	// A working directory that has no definitions/ but does have example/definitions —
	// exactly this repository's own layout, and the shape that triggered the fallback.
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "example", "definitions"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	target := filepath.Join(work, "out")
	dataDir = target
	configFile = ".env"

	cmd := newImportCommand()
	cmd.SetArgs([]string{"--report", "-"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("import: %v", err)
	}

	if _, err := os.Stat(filepath.Join(target, "definitions/sites/sites.yaml")); err != nil {
		t.Fatalf("import did not write to the target directory: %v", err)
	}
	// example/ must be untouched beyond the empty definitions/ dir we created.
	entries, err := os.ReadDir(filepath.Join(work, "example", "definitions"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("import wrote into example/definitions: %v", entries)
	}
}

// --dry-run and --diff promise to write nothing. That has to include the
// coverage report, which was being saved to disk before the mode was checked.
func TestImportDryRunAndDiffWriteNothing(t *testing.T) {
	f, _ := nbtest.New(t)
	f.Seed("dcim", "sites", client.Object{"name": "Berlin", "slug": "berlin"})
	t.Setenv("NETBOX_URL", f.URL())
	t.Setenv("NETBOX_TOKEN", "x")

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"dry-run", []string{"--dry-run"}},
		{"diff", nil}, // filled in below, needs the dir
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			dataDir = dir
			configFile = ".env"
			args := tc.args
			if args == nil {
				args = []string{"--diff", dir}
			}

			exitCode = 0
			cmd := newImportCommand()
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("import %v: %v", args, err)
			}

			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Fatalf("%s wrote %v into the target directory", tc.name, names)
			}
		})
	}
}
