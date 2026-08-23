// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
)

// Options configures one ingest run.
type Options struct {
	// DataDir is the repository's data directory; every path in a Plan is
	// relative to the process, already joined with it.
	DataDir string
	// CustomFieldPrefix is the prefix of the custom fields ingest owns.
	// Everything outside it belongs to somebody else and is never touched.
	CustomFieldPrefix string
}

// prefix returns the configured custom field prefix, or the default.
func (o Options) prefix() string {
	if o.CustomFieldPrefix == "" {
		return DefaultCustomFieldPrefix
	}
	return o.CustomFieldPrefix
}

// Action says what a Plan would do to one file.
type Action string

const (
	// ActionUpdate rewrites facts inside a file somebody else wrote.
	ActionUpdate Action = "update"
	// ActionCreate writes a file that does not exist yet.
	ActionCreate Action = "create"
)

// Change is one file the run would write.
type Change struct {
	Action Action `json:"action"`
	// Path is relative to the repository root, as it would appear in a commit.
	Path string `json:"path"`
	// Objects names the objects whose facts changed, for the summary.
	Objects []string `json:"objects,omitempty"`
	// Keys lists the YAML keys written, so a reviewer can see at a glance that
	// nothing outside the whitelist moved.
	Keys []string `json:"keys,omitempty"`
	// Before and After are the whole file; Diff renders them.
	Before string `json:"-"`
	After  string `json:"-"`
}

// Summary counts what a run found, in the shape of the sync's plan line.
type Summary struct {
	// FactsUpdated counts declared objects whose facts changed.
	FactsUpdated int `json:"facts_updated"`
	// NewVMs counts discovered VMs written into a generated file.
	NewVMs int `json:"new_vms"`
	// ParkedDevices counts scanned machines written as parked skeletons.
	ParkedDevices int `json:"parked_devices"`
	// Unchanged counts discovered objects that already read the way the scan
	// found them.
	Unchanged int `json:"unchanged"`
}

// HasChanges reports whether the repository would change.
func (s Summary) HasChanges() bool {
	return s.FactsUpdated > 0 || s.NewVMs > 0 || s.ParkedDevices > 0
}

// Plan is what a run would do. Nothing has been written when it is returned.
type Plan struct {
	Summary Summary  `json:"summary"`
	Changes []Change `json:"changes"`
	// Parked lists the skeleton files the run would write, so the summary can
	// name them: a file nobody notices is a file nobody finishes.
	Parked []string `json:"parked,omitempty"`
}

// BuildPlan resolves a snapshot against the declared inventory and works out
// the file changes it implies. It reads files and writes none.
func BuildPlan(snap *discovery.Snapshot, inv loader.Inventory, opts Options) (*Plan, error) {
	matches, err := Match(snap, inv)
	if err != nil {
		return nil, err
	}

	prefix := opts.prefix()
	plan := &Plan{}

	// Edits are grouped by file so a file with several changed objects is
	// rewritten once and reviewed as one diff.
	editsByFile := map[string][]objectEdit{}
	var unchanged int

	for _, m := range matches.Devices {
		if m.Declared == nil {
			continue
		}
		edit, changed := deviceEdit(m, prefix)
		if !changed {
			unchanged++
			continue
		}
		editsByFile[m.Declared.Prov.File] = append(editsByFile[m.Declared.Prov.File], edit)
	}

	for _, m := range matches.VMs {
		if m.Declared == nil {
			continue
		}
		edit, changed := vmEdit(m)
		if !changed {
			unchanged++
			continue
		}
		editsByFile[m.Declared.Prov.File] = append(editsByFile[m.Declared.Prov.File], edit)
	}

	updates, updatedObjects, err := planUpdates(editsByFile)
	if err != nil {
		return nil, err
	}
	plan.Changes = append(plan.Changes, updates...)
	plan.Summary.FactsUpdated = updatedObjects
	// An object whose file turned out not to need rewriting after all (every
	// fact already read that way) counts as unchanged, not as updated.
	unchanged += countPlannedButUnchanged(editsByFile, updates)

	generated, newVMs, err := planGeneratedVMs(snap, matches, opts)
	if err != nil {
		return nil, err
	}
	plan.Changes = append(plan.Changes, generated...)
	plan.Summary.NewVMs = newVMs

	parked, parkedPaths, err := planParkedDevices(snap, matches, opts)
	if err != nil {
		return nil, err
	}
	plan.Changes = append(plan.Changes, parked...)
	plan.Summary.ParkedDevices = len(parkedPaths)
	plan.Parked = parkedPaths

	plan.Summary.Unchanged = unchanged

	sort.SliceStable(plan.Changes, func(i, j int) bool { return plan.Changes[i].Path < plan.Changes[j].Path })
	return plan, nil
}

// countPlannedButUnchanged counts objects whose file needed no rewrite after
// all, because every fact already read the way the scan found it.
func countPlannedButUnchanged(editsByFile map[string][]objectEdit, written []Change) int {
	rewritten := map[string]bool{}
	for _, change := range written {
		rewritten[change.Path] = true
	}
	var unchanged int
	for file, edits := range editsByFile {
		if !rewritten[file] {
			unchanged += len(edits)
		}
	}
	return unchanged
}

// planUpdates rewrites each file whose declared objects have new facts.
func planUpdates(editsByFile map[string][]objectEdit) ([]Change, int, error) {
	files := make([]string, 0, len(editsByFile))
	for file := range editsByFile {
		files = append(files, file)
	}
	sort.Strings(files)

	var changes []Change
	var objects int
	var errs []error

	for _, file := range files {
		content, err := os.ReadFile(file) // #nosec G304 -- the path came from the loader
		if err != nil {
			errs = append(errs, fmt.Errorf("cannot re-read %s: %w", file, err))
			continue
		}

		edits := editsByFile[file]
		change, err := editFile(file, string(content), edits)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if change == nil {
			continue
		}
		if err := verifyFileChange(change, edits); err != nil {
			errs = append(errs, err)
			continue
		}

		changes = append(changes, Change{
			Action:  ActionUpdate,
			Path:    file,
			Objects: change.Objects,
			Keys:    change.Keys,
			Before:  change.Before,
			After:   change.After,
		})
		objects += len(change.Objects)
	}

	return changes, objects, errors.Join(errs...)
}

// deviceEdit works out which whitelisted keys of a declared device the scan
// disagrees with. A fact equal to what the YAML already says writes nothing,
// which is what makes a re-run on unchanged input produce a zero diff.
func deviceEdit(m DeviceMatch, prefix string) (objectEdit, bool) {
	declared := m.Declared.Object
	edit := objectEdit{
		Label: fmt.Sprintf("device %q", declared.Name),
		Prov:  m.Declared.Prov,
	}

	if serial := strings.TrimSpace(m.Discovered.Serial); serial != "" && serial != declared.Serial {
		edit.Scalars = append(edit.Scalars, keyValue{Key: KeyDeviceSerial, Value: serial})
	}
	if tag := strings.TrimSpace(m.Discovered.ServiceTag); tag != "" && tag != declared.AssetTag {
		edit.Scalars = append(edit.Scalars, keyValue{Key: KeyDeviceAssetTag, Value: tag})
	}

	for _, kv := range hardwareCustomFields(m.Discovered.HW, prefix) {
		if !sameValue(declared.CustomFields[kv.Key], kv.Value) {
			edit.CustomFields = append(edit.CustomFields, kv)
		}
	}

	return edit, len(edit.Scalars)+len(edit.CustomFields) > 0
}

// vmEdit works out which whitelisted keys of a declared VM the scan disagrees
// with.
func vmEdit(m VMMatch) (objectEdit, bool) {
	declared := m.Declared.Object
	found := m.Discovered
	edit := objectEdit{
		Label: fmt.Sprintf("virtual machine %q", declared.Name),
		Prov:  m.Declared.Prov,
	}

	if found.VCPUs > 0 && found.VCPUs != declared.VCPUs {
		edit.Scalars = append(edit.Scalars, keyValue{Key: KeyVMVCPUs, Value: found.VCPUs})
	}
	if found.MemoryMB > 0 && found.MemoryMB != declared.Memory {
		edit.Scalars = append(edit.Scalars, keyValue{Key: KeyVMMemory, Value: found.MemoryMB})
	}
	if found.DiskGB > 0 && found.DiskGB != declared.Disk {
		edit.Scalars = append(edit.Scalars, keyValue{Key: KeyVMDisk, Value: found.DiskGB})
	}
	if found.Status != "" && found.Status != declared.Status {
		edit.Scalars = append(edit.Scalars, keyValue{Key: KeyVMStatus, Value: found.Status})
	}
	if found.VMID > 0 && found.VMID != declared.VMID {
		edit.Scalars = append(edit.Scalars, keyValue{Key: KeyVMVMID, Value: found.VMID})
	}

	return edit, len(edit.Scalars) > 0
}

// sameValue compares a declared YAML value with a discovered fact on what they
// mean rather than on their Go type: the decoder may hand back an int, an
// int64 or a float64 for the same number, and a value quoted in YAML is the
// same fact as one that is not.
func sameValue(declared, found interface{}) bool {
	if declared == nil {
		return false
	}
	return fmt.Sprintf("%v", declared) == fmt.Sprintf("%v", found)
}

// planGeneratedVMs renders the guests no file in this repository declares.
//
// Every VM the repository declares anywhere — including in a parked file, and
// including in a file a previous run generated — has already been matched, so
// it never reaches here. That is what keeps one VM from being declared twice.
func planGeneratedVMs(snap *discovery.Snapshot, matches Matches, opts Options) ([]Change, int, error) {
	byCluster := map[string][]discovery.VM{}
	var count int

	for _, m := range matches.VMs {
		if m.Declared != nil {
			continue
		}
		byCluster[m.Discovered.Cluster] = append(byCluster[m.Discovered.Cluster], m.Discovered)
		count++
	}
	if count == 0 {
		return nil, 0, nil
	}

	clusters := make([]string, 0, len(byCluster))
	for cluster := range byCluster {
		clusters = append(clusters, cluster)
	}
	sort.Strings(clusters)

	changes := make([]Change, 0, len(clusters))
	for _, cluster := range clusters {
		rel := generatedVMPath(snap.Source, cluster)
		full := filepath.Join(opts.DataDir, rel)
		content := generatedVMFile(snap.Source, cluster, byCluster[cluster], cluster == "")

		before, err := readIfExists(full)
		if err != nil {
			return nil, 0, err
		}
		if before == content {
			continue
		}

		action := ActionCreate
		if before != "" {
			action = ActionUpdate
		}
		names := make([]string, 0, len(byCluster[cluster]))
		for _, vm := range byCluster[cluster] {
			names = append(names, vm.Name)
		}
		sort.Strings(names)

		changes = append(changes, Change{
			Action: action, Path: full, Objects: names,
			Keys: VMWritableKeys, Before: before, After: content,
		})
	}

	return changes, count, nil
}

// planParkedDevices renders a skeleton for each scanned machine the repository
// does not declare. One file per machine, parked, because a scan cannot know
// where a machine stands.
func planParkedDevices(snap *discovery.Snapshot, matches Matches, opts Options) ([]Change, []string, error) {
	var changes []Change
	var paths []string
	seen := map[string]bool{}

	for _, m := range matches.Devices {
		if m.Declared != nil {
			continue
		}
		rel := parkedDevicePath(snap.Source, m.Discovered)
		if seen[rel] {
			// Two scanned machines whose only identifiers collide would
			// otherwise overwrite each other's skeleton.
			return nil, nil, fmt.Errorf(
				"two discovered devices would both be written to %s: %s; give them distinct serials or service tags",
				rel, describeDevice(m.Discovered))
		}
		seen[rel] = true

		full := filepath.Join(opts.DataDir, rel)
		content := parkedDeviceFile(snap.Source, m.Discovered, opts.prefix())

		before, err := readIfExists(full)
		if err != nil {
			return nil, nil, err
		}
		paths = append(paths, full)
		if before == content {
			continue
		}

		action := ActionCreate
		if before != "" {
			action = ActionUpdate
		}
		changes = append(changes, Change{
			Action: action, Path: full,
			Objects: []string{deviceSkeletonName(m.Discovered)},
			Before:  before, After: content,
		})
	}

	sort.Strings(paths)
	return changes, paths, nil
}

// readIfExists returns a file's content, or "" when it does not exist.
func readIfExists(path string) (string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- a path this package composed
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// Apply writes a plan to disk. It creates the directories a generated file
// needs and rewrites each changed file whole, so a file is never left half
// written.
//
// It writes YAML, and nothing else. There is no NetBox client here and no way
// to reach one: the only route from a discovered fact to NetBox runs through a
// commit, a merge request and the reconciler.
func Apply(plan *Plan) error {
	var errs []error
	for _, change := range plan.Changes {
		if dir := filepath.Dir(change.Path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				errs = append(errs, fmt.Errorf("cannot create %s: %w", dir, err))
				continue
			}
		}
		if err := os.WriteFile(change.Path, []byte(change.After), 0o600); err != nil {
			errs = append(errs, fmt.Errorf("cannot write %s: %w", change.Path, err))
		}
	}
	return errors.Join(errs...)
}

// Diff renders a change as a unified diff, for --dry-run.
func (c Change) Diff() string {
	return unifiedDiff(c.Path, c.Before, c.After)
}
