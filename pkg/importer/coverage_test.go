// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"reflect"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// coverageStructs pairs each struct name in the fieldCoverage table with an
// instance to reflect over. Every model the importer writes belongs here.
var coverageStructs = map[string]interface{}{
	"Site":              models.Site{},
	"Rack":              models.Rack{},
	"Role":              models.Role{},
	"Tag":               models.Tag{},
	"Platform":          models.Platform{},
	"TenantGroup":       models.TenantGroup{},
	"Tenant":            models.Tenant{},
	"CustomField":       models.CustomField{},
	"VRF":               models.VRF{},
	"VLAN":              models.VLAN{},
	"VLANGroup":         models.VLANGroup{},
	"Prefix":            models.Prefix{},
	"DeviceType":        models.DeviceType{},
	"ModuleType":        models.ModuleType{},
	"ClusterType":       models.ClusterType{},
	"ClusterGroup":      models.ClusterGroup{},
	"Cluster":           models.Cluster{},
	"DeviceConfig":      models.DeviceConfig{},
	"InterfaceConfig":   models.InterfaceConfig{},
	"VMConfig":          models.VMConfig{},
	"VMInterfaceConfig": models.VMInterfaceConfig{},
}

// yamlFieldNames returns the yaml field names of a struct (skipping "-").
func yamlFieldNames(v interface{}) []string {
	tp := reflect.TypeOf(v)
	var names []string
	for i := 0; i < tp.NumField(); i++ {
		tag := tp.Field(i).Tag.Get("yaml")
		if tag == "" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		names = append(names, name)
	}
	return names
}

// TestFieldCoverageIsComplete fails closed: every yaml field of every model the
// importer writes must have a rule in fieldCoverage, and the table must carry
// no stale entries. A field added to a model next year cannot quietly get the
// wrong round-trip rule — this test forces a deliberate choice for it.
func TestFieldCoverageIsComplete(t *testing.T) {
	for name, sample := range coverageStructs {
		rules, ok := fieldCoverage[name]
		if !ok {
			t.Errorf("struct %s has no coverage table entry", name)
			continue
		}
		fields := yamlFieldNames(sample)
		seen := map[string]bool{}
		for _, f := range fields {
			seen[f] = true
			if _, ok := rules[f]; !ok {
				t.Errorf("%s.%s has no round-trip rule in fieldCoverage — add one (a new field must not silently get the wrong default)", name, f)
			}
		}
		for f := range rules {
			if !seen[f] {
				t.Errorf("fieldCoverage[%s] has a stale entry %q not present on the struct", name, f)
			}
		}
	}
	// Guard the other direction too: no coverage entry for a struct that is not
	// enumerated for reflection (which would go unchecked).
	for name := range fieldCoverage {
		if _, ok := coverageStructs[name]; !ok {
			t.Errorf("fieldCoverage has %q but coverageStructs does not enumerate it, so it is unchecked", name)
		}
	}
}
