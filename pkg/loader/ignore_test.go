// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// writeSites writes a sites definition file into <base>/definitions/sites.
func writeSites(t *testing.T, base, name, content string) {
	t.Helper()
	dir := filepath.Join(base, "definitions", "sites")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func TestLoadSkipsIgnoredFilesByDefault(t *testing.T) {
	base := t.TempDir()
	writeSites(t, base, "sites.yaml", "- name: Berlin\n  slug: berlin\n")
	// Parked: owned elsewhere, kept in the repo for reference.
	writeSites(t, base, "_staged.yaml", "- name: Staged\n  slug: staged\n")

	dl := NewDataLoader(base, utils.NewLogger(false))
	sites, err := dl.LoadSites("definitions/sites")
	if err != nil {
		t.Fatalf("LoadSites() error = %v", err)
	}

	if len(sites) != 1 || sites[0].Slug != "berlin" {
		t.Fatalf("loaded %d site(s) %+v, want only the unparked one", len(sites), sites)
	}
}

func TestLoadIncludesIgnoredFilesWhenPatternsCleared(t *testing.T) {
	base := t.TempDir()
	writeSites(t, base, "sites.yaml", "- name: Berlin\n  slug: berlin\n")
	writeSites(t, base, "_staged.yaml", "- name: Staged\n  slug: staged\n")

	dl := NewDataLoader(base, utils.NewLogger(false))
	// What --include-ignored-files does.
	dl.SetIgnorePatterns(nil)

	sites, err := dl.LoadSites("definitions/sites")
	if err != nil {
		t.Fatalf("LoadSites() error = %v", err)
	}
	if len(sites) != 2 {
		t.Fatalf("loaded %d site(s), want both once ignoring is disabled", len(sites))
	}
}

func TestLoadHonoursCustomIgnorePatterns(t *testing.T) {
	base := t.TempDir()
	writeSites(t, base, "sites.yaml", "- name: Berlin\n  slug: berlin\n")
	writeSites(t, base, "imported-from-cmdb.yaml", "- name: CMDB\n  slug: cmdb\n")

	dl := NewDataLoader(base, utils.NewLogger(false))
	dl.SetIgnorePatterns([]string{"imported-*.yaml"})

	sites, err := dl.LoadSites("definitions/sites")
	if err != nil {
		t.Fatalf("LoadSites() error = %v", err)
	}
	if len(sites) != 1 || sites[0].Slug != "berlin" {
		t.Fatalf("loaded %+v, want the custom pattern to skip the imported file", sites)
	}
}

func TestIgnorePatternsMatchBasenameNotPath(t *testing.T) {
	base := t.TempDir()
	// A directory whose name would match the pattern must not take its files
	// with it — only basenames are matched.
	dir := filepath.Join(base, "definitions", "sites", "_archive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sites.yaml"), []byte("- name: Old\n  slug: old\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	dl := NewDataLoader(base, utils.NewLogger(false))
	sites, err := dl.LoadSites("definitions/sites")
	if err != nil {
		t.Fatalf("LoadSites() error = %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("loaded %d site(s), want the file inside the _archive directory to still load", len(sites))
	}
}

func TestValidateIgnorePatterns(t *testing.T) {
	if err := ValidateIgnorePatterns([]string{"_*.yaml", "*.yml", "imported-?.yaml"}); err != nil {
		t.Errorf("ValidateIgnorePatterns() error = %v, want valid globs accepted", err)
	}

	// A malformed glob would otherwise match nothing and silently load a file
	// the operator meant to park.
	err := ValidateIgnorePatterns([]string{"[unterminated"})
	if err == nil {
		t.Fatal("ValidateIgnorePatterns() = nil, want an error for a malformed glob")
	}
}

func TestIgnoredFilesAreReportable(t *testing.T) {
	base := t.TempDir()
	writeSites(t, base, "sites.yaml", "- name: Berlin\n  slug: berlin\n")
	writeSites(t, base, "_staged.yaml", "- name: Staged\n  slug: staged\n")

	dl := NewDataLoader(base, utils.NewLogger(false))
	if got := dl.IgnoredFiles(); len(got) != 0 {
		t.Fatalf("IgnoredFiles() = %v before loading, want empty", got)
	}

	if _, err := dl.LoadSites("definitions/sites"); err != nil {
		t.Fatalf("LoadSites() error = %v", err)
	}

	// The caller needs these to warn before pruning, which would delete any
	// managed object declared only in a parked file.
	ignored := dl.IgnoredFiles()
	if len(ignored) != 1 || filepath.Base(ignored[0]) != "_staged.yaml" {
		t.Fatalf("IgnoredFiles() = %v, want the parked file reported", ignored)
	}
}
