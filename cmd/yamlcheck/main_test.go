// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// dataTree writes a minimal but valid data directory: one site, one rack, one
// role, one device type and one device that references them all.
func dataTree(t *testing.T) string {
	t.Helper()
	base := t.TempDir()

	write := func(rel, content string) {
		path := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	write("definitions/sites/sites.yaml", "- name: Berlin\n  slug: berlin\n")
	write("definitions/racks/racks.yaml", "- name: Rack A01\n  slug: rack-a01\n  site_slug: berlin\n")
	write("definitions/roles/roles.yaml", "- name: Server\n  slug: server\n  color: 4caf50\n")
	write("definitions/device_types/servers.yaml", `- model: R740
  slug: r740
  manufacturer: Dell
  u_height: 2
  interfaces:
    - name: eth0
      type: 25gbase-x-sfp28
`)
	write("inventory/hardware/active/servers.yaml", `defaults:
  site_slug: berlin
  role_slug: server
  device_type_slug: r740
  rack_slug: rack-a01
devices:
  - name: srv-01
    position: 10
`)
	return base
}

func TestIsDataDir(t *testing.T) {
	base := dataTree(t)

	if !isDataDir(base) {
		t.Errorf("a directory holding definitions/ and inventory/ is a data directory")
	}
	if isDataDir(filepath.Join(base, "definitions")) {
		t.Errorf("definitions/ is a subtree of a data directory, not one itself")
	}
	if isDataDir(filepath.Join(base, "does-not-exist")) {
		t.Errorf("a missing directory is not a data directory")
	}
}

func TestDataDirsDerivesBases(t *testing.T) {
	base := dataTree(t)

	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "a data directory stands for itself",
			in:   []string{base},
			want: []string{base},
		},
		{
			name: "a subtree resolves to its parent, deduplicated",
			in:   []string{filepath.Join(base, "definitions"), filepath.Join(base, "inventory")},
			want: []string{base},
		},
		{
			name: "the conventional relative layout resolves to the working directory",
			in:   []string{"definitions", "inventory"},
			want: []string{"."},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dataDirs(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// A data directory is scanned through definitions/ and inventory/, so YAML
// that lives elsewhere in the tree (a CI config, a Helm chart) is left alone.
func TestCollectFilesSkipsYAMLOutsideTheDataSubtrees(t *testing.T) {
	base := dataTree(t)
	if err := os.WriteFile(filepath.Join(base, "unrelated.yaml"), []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := collectFiles([]string{base})
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	if len(files) != 5 {
		t.Errorf("got %d files, want the 5 under definitions/ and inventory/: %v", len(files), files)
	}
	for _, f := range files {
		if filepath.Base(f) == "unrelated.yaml" {
			t.Errorf("YAML outside the data subtrees should not be checked: %s", f)
		}
	}
}

func TestCollectFilesDeduplicatesOverlappingArguments(t *testing.T) {
	base := dataTree(t)

	files, err := collectFiles([]string{base, filepath.Join(base, "definitions")})
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		if seen[f] {
			t.Errorf("file listed twice: %s", f)
		}
		seen[f] = true
	}
}

func TestCollectFilesIgnoresMissingDirectories(t *testing.T) {
	files, err := collectFiles([]string{filepath.Join(t.TempDir(), "nope")})
	if err != nil {
		t.Fatalf("a missing directory is skipped, not an error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("got %v, want no files", files)
	}
}

func TestCollectBuildsDatasetForLinting(t *testing.T) {
	base := dataTree(t)

	ds, err := collect(base)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(ds.Sites) != 1 || len(ds.Racks) != 1 || len(ds.Roles) != 1 {
		t.Errorf("definitions missing from the dataset: %d sites, %d racks, %d roles",
			len(ds.Sites), len(ds.Racks), len(ds.Roles))
	}
	if len(ds.DeviceTypes) != 1 {
		t.Errorf("got %d device types, want 1", len(ds.DeviceTypes))
	}
	if len(ds.Devices) != 1 || ds.Devices[0].Name != "srv-01" {
		t.Errorf("got devices %v, want srv-01", ds.Devices)
	}
	// The grouped form's defaults must reach the linter, or every device would
	// look like it references nothing.
	if ds.Devices[0].SiteSlug != "berlin" {
		t.Errorf("file defaults were not merged: site_slug=%q", ds.Devices[0].SiteSlug)
	}
}

func TestCollectSkipsADirectoryThatIsNotADataDirectory(t *testing.T) {
	ds, err := collect(t.TempDir())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(ds.Devices) != 0 || len(ds.Sites) != 0 {
		t.Errorf("expected an empty dataset, got %d devices and %d sites", len(ds.Devices), len(ds.Sites))
	}
}

func TestResolveLibraryPrefersTheEnvironment(t *testing.T) {
	t.Setenv("DEVICETYPE_LIBRARY", "/elsewhere/device-types")
	if got := resolveLibrary("DEVICETYPE_LIBRARY", "data", "device_type_library"); got != "/elsewhere/device-types" {
		t.Errorf("got %q, want the environment value", got)
	}

	t.Setenv("DEVICETYPE_LIBRARY", "")
	want := filepath.Join("data", "definitions", "device_type_library")
	if got := resolveLibrary("DEVICETYPE_LIBRARY", "data", "device_type_library"); got != want {
		t.Errorf("got %q, want the conventional path %q", got, want)
	}
}
