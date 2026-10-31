// SPDX-License-Identifier: Apache-2.0

// yamlcheck validates the YAML in a data directory without touching NetBox:
// first the syntax of every file, then the typed model validation (required
// fields, cross-field constraints), then the cross-object lint checks in
// pkg/lint (references that resolve to nothing, two devices in one rack unit,
// an IP used twice, a port cabled twice). It is used by the CI pipeline
// (yaml_check job) to catch bad definitions before they reach the reconciler.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
		codequality  string
	)
	flag.BoolVar(&strict, "strict", false,
		"treat warnings as failures (for a repository that declares everything it references)")
	flag.BoolVar(&allowRefs, "allow-undeclared-refs", false,
		"report references to objects this repository does not declare as warnings instead of errors")
	flag.BoolVar(&skipLint, "no-lint", false,
		"run syntax and model validation only, skipping the cross-object checks")
	flag.BoolVar(&showWarnings, "warnings", true, "print warnings (errors are always printed)")
	flag.StringVar(&codequality, "codequality", "",
		"also write every problem and finding to this file in GitLab Code Quality format, so a merge request shows them on the changed lines")
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
	var issues []codeQualityIssue
	for _, base := range dataDirs(dirs) {
		dataset, err := collect(base)
		if err != nil {
			// Every problem on its own line. The cross-object checks need the
			// whole directory loaded, so they run once these are fixed.
			list := loader.Problems(err)
			for _, problem := range list {
				fmt.Fprintf(os.Stderr, "❌ %v\n", problem)
				issues = append(issues, problemIssue(base, problem))
			}
			fmt.Fprintf(os.Stderr, "%d problem(s) in %s; fix them to run the cross-object checks\n", len(list), base)
			fatal = true
			continue
		}
		if skipLint {
			continue
		}
		findings := lint.Check(dataset, lint.Options{AllowUndeclaredRefs: allowRefs})
		report(base, findings, showWarnings)
		for _, f := range findings {
			issues = append(issues, findingIssue(base, f))
		}
		if lint.HasErrors(findings, strict) {
			fatal = true
		}
	}
	if codequality != "" {
		if err := writeCodeQuality(codequality, issues); err != nil {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
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

// GitLab's Code Quality report format: the merge request widget lists the
// issues, and the diff view pins each one to its line. Every loader problem
// and lint finding becomes one issue; the fingerprint is stable across runs
// (no line number in it), so GitLab can tell a new issue from one that moved.
type codeQualityIssue struct {
	Description string           `json:"description"`
	CheckName   string           `json:"check_name"`
	Fingerprint string           `json:"fingerprint"`
	Severity    string           `json:"severity"`
	Location    codeQualityWhere `json:"location"`
}

type codeQualityWhere struct {
	Path  string `json:"path"`
	Lines struct {
		Begin int `json:"begin"`
	} `json:"lines"`
}

// problemLine matches the prefix every loader problem carries: the file, and
// the line when the loader knows it.
var problemLine = regexp.MustCompile(`^([^:\s]+?)(?::(\d+))?: `)

func problemIssue(base string, problem error) codeQualityIssue {
	text := problem.Error()
	issue := codeQualityIssue{Description: text, CheckName: "yaml-load", Severity: "major"}
	issue.Location.Path = base
	issue.Location.Lines.Begin = 1
	if m := problemLine.FindStringSubmatch(text); m != nil {
		issue.Location.Path = m[1]
		if m[2] != "" {
			fmt.Sscanf(m[2], "%d", &issue.Location.Lines.Begin)
		}
	}
	issue.Fingerprint = fingerprint("load", issue.Location.Path, stripLine(text))
	return issue
}

func findingIssue(base string, f lint.Finding) codeQualityIssue {
	issue := codeQualityIssue{
		Description: f.Object + ": " + f.Message,
		CheckName:   f.Check,
		Severity:    "minor",
	}
	if f.Severity == lint.Error {
		issue.Severity = "major"
	}
	issue.Location.Path, issue.Location.Lines.Begin = base, 1
	if f.File != "" {
		issue.Location.Path, issue.Location.Lines.Begin = f.File, f.Line
	}
	issue.Fingerprint = fingerprint(f.Check, f.Object, f.Message)
	return issue
}

// stripLine removes the ":<line>" of a problem's prefix, so a problem keeps
// its fingerprint when lines above it are added or removed.
func stripLine(text string) string {
	return problemLine.ReplaceAllString(text, "$1: ")
}

func fingerprint(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func writeCodeQuality(path string, issues []codeQualityIssue) error {
	if issues == nil {
		issues = []codeQualityIssue{} // an empty list, not null: GitLab wants an array
	}
	data, err := json.MarshalIndent(issues, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil { // #nosec G306 -- a report meant to be read
		return fmt.Errorf("writing the code quality report: %w", err)
	}
	fmt.Printf("Wrote %d issue(s) to %s (GitLab Code Quality format)\n", len(issues), path)
	return nil
}
