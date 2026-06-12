// yamlcheck validates the syntax of all YAML files under the given
// directories and additionally runs the typed model validation (required
// fields, cross-field constraints) for standard definitions/inventory
// layouts. It is used by the CI pipeline (yaml_check job) to catch
// malformed definitions before they reach the reconciler.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

func main() {
	dirs := os.Args[1:]
	if len(dirs) == 0 {
		dirs = []string{"definitions", "inventory", "example/definitions", "example/inventory"}
	}

	var files []string
	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && (strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to scan %s: %v\n", dir, err)
			os.Exit(1)
		}
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

	// Typed model validation for standard data layouts. Derive base
	// directories from the checked dirs (e.g. "example/definitions" -> base
	// "example", "definitions" -> base ".").
	bases := map[string]bool{}
	for _, dir := range dirs {
		base := filepath.Dir(dir)
		bases[base] = true
	}

	for base := range bases {
		if err := validateModels(base); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Model validation failed for %s:\n%v\n", base, err)
			os.Exit(1)
		}
	}
	fmt.Println("✅ Model validation passed!")
}

// validateModels loads every known definition/inventory folder under base
// through the typed loader, which runs per-model validation. Missing folders
// are skipped by the loader, so partial layouts are fine.
func validateModels(base string) error {
	if _, err := os.Stat(filepath.Join(base, "definitions")); os.IsNotExist(err) {
		if _, err := os.Stat(filepath.Join(base, "inventory")); os.IsNotExist(err) {
			return nil
		}
	}

	fmt.Printf("Validating models in %s...\n", base)
	dl := loader.NewDataLoader(base, utils.NewLogger(false))

	checks := []func() error{
		func() error { _, err := dl.LoadTags("definitions/extras"); return err },
		func() error { _, err := dl.LoadRoles("definitions/roles"); return err },
		func() error { _, err := dl.LoadSites("definitions/sites"); return err },
		func() error { _, err := dl.LoadRacks("definitions/racks"); return err },
		func() error { _, err := dl.LoadVRFs("definitions/vrfs"); return err },
		func() error { _, err := dl.LoadVLANGroups("definitions/vlan_groups"); return err },
		func() error { _, err := dl.LoadVLANs("definitions/vlans"); return err },
		func() error { _, err := dl.LoadPrefixes("definitions/prefixes"); return err },
		func() error { _, err := dl.LoadModuleTypes("definitions/module_types"); return err },
		func() error { _, err := dl.LoadDeviceTypes("definitions/device_types"); return err },
		func() error { _, err := dl.LoadDevices("inventory"); return err },
	}

	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}
