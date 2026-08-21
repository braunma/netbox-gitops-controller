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

// collect loads a data directory through the shared loader, which
// model-validates every object as it goes, and returns it for the linter.
func collect(base string) (lint.Dataset, error) {
	if !isDataDir(base) {
		return lint.Dataset{}, nil
	}

	fmt.Printf("Validating models in %s...\n", base)
	dl := loader.NewDataLoader(base, utils.NewLogger(false))
	return dl.LoadDataset(loader.DatasetOptions{
		DeviceTypeLibrary: resolveLibrary("DEVICETYPE_LIBRARY", base, "device_type_library"),
		ModuleTypeLibrary: resolveLibrary("MODULETYPE_LIBRARY", base, "module_type_library"),
	})
}

// resolveLibrary mirrors the controller's library resolution: the environment
// variable if set, else the conventional path inside the data directory.
func resolveLibrary(envVar, base, folder string) string {
	if env := os.Getenv(envVar); env != "" {
		return env
	}
	return filepath.Join(base, "definitions", folder)
}
