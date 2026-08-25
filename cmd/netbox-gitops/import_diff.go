// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/importer"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// diffResult compares a rendered import against an existing repository on disk
// and prints what a re-import would change: files added, removed, or modified.
// It writes nothing. It reports whether anything differs, which the caller
// turns into exit code 2 — the "what changed in the UI since we imported"
// question a scheduled pipeline job asks.
func diffResult(result *importer.Result, dir string) (bool, error) {
	out := utils.DefaultOutput()

	generated := map[string][]byte{}
	for _, f := range result.Files {
		generated[f.Path] = f.Bytes
	}

	// Existing YAML files under the compared directory, relative to it.
	existing, err := existingYAML(dir)
	if err != nil {
		return false, err
	}

	paths := map[string]bool{}
	for p := range generated {
		paths[p] = true
	}
	for p := range existing {
		paths[p] = true
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)

	changed := false
	for _, p := range ordered {
		gen, inGen := generated[p]
		old, inOld := existing[p]
		switch {
		case inGen && !inOld:
			changed = true
			fmt.Fprintf(out, "+ %s (new)\n", p)
			printDiff(out, nil, gen)
		case !inGen && inOld:
			changed = true
			fmt.Fprintf(out, "- %s (removed)\n", p)
		case string(gen) != string(old):
			changed = true
			fmt.Fprintf(out, "~ %s\n", p)
			printDiff(out, old, gen)
		}
	}
	if !changed {
		fmt.Fprintln(out, "No differences: the import matches the repository.")
	}
	return changed, nil
}

// existingYAML reads every .yaml/.yml file under dir, keyed by its path
// relative to dir.
func existingYAML(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		if ext := filepath.Ext(path); ext != ".yaml" && ext != ".yml" {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path) // #nosec G304 -- path from a user-named compare dir
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	if os.IsNotExist(err) {
		return out, nil
	}
	return out, err
}

// printDiff prints a compact line diff: a common prefix and suffix are elided,
// and the differing middle is shown as removed (-) then added (+) lines. It is
// not a minimal Myers diff, but it is enough to review what an import changed.
func printDiff(out interface{ Write([]byte) (int, error) }, old, gen []byte) {
	oldLines := splitLines(old)
	newLines := splitLines(gen)

	pre := commonPrefix(oldLines, newLines)
	suf := commonSuffix(oldLines[pre:], newLines[pre:])

	for _, l := range oldLines[pre : len(oldLines)-suf] {
		fmt.Fprintf(out, "    - %s\n", l)
	}
	for _, l := range newLines[pre : len(newLines)-suf] {
		fmt.Fprintf(out, "    + %s\n", l)
	}
}

func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func commonPrefix(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func commonSuffix(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}
