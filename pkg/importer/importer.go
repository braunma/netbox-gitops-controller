// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// Phases are the import phases, in the same order and under the same names as
// the sync, so --only means the same thing in both directions.
var Phases = []string{"foundation", "network", "device-types", "devices", "virtualization"}

// Options configures an import run.
type Options struct {
	// Phases selects which phases run; nil or empty means all of them.
	Phases map[string]bool

	// Sites, when non-empty, restricts site-scoped source objects to these
	// slugs. It cannot filter IPAM objects that carry no site (see the report
	// and docs/IMPORT.md), which is why those are counted separately.
	Sites []string
	// Tags / ExcludeTags filter source objects by tag slug.
	Tags        []string
	ExcludeTags []string
	// ManagedOnly restricts the import to objects already carrying the managed
	// gitops tag.
	ManagedOnly bool

	// SplitBy chooses how inventory is partitioned into files: "site" (default),
	// "rack", "role" or "none".
	SplitBy string
	// Defaults controls per-file defaults extraction.
	Defaults defaultsOptions

	Logger *utils.Logger
}

// File is one rendered output file, its path relative to the data directory.
type File struct {
	Path  string
	Bytes []byte
}

// Result is the whole import, rendered in memory. Nothing is written to disk
// until Write is called, which is what lets --dry-run and --diff reuse the
// exact same rendering the real write would produce.
type Result struct {
	Files  []File
	Report *Report
}

// runContext carries the shared state one import run threads through its phases:
// the read-only fetcher, the resolved options, the tag slugs to strip from
// emitted output, and the accumulating report.
type runContext struct {
	f      fetcher
	opts   Options
	strip  map[string]bool // tag slugs never emitted (managed, sandbox)
	report *Report
	files  []File
	log    *utils.Logger
}

// Import reads the live instance and returns the rendered repository. It makes
// no write request of any kind.
func Import(c *client.NetBoxClient, opts Options) (*Result, error) {
	if opts.Logger == nil {
		opts.Logger = utils.NewLogger(false)
	}
	if opts.SplitBy == "" {
		opts.SplitBy = "site"
	}
	if len(opts.Phases) == 0 {
		opts.Phases = map[string]bool{}
		for _, p := range Phases {
			opts.Phases[p] = true
		}
	}

	rc := &runContext{
		f:      fetcher{c},
		opts:   opts,
		strip:  map[string]bool{constants.ManagedTagSlug: true},
		report: newReport(),
		log:    opts.Logger,
	}

	phaseFns := []struct {
		name string
		fn   func(*runContext) error
	}{
		{"foundation", (*runContext).foundation},
	}
	for _, p := range phaseFns {
		if !opts.Phases[p.name] {
			continue
		}
		rc.log.Info("Importing phase: %s", p.name)
		if err := p.fn(rc); err != nil {
			return nil, fmt.Errorf("import %s: %w", p.name, err)
		}
	}

	// Stable file order regardless of the order phases produced them in.
	sort.Slice(rc.files, func(i, j int) bool { return rc.files[i].Path < rc.files[j].Path })

	return &Result{Files: rc.files, Report: rc.report}, nil
}

// emit renders items to a file at the given data-dir-relative path and appends
// it to the run's output. A path is emitted at most once; a second emit to the
// same path is a programming error and fails loudly rather than clobbering.
func (rc *runContext) emit(path, header, listKey string, items []interface{}) error {
	for _, existing := range rc.files {
		if existing.Path == path {
			return fmt.Errorf("internal: %s emitted twice", path)
		}
	}
	b, err := renderFile(header, listKey, items, rc.opts.Defaults)
	if err != nil {
		return fmt.Errorf("render %s: %w", path, err)
	}
	rc.files = append(rc.files, File{Path: path, Bytes: b})
	return nil
}

// keep reports whether a source object passes the tag / managed filters. The
// site filter is applied per phase, since not every kind carries a site.
func (rc *runContext) keep(obj client.Object) bool {
	if rc.opts.ManagedOnly && !hasTag(obj, constants.ManagedTagSlug) {
		return false
	}
	for _, t := range rc.opts.ExcludeTags {
		if hasTag(obj, t) {
			return false
		}
	}
	for _, t := range rc.opts.Tags {
		if !hasTag(obj, t) {
			return false
		}
	}
	return true
}

// Write writes every rendered file under dir. It refuses a non-empty target
// unless force is set: the importer creates a repository, it does not merge
// into a hand-written one.
func (r *Result) Write(dir string, force bool) error {
	if !force {
		if err := ensureEmpty(dir); err != nil {
			return err
		}
	}
	for _, file := range r.Files {
		full := filepath.Join(dir, file.Path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, file.Bytes, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ensureEmpty reports an error if dir exists and contains anything other than
// dotfiles a VCS keeps (a bare .git is fine; tracked YAML is not).
func ensureEmpty(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		return fmt.Errorf("target directory %s is not empty (use --force to write into it)", dir)
	}
	return nil
}

// idOf is GetIDFromObject specialised to client.Object, for local joins.
func idOf(obj client.Object) int { return utils.GetIDFromObject(map[string]interface{}(obj)) }

// hasTag reports whether an object carries a tag with the given slug.
func hasTag(obj client.Object, slug string) bool {
	raw, ok := obj["tags"].([]interface{})
	if !ok {
		return false
	}
	for _, t := range raw {
		if tm, ok := t.(map[string]interface{}); ok {
			if s, _ := tm["slug"].(string); s == slug {
				return true
			}
		}
	}
	return false
}
