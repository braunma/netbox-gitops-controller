// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// TestEveryProblemIsReportedOnItsOwnLine loads a directory with mistakes in
// several entries and files — several in one entry, a validation error with
// several missing fields — and requires one self-contained line per mistake:
// each names its file, its entry and its object.
func TestEveryProblemIsReportedOnItsOwnLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "devices", "a.yaml", `
- name: "srv-01"
  site: "s"
  role: "r"
  device_type: "dt"
  serail: "X"
  interfaces:
    - { name: "eth0", ip: { address: "10.0.0.1/24", vfr: "P" } }
- name: "srv-02"
`)
	writeFile(t, dir, "devices", "b.yaml", `
- { name: "srv-03", site: "s", site_slug: "s", role: "r", device_type: "dt" }
`)

	_, err := NewDataLoader(dir, utils.NewLogger(false)).LoadDevices("devices")
	if err == nil {
		t.Fatal("LoadDevices() = nil, want the problems reported")
	}
	got := Problems(err)
	want := []struct{ file, entry, object, text string }{
		{"a.yaml", "entry 1", `device "srv-01"`, `interfaces[1].ip: unknown field "vfr"`},
		{"a.yaml", "entry 1", `device "srv-01"`, `unknown field "serail" (did you mean "serial"?)`},
		{"a.yaml", "entry 2", `device "srv-02"`, "site is required"},
		{"a.yaml", "entry 2", `device "srv-02"`, "device_type is required"},
		{"a.yaml", "entry 2", `device "srv-02"`, "role is required"},
		{"b.yaml", "entry 1", `device "srv-03"`, `both "site" and "site_slug" are set`},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d problems, want %d:\n%v", len(got), len(want), err)
	}
	for _, w := range want {
		found := false
		for _, p := range got {
			line := p.Error()
			if strings.Contains(line, "\n") {
				t.Errorf("problem spans several lines: %q", line)
			}
			if entryLine(w.file, w.entry, w.object).MatchString(line) && strings.Contains(line, w.text) {
				found = true
			}
		}
		if !found {
			t.Errorf("no line for %s %s %s %q in:\n%v", w.file, w.entry, w.object, w.text, err)
		}
	}
	if strings.Count(err.Error(), "a.yaml") != 5 {
		t.Errorf("the file should be named once per problem, not repeated:\n%v", err)
	}
}

// TestDecoderTypeErrorsAreOnePerLine verifies that a value of the wrong type
// — which the YAML decoder reports for every such field of an entry in one
// multi-line error — is split into one prefixed line per field, naming the
// field by its path rather than by a line of the re-serialized entry.
func TestDecoderTypeErrorsAreOnePerLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "devices", "a.yaml", `
- name: "srv-01"
  site: "s"
  role: "r"
  device_type: "dt"
  position: "top"
  interfaces:
    - name: "eth0"
      mtu: "big"
`)
	_, err := NewDataLoader(dir, utils.NewLogger(false)).LoadDevices("devices")
	if err == nil {
		t.Fatal("LoadDevices() = nil, want the type errors reported")
	}
	got := Problems(err)
	if len(got) != 2 {
		t.Fatalf("got %d problems, want one per field:\n%v", len(got), err)
	}
	for _, p := range got {
		line := p.Error()
		if strings.Contains(line, "\n") {
			t.Errorf("problem spans several lines: %q", line)
		}
		if !entryLine("a.yaml", "entry 1", `device "srv-01"`).MatchString(line) {
			t.Errorf("problem lacks its file, entry and object: %q", line)
		}
	}
	for _, want := range []string{`: position: cannot unmarshal`, `: interfaces[1].mtu: cannot unmarshal`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("no line naming %q in:\n%v", want, err)
		}
	}
}

// TestKindOfMatchesValidation keeps the loader's object labels and the
// models' validation labels the same word for every kind.
func TestKindOfMatchesValidation(t *testing.T) {
	for _, kind := range loadedKinds {
		v := reflect.New(kind).Interface().(models.Validator)
		err := v.Validate()
		if err == nil {
			t.Errorf("%s: an empty object validated; cannot compare labels", kind.Name())
			continue
		}
		want := models.KindOf(v) + ` "<unnamed>": `
		for _, p := range Problems(err) {
			if !strings.HasPrefix(p.Error(), want) {
				t.Errorf("%s: validation says %q, KindOf says %q", kind.Name(), p.Error(), want)
			}
		}
	}
}

// TestLoadDatasetPhases verifies that a dataset restricted to some phases
// reads only their folders: a problem in an unselected phase's file is not
// reported, and its kinds stay empty, while every kind is read with no
// restriction.
func TestLoadDatasetPhases(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "definitions/sites", "sites.yaml", `
- { name: "Berlin", slug: "berlin" }
`)
	writeFile(t, dir, "definitions/vlan_groups", "groups.yaml", `
- { name: "Berlin VLANs", slug: "berlin-vlans", site: "berlin" }
`)
	writeFile(t, dir, "inventory/hardware/active", "servers.yaml", `
- { name: "srv-01", sight: "berlin", role: "r", device_type: "dt" }
`)
	dl := NewDataLoader(dir, utils.NewLogger(false))

	ds, err := dl.LoadDataset(DatasetOptions{Phases: map[string]bool{PhaseFoundation: true, PhaseNetwork: true}})
	if err != nil {
		t.Fatalf("LoadDataset(foundation, network) error = %v; the device inventory is not part of those phases", err)
	}
	if len(ds.Sites) != 1 || len(ds.VLANGroups) != 1 || len(ds.Devices) != 0 {
		t.Errorf("sites=%d vlan groups=%d devices=%d, want 1, 1, 0", len(ds.Sites), len(ds.VLANGroups), len(ds.Devices))
	}

	if _, err := dl.LoadDataset(DatasetOptions{}); err == nil || !strings.Contains(err.Error(), `unknown field "sight"`) {
		t.Errorf("LoadDataset() with every phase = %v, want the inventory's problem", err)
	}
}

// entryLine matches the prefix every problem of an entry carries: the file,
// the line the entry starts on, the entry number and the object.
func entryLine(file, entry, object string) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(file) + `:\d+: ` + regexp.QuoteMeta(entry+": "+object+": "))
}
