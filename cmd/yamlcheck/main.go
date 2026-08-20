// SPDX-License-Identifier: Apache-2.0

// yamlcheck validates the YAML in a data directory without touching NetBox:
// first the syntax of every file, then the typed model validation (required
// fields, cross-field constraints), then the cross-object lint checks in
// pkg/lint (references that resolve to nothing, two devices in one rack unit,
// an IP used twice, a port cabled twice). It is used by the CI pipeline
// (yaml_check job) to catch bad definitions before they reach the reconciler.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/braunma/netbox-gitops-controller/pkg/lint"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// defaultDirs are the conventional locations checked when no directory is
// named: the private data and the committed examples.
var defaultDirs = []string{"definitions", "inventory", "example/definitions", "example/inventory"}

func main() {
	var (
		strict       bool
		allowRefs    bool
		skipLint     bool
		showWarnings bool
	)
	flag.BoolVar(&strict, "strict", false,
		"treat warnings as failures (for a repository that declares everything it references)")
	flag.BoolVar(&allowRefs, "allow-undeclared-refs", false,
		"report references to objects this repository does not declare as warnings instead of errors")
	flag.BoolVar(&skipLint, "no-lint", false,
		"run syntax and model validation only, skipping the cross-object checks")
	flag.BoolVar(&showWarnings, "warnings", true, "print warnings (errors are always printed)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"Usage: yamlcheck [flags] [dir ...]\n\n"+
				"A directory that contains definitions/ or inventory/ is treated as a data\n"+
				"directory; any other directory is scanned for YAML syntax and its parent is\n"+
				"used as the data directory. With no argument, %s are checked.\n\nFlags:\n",
			strings.Join(defaultDirs, ", "))
		flag.PrintDefaults()
	}
	flag.Parse()

	dirs := flag.Args()
	if len(dirs) == 0 {
		dirs = defaultDirs
	}

	files, err := collectFiles(dirs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Println("⚠️  No YAML files found to check.")
		return
	}

	fmt.Printf("Found %d YAML files to validate\n", len(files))
	failed := false
	for _, file := range files {
		fmt.Printf("Checking %s...\n", file)
		data, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ %s: %v\n", file, err)
			failed = true
			continue
		}
		var content interface{}
		if err := yaml.Unmarshal(data, &content); err != nil {
			fmt.Fprintf(os.Stderr, "❌ %s: %v\n", file, err)
			failed = true
		}
	}

	if failed {
		os.Exit(1)
	}
	fmt.Println("✅ All found YAML files are valid!")

	// Typed model validation and linting run per data directory. Every
	// directory is checked before exiting, so one bad file does not hide the
	// findings in the next.
	fatal := false
	for _, base := range dataDirs(dirs) {
		dataset, err := collect(base)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Model validation failed for %s:\n%v\n", base, err)
			os.Exit(1)
		}
		if skipLint {
			continue
		}
		findings := lint.Check(dataset, lint.Options{AllowUndeclaredRefs: allowRefs})
		report(base, findings, showWarnings)
		if lint.HasErrors(findings, strict) {
			fatal = true
		}
	}
	if fatal {
		os.Exit(1)
	}
	fmt.Println("✅ Model validation passed!")
}

// collectFiles gathers every YAML file under the named directories. A data
// directory is scanned through its definitions/ and inventory/ subtrees so
// unrelated YAML elsewhere in the tree is left alone.
func collectFiles(dirs []string) ([]string, error) {
	var files []string
	seen := make(map[string]bool)
	for _, dir := range dirs {
		roots := []string{dir}
		if isDataDir(dir) {
			roots = []string{filepath.Join(dir, "definitions"), filepath.Join(dir, "inventory")}
		}
		for _, root := range roots {
			if _, err := os.Stat(root); os.IsNotExist(err) {
				continue
			}
			err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() && (strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) && !seen[path] {
					seen[path] = true
					files = append(files, path)
				}
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("failed to scan %s: %w", root, err)
			}
		}
	}
	return files, nil
}

// isDataDir reports whether dir is itself a data directory — one holding
// definitions/ or inventory/ — rather than one of those subtrees.
func isDataDir(dir string) bool {
	for _, sub := range []string{"definitions", "inventory"} {
		if info, err := os.Stat(filepath.Join(dir, sub)); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// dataDirs derives the data directories to load from the checked directories:
// a data directory stands for itself, and "example/definitions" for "example".
// The result is deduplicated and keeps the order the arguments came in.
func dataDirs(dirs []string) []string {
	var bases []string
	seen := make(map[string]bool)
	for _, dir := range dirs {
		base := filepath.Dir(dir)
		if isDataDir(dir) {
			base = dir
		}
		if !seen[base] {
			seen[base] = true
			bases = append(bases, base)
		}
	}
	return bases
}

// report prints the findings for a data directory. Errors always print;
// warnings are printed unless suppressed.
func report(base string, findings []lint.Finding, showWarnings bool) {
	var errors, warnings int
	for _, f := range findings {
		if f.Severity == lint.Error {
			errors++
		} else {
			warnings++
		}
	}
	if errors == 0 && warnings == 0 {
		return
	}

	fmt.Printf("Linting %s...\n", base)
	for _, f := range findings {
		if f.Severity == lint.Error {
			fmt.Fprintf(os.Stderr, "❌ %s: %s (%s)\n", f.Object, f.Message, f.Check)
			continue
		}
		if showWarnings {
			fmt.Printf("⚠️  %s: %s (%s)\n", f.Object, f.Message, f.Check)
		}
	}
	fmt.Printf("%d error(s), %d warning(s) in %s\n", errors, warnings, base)
}

// collect loads every known definition and inventory folder under base through
// the typed loader, which runs per-model validation, and returns the result as
// a dataset for the linter. Missing folders are skipped by the loader, so a
// partial layout is fine.
func collect(base string) (lint.Dataset, error) {
	var ds lint.Dataset
	if !isDataDir(base) {
		return ds, nil
	}

	fmt.Printf("Validating models in %s...\n", base)
	logger := utils.NewLogger(false)
	dl := loader.NewDataLoader(base, logger)

	var err error
	load := func(fn func() error) {
		if err == nil {
			err = fn()
		}
	}

	load(func() (e error) { ds.Sites, e = dl.LoadSites("definitions/sites"); return })
	load(func() (e error) { ds.Racks, e = dl.LoadRacks("definitions/racks"); return })
	load(func() (e error) { ds.Roles, e = dl.LoadRoles("definitions/roles"); return })
	load(func() (e error) { _, e = dl.LoadTags("definitions/extras"); return })
	load(func() (e error) { _, e = dl.LoadCustomFields("definitions/custom_fields"); return })
	load(func() (e error) { ds.VRFs, e = dl.LoadVRFs("definitions/vrfs"); return })
	load(func() (e error) { _, e = dl.LoadVLANGroups("definitions/vlan_groups"); return })
	load(func() (e error) { ds.VLANs, e = dl.LoadVLANs("definitions/vlans"); return })
	load(func() (e error) { ds.Prefixes, e = dl.LoadPrefixes("definitions/prefixes"); return })
	load(func() (e error) { ds.Platforms, e = dl.LoadPlatforms("definitions/platforms"); return })
	load(func() (e error) { _, e = dl.LoadTenantGroups("definitions/tenant_groups"); return })
	load(func() (e error) { ds.Tenants, e = dl.LoadTenants("definitions/tenants"); return })
	load(func() (e error) { _, e = dl.LoadClusterTypes("definitions/virtualization/cluster_types"); return })
	load(func() (e error) { _, e = dl.LoadClusterGroups("definitions/virtualization/cluster_groups"); return })
	load(func() (e error) { ds.Clusters, e = dl.LoadClusters("definitions/virtualization/clusters"); return })

	// Native types are merged with the community-format libraries the same way
	// the controller merges them, so a reference to a vendored type resolves
	// here too.
	var nativeModuleTypes, libraryModuleTypes []*models.ModuleType
	load(func() (e error) { nativeModuleTypes, e = dl.LoadModuleTypes("definitions/module_types"); return })
	load(func() (e error) {
		libraryModuleTypes, e = dl.LoadModuleTypeLibrary(resolveLibrary("MODULETYPE_LIBRARY", base, "module_type_library"))
		return
	})
	var nativeDeviceTypes, libraryDeviceTypes []*models.DeviceType
	load(func() (e error) { nativeDeviceTypes, e = dl.LoadDeviceTypes("definitions/device_types"); return })
	load(func() (e error) {
		libraryDeviceTypes, e = dl.LoadDeviceTypeLibrary(resolveLibrary("DEVICETYPE_LIBRARY", base, "device_type_library"))
		return
	})

	// Match the reconciler's device folders so VM definitions under
	// inventory/virtual are not mis-parsed as devices by a recursive scan.
	var active, passive []*models.DeviceConfig
	load(func() (e error) { active, e = dl.LoadDevices("inventory/hardware/active"); return })
	load(func() (e error) { passive, e = dl.LoadDevices("inventory/hardware/passive"); return })
	load(func() (e error) { ds.VMs, e = dl.LoadVMs("inventory/virtual"); return })

	if err != nil {
		return lint.Dataset{}, err
	}

	ds.ModuleTypes = loader.MergeModuleTypes(nativeModuleTypes, libraryModuleTypes, logger)
	ds.DeviceTypes = loader.MergeDeviceTypes(nativeDeviceTypes, libraryDeviceTypes, logger)
	ds.Devices = append(append([]*models.DeviceConfig{}, active...), passive...)
	return ds, nil
}

// resolveLibrary mirrors the controller's library resolution: the environment
// variable if set, else the conventional path inside the data directory.
func resolveLibrary(envVar, base, folder string) string {
	if env := os.Getenv(envVar); env != "" {
		return env
	}
	return filepath.Join(base, "definitions", folder)
}
