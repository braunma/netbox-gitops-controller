// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/changeset"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// scopeFixture is a git repository holding a small data directory, and a
// NetBox that holds nothing but tags.
type scopeFixture struct {
	t    *testing.T
	root string
	// description of the gitops-state tag; nil while the tag does not exist.
	description *string
	writes      []string
	srv         *httptest.Server
}

func newScopeFixture(t *testing.T) *scopeFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &scopeFixture{t: t, root: root}
	// A temp directory inside some other checkout must not be taken for part
	// of it.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))

	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)

	// Every package-level setting the scope reads, restored afterwards.
	old := []any{dataDir, dryRun, fullSync, changedSince, onlyPhases, siteFilter, deviceFilter, vmFilter,
		deviceTypeLibrary, moduleTypeLibrary, ignoredFiles, includeIgnoredFiles}
	t.Cleanup(func() {
		dataDir, dryRun, fullSync, changedSince = old[0].(string), old[1].(bool), old[2].(bool), old[3].(string)
		onlyPhases, siteFilter, deviceFilter, vmFilter = old[4].([]string), old[5].(string), old[6].(string), old[7].(string)
		deviceTypeLibrary, moduleTypeLibrary = old[8].(string), old[9].(string)
		ignoredFiles, includeIgnoredFiles = old[10].([]string), old[11].(bool)
	})
	dataDir, dryRun, fullSync, changedSince = root, false, false, ""
	onlyPhases, siteFilter, deviceFilter, vmFilter = nil, "", "", ""
	deviceTypeLibrary, moduleTypeLibrary, ignoredFiles, includeIgnoredFiles = "", "", nil, false
	t.Setenv("DEVICETYPE_LIBRARY", "")
	t.Setenv("MODULETYPE_LIBRARY", "")
	t.Setenv("IGNORED_FILES", "")

	f.write("definitions/sites/sites.yaml", "- { name: \"Berlin\", slug: \"berlin\" }\n")
	f.write("definitions/roles/roles.yaml", "- { name: \"Server\", slug: \"server\", color: \"4caf50\" }\n")
	f.write("definitions/device_types/server.yaml", "manufacturer: \"Example\"\nmodel: \"Server\"\nslug: \"server\"\nu_height: 1\n")
	f.devices("")
	f.git("init", "-q", "-b", "main")
	f.commit("initial")
	return f
}

// devices writes the inventory: two servers, the first with the given serial.
func (f *scopeFixture) devices(serial string) {
	first := "- { name: \"srv-01\", site: \"berlin\", role: \"server\", device_type: \"server\""
	if serial != "" {
		first += ", serial: \"" + serial + "\""
	}
	f.write("inventory/hardware/active/servers.yaml",
		first+" }\n- { name: \"srv-02\", site: \"berlin\", role: \"server\", device_type: \"server\" }\n")
}

func (f *scopeFixture) write(rel, content string) {
	f.t.Helper()
	path := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *scopeFixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", f.root, "-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *scopeFixture) commit(msg string) string {
	f.t.Helper()
	f.git("add", "-A")
	f.git("commit", "-q", "-m", msg)
	return f.git("rev-parse", "HEAD")
}

func (f *scopeFixture) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !strings.HasPrefix(r.URL.Path, "/api/extras/tags/") {
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case "GET":
		results := []map[string]any{}
		switch r.URL.Query().Get("slug") {
		case "gitops":
			results = append(results, map[string]any{"id": 1, "slug": "gitops"})
		case "gitops-state":
			if f.description != nil {
				results = append(results, map[string]any{"id": 2, "slug": "gitops-state", "description": *f.description})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "next": nil})
	case "POST", "PATCH":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		d, _ := body["description"].(string)
		f.description = &d
		f.writes = append(f.writes, r.Method)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 2})
	}
}

// recorded puts a completed sync at commit on record, under the settings this
// fixture runs with.
func (f *scopeFixture) recorded(commit string) {
	f.t.Helper()
	d := "commit=" + commit + " settings=" + f.settings() + " at=2026-11-28T12:00:00Z (written by netbox-gitops; do not edit)"
	f.description = &d
}

func (f *scopeFixture) settings() string {
	return settingsFingerprint([]string{"definitions", "inventory"})
}

func (f *scopeFixture) client() *client.NetBoxClient {
	f.t.Helper()
	c, err := client.NewClient(f.srv.URL, "test", dryRun)
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

// decide loads the data directory as a sync does and decides its scope.
func (f *scopeFixture) decide() (*syncScope, *client.NetBoxClient, loader.Dataset) {
	f.t.Helper()
	logger := utils.NewDiscardLogger()
	phases, _ := parseOnly(onlyPhases)
	ds, _, err := preflight(logger, phases)
	if err != nil {
		f.t.Fatal(err)
	}
	c := f.client()
	sc, err := decideScope(c, &ds, logger)
	if err != nil {
		f.t.Fatal(err)
	}
	return sc, c, ds
}

func (f *scopeFixture) wantFull(reason string) *syncScope {
	f.t.Helper()
	sc, _, _ := f.decide()
	if !sc.Full || !strings.Contains(sc.Reason, reason) {
		f.t.Fatalf("scope = full:%v %q, want a full sync because %q", sc.Full, sc.Reason, reason)
	}
	return sc
}

func selected(set map[string]bool) []string {
	var out []string
	for _, name := range []string{"srv-01", "srv-02", "srv-03"} {
		if set[name] {
			out = append(out, name)
		}
	}
	return out
}

func TestScopeIsFullWithoutARecord(t *testing.T) {
	f := newScopeFixture(t)
	f.wantFull("no record of a completed sync")
}

func TestScopeIsFullOutsideGit(t *testing.T) {
	f := newScopeFixture(t)
	plain := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(plain))
	if err := os.CopyFS(plain, os.DirFS(f.root)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(plain, ".git")); err != nil {
		t.Fatal(err)
	}
	dataDir = plain

	sc := f.wantFull("not in a git repository")
	// No repository, no bookkeeping: nothing is read from or written to the
	// state tag.
	c := f.client()
	sc.begin(c, utils.NewDiscardLogger())
	sc.record(c, utils.NewDiscardLogger())
	if len(f.writes) != 0 {
		t.Errorf("state written for a data directory outside git: %v", f.writes)
	}

	changedSince = "HEAD"
	ds, _, _ := preflight(utils.NewDiscardLogger(), map[string]bool{})
	if _, err := decideScope(c, &ds, utils.NewDiscardLogger()); err == nil {
		t.Error("--changed-since outside a git repository was accepted")
	}
}

func TestScopeSelectsWhatChangedSinceTheRecord(t *testing.T) {
	f := newScopeFixture(t)
	first := f.git("rev-parse", "HEAD")
	f.devices("ABC")
	f.commit("serial")
	f.recorded(first)

	sc, _, ds := f.decide()
	if sc.Full {
		t.Fatalf("full sync: %s", sc.Reason)
	}
	if sc.Base != first {
		t.Errorf("base = %s, want the recorded commit %s", sc.Base, first)
	}
	if got := selected(sc.Changes.Devices); !reflect.DeepEqual(got, []string{"srv-01"}) {
		t.Fatalf("devices = %v, want srv-01", got)
	}
	e := sc.Changes.Entries[0]
	if e.Why != "changed: serial" || !e.Touched {
		t.Errorf("entry = %+v", e)
	}

	phases, _ := parseOnly(nil)
	sc.narrow(phases, &ds)
	if len(ds.Devices) != 1 || ds.Devices[0].Name != "srv-01" {
		t.Errorf("narrowed devices = %d", len(ds.Devices))
	}
	want := map[string]bool{"foundation": false, "network": false, "device-types": false, "devices": true, "virtualization": false}
	if !reflect.DeepEqual(phases, want) {
		t.Errorf("phases = %v, want %v", phases, want)
	}
	if got := sc.skipReason(); !strings.Contains(got, "nothing in it changed since") {
		t.Errorf("skipReason() = %q", got)
	}

	plan := sc.plan()
	if plan.Mode != "changes" || plan.Base != first || len(plan.Changed) != 1 {
		t.Errorf("plan = %+v", plan)
	}

	var out bytes.Buffer
	old := utils.DefaultOutput()
	utils.SetDefaultOutput(&out)
	defer utils.SetDefaultOutput(old)
	sc.report(utils.NewLogger(true))
	for _, want := range []string{"Scope: changes since " + first[:10], "Reconciling 1 of 2 devices", "device srv-01: changed: serial"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}

func TestScopeWithNothingChanged(t *testing.T) {
	f := newScopeFixture(t)
	f.recorded(f.git("rev-parse", "HEAD"))

	sc, c, ds := f.decide()
	if sc.Full || !sc.Changes.Empty() {
		t.Fatalf("scope = %+v, want an empty change-based sync", sc)
	}
	phases, _ := parseOnly(nil)
	sc.narrow(phases, &ds)
	for phase, selected := range phases {
		if selected {
			t.Errorf("phase %s runs although nothing changed", phase)
		}
	}
	// Nothing applied, same commit: the record is neither withdrawn nor
	// rewritten.
	sc.begin(c, utils.NewDiscardLogger())
	sc.record(c, utils.NewDiscardLogger())
	if len(f.writes) != 0 {
		t.Errorf("state written by a run with nothing to do: %v", f.writes)
	}
}

func TestScopeListsWhatIsNoLongerDeclared(t *testing.T) {
	f := newScopeFixture(t)
	first := f.git("rev-parse", "HEAD")
	f.write("inventory/hardware/active/servers.yaml", "- { name: \"srv-01\", site: \"berlin\", role: \"server\", device_type: \"server\" }\n")
	f.commit("remove srv-02")
	f.recorded(first)

	sc, _, _ := f.decide()
	if sc.Full || !reflect.DeepEqual(sc.Changes.Removed, []string{"device srv-02"}) {
		t.Fatalf("removed = %v (full: %v %s)", sc.Changes.Removed, sc.Full, sc.Reason)
	}
	if got := sc.plan().NoLongerDeclared; !reflect.DeepEqual(got, []string{"device srv-02"}) {
		t.Errorf("plan lists %v", got)
	}
}

func TestScopeIsFullWhenTheRecordCannotBeUsed(t *testing.T) {
	t.Run("interrupted sync", func(t *testing.T) {
		f := newScopeFixture(t)
		d := "sync in progress or interrupted; the next sync is a full one"
		f.description = &d
		f.wantFull("did not finish")
	})
	t.Run("commit not in this clone", func(t *testing.T) {
		f := newScopeFixture(t)
		f.recorded("0123456789abcdef0123456789abcdef01234567")
		f.wantFull("is not in this clone")
	})
	t.Run("other settings", func(t *testing.T) {
		f := newScopeFixture(t)
		f.recorded(f.git("rev-parse", "HEAD"))
		// A file parked then is loaded now: the same commit, another dataset.
		includeIgnoredFiles = true
		f.wantFull("ignore patterns")
	})
	t.Run("--full", func(t *testing.T) {
		f := newScopeFixture(t)
		f.recorded(f.git("rev-parse", "HEAD"))
		fullSync = true
		sc := f.wantFull("--full was given")
		if got := sc.plan(); got.Mode != "full" || got.Reason == "" {
			t.Errorf("plan = %+v", got)
		}
		if got := sc.skipReason(); got != "not selected via --only" {
			t.Errorf("skipReason() = %q", got)
		}
	})
	t.Run("hand-set scope", func(t *testing.T) {
		f := newScopeFixture(t)
		f.recorded(f.git("rev-parse", "HEAD"))
		deviceFilter = "srv-01"
		f.wantFull("--only, --site, --device or --vm")
	})
	t.Run("controller source changed", func(t *testing.T) {
		f := newScopeFixture(t)
		f.write("cmd/tool/main.go", "package main\n")
		first := f.commit("code")
		f.write("cmd/tool/main.go", "package main\n\nfunc main() {}\n")
		f.commit("code change")
		f.recorded(first)
		f.wantFull("the controller's own source changed")
	})
	t.Run("library outside the repository", func(t *testing.T) {
		f := newScopeFixture(t)
		f.recorded(f.git("rev-parse", "HEAD"))
		deviceTypeLibrary = t.TempDir()
		// The fingerprint covers paths inside the repository; the outside
		// library is caught on its own.
		f.wantFull("outside the git repository")
	})
	t.Run("old YAML no longer loads", func(t *testing.T) {
		f := newScopeFixture(t)
		f.write("definitions/racks/racks.yaml", "- { name: \"R1\", slug: \"r1\", sight: \"berlin\" }\n")
		broken := f.commit("a rack with a typo")
		f.write("definitions/racks/racks.yaml", "- { name: \"R1\", slug: \"r1\", site: \"berlin\" }\n")
		f.commit("fixed")
		f.recorded(broken)
		f.wantFull("does not load with this controller")
	})
}

func TestChangedSinceOverridesTheRecord(t *testing.T) {
	f := newScopeFixture(t)
	first := f.git("rev-parse", "HEAD")
	f.devices("ABC")
	f.commit("serial")
	// Nothing on record at all.
	changedSince = "HEAD~1"

	sc, _, _ := f.decide()
	if sc.Full || sc.Base != first || !sc.Changes.Devices["srv-01"] {
		t.Fatalf("scope = full:%v base:%s devices:%v", sc.Full, sc.Base, sc.Changes.Devices)
	}

	changedSince = "no-such-ref"
	ds, _, _ := preflight(utils.NewDiscardLogger(), map[string]bool{})
	if _, err := decideScope(f.client(), &ds, utils.NewDiscardLogger()); err == nil {
		t.Error("--changed-since with an unknown ref was accepted")
	}
}

// A plan for a branch that lacks the commit NetBox is at shows the branch's
// own changes, not the main line's newer ones as reverts.
func TestPlanOnABranchBehindTheRecord(t *testing.T) {
	f := newScopeFixture(t)
	fork := f.git("rev-parse", "HEAD")
	f.git("checkout", "-q", "-b", "feature")
	f.write("inventory/hardware/active/more.yaml", "- { name: \"srv-03\", site: \"berlin\", role: \"server\", device_type: \"server\" }\n")
	f.commit("add srv-03")
	f.git("checkout", "-q", "main")
	f.devices("ABC")
	applied := f.commit("serial, applied to NetBox")
	f.git("checkout", "-q", "feature")
	f.recorded(applied)

	dryRun = true
	sc, _, _ := f.decide()
	if sc.Full {
		t.Fatalf("full sync: %s", sc.Reason)
	}
	if sc.Base != fork || !strings.Contains(sc.BaseNote, "does not contain") {
		t.Errorf("base = %s (%s), want the fork point %s", sc.Base, sc.BaseNote, fork)
	}
	if got := selected(sc.Changes.Devices); !reflect.DeepEqual(got, []string{"srv-03"}) {
		t.Errorf("devices = %v, want the branch's own srv-03 only", got)
	}

	// An apply from that branch is different: NetBox really has to go from
	// the recorded commit to this one, serial revert included.
	dryRun = false
	sc, _, _ = f.decide()
	if sc.Full || sc.Base != applied {
		t.Fatalf("apply scope = full:%v base:%s", sc.Full, sc.Base)
	}
	if got := selected(sc.Changes.Devices); !reflect.DeepEqual(got, []string{"srv-01", "srv-03"}) {
		t.Errorf("devices = %v, want srv-01 (reverted) and srv-03 (new)", got)
	}
}

func TestSyncStateBookkeeping(t *testing.T) {
	logger := utils.NewDiscardLogger()
	setup := func(t *testing.T) (*scopeFixture, string) {
		f := newScopeFixture(t)
		first := f.git("rev-parse", "HEAD")
		f.devices("ABC")
		head := f.commit("serial")
		f.recorded(first)
		return f, head
	}

	t.Run("a completed sync records the new commit", func(t *testing.T) {
		f, head := setup(t)
		sc, c, _ := f.decide()
		sc.begin(c, logger)
		if !strings.Contains(*f.description, "in progress") {
			t.Fatalf("while running, the tag says %q", *f.description)
		}
		sc.record(c, logger)
		if !strings.HasPrefix(*f.description, "commit="+head+" settings="+f.settings()+" ") {
			t.Errorf("after the sync, the tag says %q", *f.description)
		}
	})

	t.Run("a first full sync creates the record", func(t *testing.T) {
		f, head := setup(t)
		f.description = nil
		sc, c, _ := f.decide()
		sc.begin(c, logger)
		sc.record(c, logger)
		if !reflect.DeepEqual(f.writes, []string{"POST"}) || !strings.HasPrefix(*f.description, "commit="+head) {
			t.Errorf("writes = %v, tag = %v", f.writes, f.description)
		}
	})

	t.Run("a dry run touches nothing", func(t *testing.T) {
		f, _ := setup(t)
		dryRun = true
		sc, c, _ := f.decide()
		sc.begin(c, logger)
		sc.record(c, logger)
		if len(f.writes) != 0 {
			t.Errorf("writes = %v", f.writes)
		}
	})

	notRecorded := func(t *testing.T, f *scopeFixture) {
		t.Helper()
		sc, c, _ := f.decide()
		sc.begin(c, logger)
		sc.record(c, logger)
		if !reflect.DeepEqual(f.writes, []string{"PATCH"}) || !strings.Contains(*f.description, "in progress") {
			t.Errorf("writes = %v, tag = %q; want the record withdrawn and not rewritten", f.writes, *f.description)
		}
	}
	t.Run("a hand-scoped run withdraws the record", func(t *testing.T) {
		f, _ := setup(t)
		deviceFilter = "srv-01"
		notRecorded(t, f)
	})
	t.Run("uncommitted data is not recorded", func(t *testing.T) {
		f, _ := setup(t)
		f.devices("NOT-COMMITTED")
		notRecorded(t, f)
	})
	t.Run("skipped objects are not recorded", func(t *testing.T) {
		f, _ := setup(t)
		sc, c, _ := f.decide()
		sc.begin(c, logger)
		c.MarkReconcileIncomplete("dcim", "devices")
		sc.record(c, logger)
		if !strings.Contains(*f.description, "in progress") {
			t.Errorf("tag = %q", *f.description)
		}
	})

	t.Run("a commit that changes no data still advances the record", func(t *testing.T) {
		f := newScopeFixture(t)
		f.recorded(f.git("rev-parse", "HEAD"))
		f.write("README.md", "docs only\n")
		head := f.commit("docs")
		sc, c, _ := f.decide()
		sc.begin(c, logger)
		sc.record(c, logger)
		if !reflect.DeepEqual(f.writes, []string{"PATCH"}) || !strings.HasPrefix(*f.description, "commit="+head) {
			t.Errorf("writes = %v, tag = %q", f.writes, *f.description)
		}
	})
}

func TestWritePlanJSONCarriesTheScope(t *testing.T) {
	var out bytes.Buffer
	scope := planScope{Mode: "changes", Base: "abc", Changed: []changeset.Entry{{Kind: "device", Name: "srv-01", Why: "new"}},
		NoLongerDeclared: []string{"device srv-02"}}
	if err := writePlanJSON(&out, true, scope, client.ChangeSummary{}, nil); err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Scope planScope `json:"scope"`
	}
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Scope, scope) {
		t.Errorf("scope = %+v, want %+v", plan.Scope, scope)
	}
}

func TestShort(t *testing.T) {
	if got := short("0123456789abcdef"); got != "0123456789" {
		t.Errorf("short() = %q", got)
	}
	if got := short("abc"); got != "abc" {
		t.Errorf("short() = %q", got)
	}
}

// A changed device type is reconciled alone, while the devices built from it
// and from the untouched types still see every type's definition.
func TestScopeReconcilesOnlyTheChangedDeviceTypes(t *testing.T) {
	f := newScopeFixture(t)
	f.write("definitions/device_types/switch.yaml", "manufacturer: \"Example\"\nmodel: \"Switch\"\nslug: \"switch\"\nu_height: 1\n")
	first := f.commit("second type")
	f.write("definitions/device_types/switch.yaml", "manufacturer: \"Example\"\nmodel: \"Switch\"\nslug: \"switch\"\nu_height: 2\n")
	f.commit("taller switch")
	f.recorded(first)

	sc, _, ds := f.decide()
	if sc.Full {
		t.Fatalf("full sync: %s", sc.Reason)
	}
	phases, _ := parseOnly(nil)
	sc.narrow(phases, &ds)
	if !phases["device-types"] {
		t.Fatal("the device type phase does not run")
	}
	if len(ds.DeviceTypes) != 2 {
		t.Errorf("the other phases see %d device types, want both", len(ds.DeviceTypes))
	}
	types := sc.typesToReconcile(ds)
	if len(types.DeviceTypes) != 1 || types.DeviceTypes[0].Slug != "switch" {
		t.Errorf("device types to reconcile = %d, want only switch", len(types.DeviceTypes))
	}
	if len(ds.DeviceTypes) != 2 {
		t.Error("typesToReconcile changed the dataset it was given")
	}

	var file string
	for _, e := range sc.Changes.Entries {
		if e.Kind == "device type" && e.Name == "switch" {
			file = e.File
		}
	}
	if file != "definitions/device_types/switch.yaml" {
		t.Errorf("file = %q, want the path from the repository root", file)
	}

	sc.Full = true
	if got := sc.typesToReconcile(ds); len(got.DeviceTypes) != 2 {
		t.Errorf("a full sync reconciles %d device types, want both", len(got.DeviceTypes))
	}
}

func TestDerivedForKeepsTheLinesOfWhatWasPicked(t *testing.T) {
	changes := changeset.Result{
		Groups:  map[string]bool{},
		Devices: map[string]bool{"srv-1": true},
		VMs:     map[string]bool{"vm-a": true},
	}
	lines := []string{
		`Derived: device srv-1 interface idrac: vrf "Mgmt" (from prefix 10.0.0.0/24)`,
		`Derived: device srv-1: site "berlin" (from rack r1)`,
		`Derived: device srv-10 interface idrac: vrf "Mgmt" (from prefix 10.0.0.0/24)`,
		`Derived: virtual machine vm-a interface eth0: vrf "Mgmt" (from prefix 10.0.0.0/24)`,
		`Derived: virtual machine vm-b interface eth0: vrf "Mgmt" (from prefix 10.0.0.0/24)`,
		`Derived: prefix 10.0.0.0/24: site "berlin" (from VLAN mgmt)`,
	}
	got := derivedFor(changes, lines)
	want := []string{lines[0], lines[1], lines[3]}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("derivedFor() = %q, want %q", got, want)
	}

	changes.Groups[changeset.GroupNetwork] = true
	if got := derivedFor(changes, lines); len(got) != 4 || got[3] != lines[5] {
		t.Errorf("with the network group picked: %q", got)
	}
}
