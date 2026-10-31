// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"reflect"
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
			if strings.Contains(line, w.file+": "+w.entry+": "+w.object+": ") && strings.Contains(line, w.text) {
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
