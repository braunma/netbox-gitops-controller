// SPDX-License-Identifier: Apache-2.0

// Package facts writes scanned hardware facts onto devices NetBox already
// holds.
//
// It is the one place where something observed reaches NetBox without passing
// through the YAML, and it is deliberately narrow so that it cannot collide
// with the reconciler:
//
//   - it writes custom fields only, and only those under the scan's prefix
//     (`hw_`) that this repository declares for devices. Declaring a field is
//     what switches it on; a fact with no declared field is left out and named;
//   - it never creates a device, and never touches a name, a serial, an asset
//     tag or anything else the YAML declares;
//   - a custom field the YAML sets on a device belongs to the YAML. It is left
//     alone and named, because the next sync would write the YAML value back.
//
// The reconciler, for its part, only enforces the custom fields the YAML sets,
// so what is written here survives every sync.
package facts

import (
	"fmt"
	"sort"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// deviceObjectType is the NetBox content type a custom field must be assigned
// to before it can hold a value on a device.
const deviceObjectType = "dcim.device"

// API is the part of the NetBox client the writer needs.
type API interface {
	Filter(app, endpoint string, filters map[string]interface{}) ([]client.Object, error)
	Update(app, endpoint string, id int, data map[string]interface{}) error
	IsDryRun() bool
}

// Outcome says what happened to one device.
type Outcome string

const (
	// Updated: at least one field differed and was written (or would be, in a
	// dry run).
	Updated Outcome = "updated"
	// Unchanged: NetBox already held every value.
	Unchanged Outcome = "unchanged"
	// Missing: NetBox has no such device yet. The sync creates devices; a scan
	// does not.
	Missing Outcome = "missing"
)

// Change is one field whose value differs.
type Change struct {
	Field string
	Old   interface{}
	New   interface{}
}

// Report is the result for one device.
type Report struct {
	Device  string
	Outcome Outcome
	// Changes lists the fields written, sorted by name.
	Changes []Change
	// YAMLOwned lists scanned fields that were not written because the YAML
	// sets them on this device.
	YAMLOwned []string
}

// Writer writes facts to NetBox.
type Writer struct {
	api    API
	prefix string
	// declared holds the custom fields under the prefix that the repository
	// declares for devices.
	declared map[string]bool
	// undeclared collects the fields a scan reported that are not declared.
	undeclared map[string]bool
}

// NewWriter builds a writer. The custom field definitions are the repository's
// own: only a field declared there, under the prefix and for devices, is
// written.
func NewWriter(api API, definitions []*models.CustomField, prefix string) *Writer {
	declared := make(map[string]bool)
	for _, cf := range definitions {
		if !strings.HasPrefix(cf.Name, prefix) {
			continue
		}
		for _, objectType := range cf.ObjectTypes {
			if objectType == deviceObjectType {
				declared[cf.Name] = true
				break
			}
		}
	}
	return &Writer{api: api, prefix: prefix, declared: declared, undeclared: make(map[string]bool)}
}

// DeclaredFields returns the fields the writer may write, sorted.
func (w *Writer) DeclaredFields() []string {
	return sortedKeys(w.declared)
}

// Undeclared returns the fields a scan reported, across every Write so far,
// that the repository does not declare for devices. They were not written.
func (w *Writer) Undeclared() []string {
	return sortedKeys(w.undeclared)
}

// Write puts the scanned field values onto the declared device. In a dry run
// it reports what it would change and writes nothing.
func (w *Writer) Write(dev *models.DeviceConfig, fields map[string]interface{}) (Report, error) {
	report := Report{Device: dev.Name}

	desired := make(map[string]interface{}, len(fields))
	for name, value := range fields {
		switch {
		case !strings.HasPrefix(name, w.prefix):
			// Not a scan field. Nothing outside the prefix is ever written.
			continue
		case !w.declared[name]:
			w.undeclared[name] = true
		case hasKey(dev.CustomFields, name):
			report.YAMLOwned = append(report.YAMLOwned, name)
		default:
			desired[name] = value
		}
	}
	sort.Strings(report.YAMLOwned)

	found, err := w.api.Filter("dcim", "devices", map[string]interface{}{
		"name": dev.Name,
		"site": dev.SiteSlug,
	})
	if err != nil {
		return report, fmt.Errorf("device %s: looking it up in NetBox: %w", dev.Name, err)
	}
	switch len(found) {
	case 0:
		report.Outcome = Missing
		return report, nil
	case 1:
	default:
		return report, fmt.Errorf("device %s: NetBox holds %d devices of that name in site %s", dev.Name, len(found), dev.SiteSlug)
	}

	existing, _ := found[0]["custom_fields"].(map[string]interface{})
	payload := make(map[string]interface{})
	for _, name := range sortedKeys(desired) {
		if equalValue(existing[name], desired[name]) {
			continue
		}
		payload[name] = desired[name]
		report.Changes = append(report.Changes, Change{Field: name, Old: existing[name], New: desired[name]})
	}
	if len(payload) == 0 {
		report.Outcome = Unchanged
		return report, nil
	}
	report.Outcome = Updated
	if w.api.IsDryRun() {
		return report, nil
	}

	id, ok := objectID(found[0])
	if !ok {
		return report, fmt.Errorf("device %s: NetBox returned it without an id", dev.Name)
	}
	// Only the differing fields are sent. NetBox merges a partial
	// custom_fields object into the stored one, so every other custom field
	// on the device is untouched.
	if err := w.api.Update("dcim", "devices", id, map[string]interface{}{"custom_fields": payload}); err != nil {
		return report, fmt.Errorf("device %s: writing hardware facts: %w", dev.Name, err)
	}
	return report, nil
}

// equalValue compares a value NetBox returned with one about to be sent. JSON
// decoding turns every number into a float64, so an integer fact is compared
// numerically; an unset custom field reads back as nil and equals nothing.
func equalValue(existing, desired interface{}) bool {
	if existing == nil {
		return false
	}
	if a, ok := number(existing); ok {
		if b, ok := number(desired); ok {
			return a == b
		}
		return false
	}
	return existing == desired
}

func number(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func objectID(obj client.Object) (int, bool) {
	n, ok := number(obj["id"])
	return int(n), ok && n > 0
}

func hasKey(m map[string]interface{}, key string) bool {
	_, ok := m[key]
	return ok
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
