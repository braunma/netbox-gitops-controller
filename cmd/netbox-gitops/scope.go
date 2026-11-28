// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/braunma/netbox-gitops-controller/pkg/changeset"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/gitrepo"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// syncScope is the decision a sync makes before it reconciles anything:
// everything the YAML declares (a full sync), or only what changed since the
// commit NetBox was last brought up to date with (a change-based sync).
//
// A change-based sync is the default wherever it can be decided safely. Every
// reason it cannot — no git repository, no record in NetBox, a commit this
// clone does not hold, changed controller code — ends in a full sync and a
// line saying why, never in a guess.
type syncScope struct {
	// Full is true when every declared object is reconciled; Reason says why.
	Full   bool
	Reason string
	// Base is the commit a change-based sync compares against, BaseNote
	// where it came from.
	Base     string
	BaseNote string
	// Changes is what differs between Base and the data directory on disk.
	Changes changeset.Result
	// derived are the values the controller filled in for what was picked.
	derived []string
	// Totals of the head dataset, for "3 of 212 devices".
	devices, vms int

	// repo is the git work tree of the data directory; nil when there is
	// none, in which case no sync state is read or recorded.
	repo *gitrepo.Repo
	// paths are the data folders relative to the work tree.
	paths []string
	// settings fingerprints what decides which files are loaded.
	settings string
	// state is what NetBox had on record when the run started.
	state client.SyncState
	// started is true once the recorded commit was replaced by the
	// in-progress note.
	started bool
}

// restricted reports whether the run's scope was set by hand.
func restricted() bool {
	return len(onlyPhases) > 0 || siteFilter != "" || deviceFilter != "" || vmFilter != ""
}

func short(commit string) string {
	if len(commit) > 10 {
		return commit[:10]
	}
	return commit
}

// decideScope works out whether this run can be change-based. It returns an
// error only for a mistake in the command line (--changed-since naming
// something that is not a commit); everything else becomes a full sync.
func decideScope(c *client.NetBoxClient, head *loader.Dataset, logger *utils.Logger) (*syncScope, error) {
	sc := &syncScope{Full: true, devices: len(head.Devices), vms: len(head.VMs)}

	repo, err := gitrepo.Open(dataDir)
	if err != nil {
		if changedSince != "" {
			return nil, fmt.Errorf("--changed-since needs the data directory to be in a git repository: %w", err)
		}
		// git's own words go into the reason: "not a git repository", git
		// missing from the image, and a checkout owned by another user all end
		// here, and only the message tells them apart.
		sc.Reason = fmt.Sprintf("the data directory is not in a git repository this run can read (%s)", firstLine(err))
		return sc, nil
	}
	sc.repo = repo

	var external []string
	sc.paths, external = dataPaths(repo)
	sc.settings = settingsFingerprint(sc.paths)

	// Read the state even when the run will be a full one: a run that writes
	// must first withdraw the recorded commit.
	state, err := c.ReadSyncState()
	if err != nil {
		logger.Warning("Could not read the sync state from NetBox: %v", err)
	}
	sc.state = state

	switch {
	case fullSync:
		sc.Reason = "--full was given"
		return sc, nil
	case restricted():
		sc.Reason = "the scope was set with --only, --site, --device or --vm"
		return sc, nil
	}

	if changedSince != "" {
		base, err := repo.Resolve(changedSince)
		if err != nil {
			return nil, fmt.Errorf("--changed-since %s: not a commit in this repository: %w", changedSince, err)
		}
		sc.Base, sc.BaseNote = base, "given with --changed-since"
	} else {
		switch {
		case err != nil:
			sc.Reason = "the sync state could not be read from NetBox"
			return sc, nil
		case state.Commit == "" && state.Note != "":
			sc.Reason = "the last sync did not finish, or its scope was limited by hand"
			return sc, nil
		case state.Commit == "":
			sc.Reason = "NetBox holds no record of a completed sync yet"
			return sc, nil
		case state.Settings != sc.settings:
			sc.Reason = "the data directory, ignore patterns or library paths differ from those of the recorded sync"
			return sc, nil
		}
		base, err := repo.Resolve(state.Commit)
		if err != nil {
			sc.Reason = fmt.Sprintf("commit %s, which NetBox was last synced to, is not in this clone (a shallow clone? fetch the full history, e.g. GIT_DEPTH: 0)", short(state.Commit))
			return sc, nil
		}
		sc.Base = base
		sc.BaseNote = "the last completed sync"
		if !state.At.IsZero() {
			sc.BaseNote += " (" + state.At.Format(time.RFC3339) + ")"
		}
	}

	if len(external) > 0 {
		sc.Reason = fmt.Sprintf("%s is outside the git repository, so what it held at the last sync is unknown", strings.Join(external, " and "))
		return sc, nil
	}

	// A plan for a branch that does not contain the commit NetBox is at (a
	// merge request opened before the latest apply) is a plan of what the
	// branch itself changes, from where it left the main line. Comparing it
	// against the newer commit would show every change it merely lacks as a
	// revert.
	if dryRun && changedSince == "" && !repo.IsAncestor(sc.Base, repo.Head) {
		mergeBase, err := repo.MergeBase(sc.Base, repo.Head)
		if err != nil {
			sc.Reason = fmt.Sprintf("commit %s, which NetBox was last synced to, shares no history with the checked-out commit", short(sc.Base))
			return sc, nil
		}
		sc.BaseNote = fmt.Sprintf("where this branch left the main line; NetBox itself is at %s, which this branch does not contain, and what changed in between is not part of this plan", short(sc.Base))
		sc.Base = mergeBase
	}

	// The controller's own code is part of what a sync does. Where it lives
	// in the same repository as the data, a change to it since the base means
	// unchanged YAML may now be applied differently.
	code, err := repo.ChangedFiles(sc.Base, []string{"*.go", "go.mod", "go.sum"})
	if err != nil {
		sc.Reason = fmt.Sprintf("git could not compare against %s: %v", short(sc.Base), err)
		return sc, nil
	}
	if len(code) > 0 {
		sc.Reason = fmt.Sprintf("the controller's own source changed since %s (%d file(s), e.g. %s)", short(sc.Base), len(code), code[0])
		return sc, nil
	}

	base, err := loadBase(repo, sc.Base, sc.paths)
	if err != nil {
		sc.Reason = fmt.Sprintf("the YAML at %s does not load with this controller: %v", short(sc.Base), firstLine(err))
		return sc, nil
	}

	changedFiles, err := repo.ChangedFiles(sc.Base, sc.paths)
	if err != nil {
		sc.Reason = fmt.Sprintf("git could not compare against %s: %v", short(sc.Base), err)
		return sc, nil
	}
	sc.Changes = changeset.Compare(&base, head, touchedOrigins(repo, head, changedFiles))
	sc.derived = derivedFor(sc.Changes, head.Derivations)
	// Files are named the way a merge request names them: from the root of
	// the repository, not from wherever this checkout happens to live.
	for i := range sc.Changes.Entries {
		if rel, ok := repo.Rel(sc.Changes.Entries[i].File); ok {
			sc.Changes.Entries[i].File = rel
		}
	}
	sc.Full = false
	return sc, nil
}

// derivedFor returns the "Derived: ..." lines that concern what a
// change-based sync picked: a value the controller filled in is said out loud
// for every object it is about to write, and not repeated for the rest.
func derivedFor(changes changeset.Result, lines []string) []string {
	var out []string
	for _, line := range lines {
		rest, ok := strings.CutPrefix(line, "Derived: ")
		if !ok {
			continue
		}
		keep := false
		switch {
		case strings.HasPrefix(rest, "device "):
			keep = namedIn(strings.TrimPrefix(rest, "device "), changes.Devices)
		case strings.HasPrefix(rest, "virtual machine "):
			keep = namedIn(strings.TrimPrefix(rest, "virtual machine "), changes.VMs)
		default:
			// Prefixes and VLANs: their group runs whole or not at all.
			keep = changes.Groups[changeset.GroupNetwork]
		}
		if keep {
			out = append(out, line)
		}
	}
	return out
}

// namedIn reports whether rest starts with one of the names, followed by the
// end of the name (":" or " ").
func namedIn(rest string, names map[string]bool) bool {
	for name := range names {
		if after, ok := strings.CutPrefix(rest, name); ok && (strings.HasPrefix(after, ":") || strings.HasPrefix(after, " ")) {
			return true
		}
	}
	return false
}

func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}

// dataPaths returns the folders the loader reads, relative to the work tree:
// definitions/ and inventory/ of the data directory and the two type
// libraries. A library that exists outside the work tree cannot be read at an
// earlier commit; it is returned in external.
func dataPaths(repo *gitrepo.Repo) (paths, external []string) {
	for _, dir := range []string{filepath.Join(dataDir, "definitions"), filepath.Join(dataDir, "inventory")} {
		if rel, ok := repo.Rel(dir); ok {
			paths = append(paths, rel)
		}
	}
	for _, lib := range []string{resolveDeviceTypeLibrary(), resolveModuleTypeLibrary()} {
		rel, ok := repo.Rel(lib)
		if !ok {
			if _, err := os.Stat(lib); err == nil {
				external = append(external, lib)
			}
			continue
		}
		covered := false
		for _, p := range paths {
			if rel == p || strings.HasPrefix(rel, p+"/") {
				covered = true
			}
		}
		if !covered {
			paths = append(paths, rel)
		}
	}
	return paths, external
}

// settingsFingerprint is a short hash of what decides which files a load
// reads. Two runs with different fingerprints do not load comparable
// datasets from the same commit.
func settingsFingerprint(paths []string) string {
	patterns, _ := resolveIgnorePatterns()
	sorted := append([]string(nil), patterns...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(paths, "\x00") + "\x01" + strings.Join(sorted, "\x00")))
	return hex.EncodeToString(sum[:6])
}

// loadBase loads the data directory as it was at commit, with the settings of
// this run, from a temporary copy that is removed again.
func loadBase(repo *gitrepo.Repo, commit string, paths []string) (loader.Dataset, error) {
	tmp, err := os.MkdirTemp("", "netbox-gitops-base-")
	if err != nil {
		return loader.Dataset{}, err
	}
	defer os.RemoveAll(tmp)

	if err := repo.Extract(commit, paths, tmp); err != nil {
		return loader.Dataset{}, err
	}
	at := func(path string) string {
		rel, ok := repo.Rel(path)
		if !ok {
			// Outside the work tree and absent (decideScope has ruled out an
			// existing one): there is nothing to load either way.
			return filepath.Join(tmp, ".absent")
		}
		return filepath.Join(tmp, filepath.FromSlash(rel))
	}

	patterns, err := resolveIgnorePatterns()
	if err != nil {
		return loader.Dataset{}, err
	}
	dl := loader.NewDataLoader(at(dataDir), utils.NewDiscardLogger())
	dl.SetIgnorePatterns(patterns)
	return dl.LoadDataset(loader.DatasetOptions{
		DeviceTypeLibrary: at(resolveDeviceTypeLibrary()),
		ModuleTypeLibrary: at(resolveModuleTypeLibrary()),
	})
}

// touchedOrigins maps the changed files git reported (relative to the work
// tree) onto the file names the loader recorded as origins.
func touchedOrigins(repo *gitrepo.Repo, head *loader.Dataset, changedFiles []string) map[string]bool {
	changed := make(map[string]bool, len(changedFiles))
	for _, f := range changedFiles {
		changed[f] = true
	}
	touched := map[string]bool{}
	checked := map[string]bool{}
	for _, origin := range head.Origins {
		if checked[origin.File] {
			continue
		}
		checked[origin.File] = true
		if rel, ok := repo.Rel(origin.File); ok && changed[rel] {
			touched[origin.File] = true
		}
	}
	return touched
}

// report prints the decision and, for a change-based sync, every object it
// selected and why — the part of the plan that lets a reader check the
// selection instead of trusting it.
func (sc *syncScope) report(logger *utils.Logger) {
	logger.Info("═══════════════════════════════════════════════════════")
	if sc.Full {
		logger.Info("Scope: full sync — %s", sc.Reason)
		logger.Info("═══════════════════════════════════════════════════════")
		return
	}

	logger.Info("Scope: changes since %s — %s", short(sc.Base), sc.BaseNote)
	logger.Info("  Everything else is left as it is: an edit made in NetBox by hand is neither seen nor reverted. --full reconciles everything.")
	if sc.Changes.Empty() {
		logger.Info("  Nothing declared was added or changed.")
	} else {
		var groups []string
		for _, g := range []string{changeset.GroupFoundation, changeset.GroupNetwork, changeset.GroupDeviceTypes, changeset.GroupClusters} {
			if sc.Changes.Groups[g] {
				groups = append(groups, g)
			}
		}
		definitions := "none"
		if len(groups) > 0 {
			definitions = strings.Join(groups, ", ")
		}
		logger.Info("  Reconciling %d of %d devices, %d of %d VMs; definition groups: %s",
			len(sc.Changes.Devices), sc.devices, len(sc.Changes.VMs), sc.vms, definitions)
		for _, e := range sc.Changes.Entries {
			where := ""
			switch {
			case e.File == "":
			case e.Touched:
				where = " [" + e.File + "]"
			case strings.HasPrefix(e.Why, "changed"), e.Why == "new":
				// Its own file is as it was: the difference was resolved
				// into it from elsewhere.
				where = " [" + e.File + " itself is untouched: a defaults block, a derived value or a cable path changed its result]"
			}
			logger.Info("  • %s %s: %s%s", e.Kind, e.Name, e.Why, where)
		}
		// With --verbose every derived value was already printed on load.
		if !utils.Verbose() {
			for _, line := range sc.derived {
				logger.Info("  %s", line)
			}
		}
	}

	if len(sc.Changes.Removed) > 0 {
		logger.Warning("  No longer declared since %s (%d). A change-based sync deletes nothing; the next full sync with --prune removes what the controller manages of these:",
			short(sc.Base), len(sc.Changes.Removed))
		for _, r := range sc.Changes.Removed {
			logger.Warning("    ✗ %s", r)
		}
	}
	logger.Info("═══════════════════════════════════════════════════════")
}

// narrow reduces the phases and the dataset of a change-based sync to what
// changed. Definition groups run whole or not at all, exactly as --only runs
// them; devices and VMs are picked one by one, as --device and --vm pick
// them. Both mechanisms resolve what they skip from NetBox.
func (sc *syncScope) narrow(phases map[string]bool, ds *loader.Dataset) {
	if sc.Full {
		return
	}
	for _, group := range []string{changeset.GroupFoundation, changeset.GroupNetwork, changeset.GroupDeviceTypes} {
		phases[group] = phases[group] && sc.Changes.Groups[group]
	}

	var devices []*models.DeviceConfig
	for _, d := range ds.Devices {
		if sc.Changes.Devices[d.Name] {
			devices = append(devices, d)
		}
	}
	ds.Devices = devices
	phases[loader.PhaseDevices] = phases[loader.PhaseDevices] && len(devices) > 0

	var vms []*models.VMConfig
	for _, vm := range ds.VMs {
		if sc.Changes.VMs[vm.Name] {
			vms = append(vms, vm)
		}
	}
	ds.VMs = vms
	// The cluster definitions stay in the dataset whenever the phase runs:
	// they are few, and a VM without a site of its own takes its site caches
	// from its cluster.
	phases[loader.PhaseVirtualization] = phases[loader.PhaseVirtualization] &&
		(sc.Changes.Groups[changeset.GroupClusters] || len(vms) > 0)
}

// typesToReconcile returns the dataset the device type phase works on. Device
// types and module types are many and each has dozens of port templates, so a
// change-based sync reconciles only the new and changed ones. The dataset the
// other phases see keeps all of them: a device is built from its type's
// definition whether or not that type changed.
func (sc *syncScope) typesToReconcile(ds loader.Dataset) loader.Dataset {
	if sc.Full {
		return ds
	}
	changed := map[string]bool{}
	for _, e := range sc.Changes.Entries {
		changed[e.Kind+"/"+e.Name] = true
	}
	var deviceTypes []*models.DeviceType
	for _, dt := range ds.DeviceTypes {
		if changed["device type/"+dt.Slug] {
			deviceTypes = append(deviceTypes, dt)
		}
	}
	ds.DeviceTypes = deviceTypes
	var moduleTypes []*models.ModuleType
	for _, mt := range ds.ModuleTypes {
		key := mt.Slug
		if key == "" {
			key = mt.Model
		}
		if changed["module type/"+key] {
			moduleTypes = append(moduleTypes, mt)
		}
	}
	ds.ModuleTypes = moduleTypes
	return ds
}

// skipReason says why a phase does not run, for the log.
func (sc *syncScope) skipReason() string {
	if sc.Full {
		return "not selected via --only"
	}
	return "nothing in it changed since " + short(sc.Base)
}

// begin withdraws the recorded commit before a run that writes to NetBox, so
// that a run which fails halfway leaves no claim that NetBox matches a
// commit. A change-based run with nothing to apply writes nothing and leaves
// the record alone.
func (sc *syncScope) begin(c *client.NetBoxClient, logger *utils.Logger) {
	if dryRun || sc.repo == nil || sc.state.Commit == "" {
		return
	}
	if !sc.Full && sc.Changes.Empty() {
		return
	}
	if err := c.MarkSyncStarted(); err != nil {
		// Without this the record could outlive a failed run. Say so; the
		// run itself is still correct.
		logger.Warning("Could not mark the sync as started in NetBox (%v). If this run fails, run the next one with --full.", err)
		return
	}
	sc.started = true
}

// record notes in NetBox that it now matches the checked-out commit — when
// that is true: the run covered everything, skipped nothing, and the data on
// disk is exactly what the commit holds.
func (sc *syncScope) record(c *client.NetBoxClient, logger *utils.Logger) {
	if dryRun || sc.repo == nil {
		return
	}
	unrecorded := func(why string) {
		if sc.started {
			logger.Info("Sync state not recorded: %s. The next unrestricted sync is a full one.", why)
		}
	}

	if restricted() {
		unrecorded("a run limited with --only, --site, --device or --vm does not bring NetBox as a whole to a commit")
		return
	}
	if c.ReconcileIncomplete() {
		unrecorded("some declared objects were skipped (see the warnings above)")
		return
	}
	dirty, err := sc.repo.Dirty(sc.paths)
	if err != nil {
		unrecorded(fmt.Sprintf("git could not tell whether the data directory matches the commit (%v)", err))
		return
	}
	if len(dirty) > 0 {
		unrecorded(fmt.Sprintf("the data directory has uncommitted changes (%d file(s), e.g. %s)", len(dirty), dirty[0]))
		return
	}
	if !sc.started && sc.state.Commit == sc.repo.Head && sc.state.Settings == sc.settings {
		return
	}

	err = c.WriteSyncState(client.SyncState{Commit: sc.repo.Head, Settings: sc.settings, At: time.Now()})
	if err != nil {
		logger.Warning("Could not record the sync state in NetBox (%v); the next sync is a full one.", err)
		return
	}
	logger.Info("Recorded in NetBox (tag gitops-state): synced to commit %s", short(sc.repo.Head))
}

// planScope is the scope as it appears in the JSON plan.
type planScope struct {
	Mode    string            `json:"mode"` // "full" or "changes"
	Reason  string            `json:"reason,omitempty"`
	Base    string            `json:"base,omitempty"`
	Changed []changeset.Entry `json:"changed,omitempty"`
	// NoLongerDeclared is what a change-based sync leaves for the next
	// pruning full sync.
	NoLongerDeclared []string `json:"no_longer_declared,omitempty"`
}

func (sc *syncScope) plan() planScope {
	if sc.Full {
		return planScope{Mode: "full", Reason: sc.Reason}
	}
	return planScope{Mode: "changes", Base: sc.Base, Changed: sc.Changes.Entries, NoLongerDeclared: sc.Changes.Removed}
}
