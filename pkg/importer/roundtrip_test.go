// SPDX-License-Identifier: Apache-2.0

package importer_test

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/internal/nbtest"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/importer"
	"github.com/braunma/netbox-gitops-controller/pkg/lint"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// TestRoundTripLoadsAndLintsClean is the fast, NetBox-free proof that the
// importer's output is valid native YAML: import a representative instance,
// write it, then load it back through the same loader the controller uses and
// run the linter over it. The output must carry no lint errors and none of the
// redundancy warnings the importer's template diffing exists to avoid.
func TestRoundTripLoadsAndLintsClean(t *testing.T) {
	f, c := nbtest.New(t)

	// A device type with an interface template, so template diffing has
	// something to diff against.
	f.Seed("dcim", "sites", client.Object{"name": "Berlin", "slug": "berlin",
		"status": map[string]interface{}{"value": "active"}})
	f.Seed("dcim", "device-roles", client.Object{"name": "Server", "slug": "server", "color": "00ff00"})
	dt := f.Seed("dcim", "device-types", client.Object{
		"model": "R640", "slug": "r640",
		"manufacturer": map[string]interface{}{"name": "Dell", "slug": "dell"},
		"u_height":     float64(1),
	})
	dtID := dt["id"].(int)
	f.Seed("dcim", "interface-templates", client.Object{
		"name": "eth0", "type": map[string]interface{}{"value": "1000base-t"},
		"device_type": map[string]interface{}{"id": dtID},
	})

	dev := f.Seed("dcim", "devices", client.Object{
		"name": "srv-01", "site": map[string]interface{}{"slug": "berlin", "name": "Berlin"},
		"role":        map[string]interface{}{"slug": "server"},
		"device_type": map[string]interface{}{"slug": "r640", "id": dtID},
		"status":      map[string]interface{}{"value": "active"},
	})
	// eth0 matches the template and carries a primary IP — emitted without a
	// restated type (which would trip redundant-interface-type).
	eth0 := f.Seed("dcim", "interfaces", client.Object{
		"name": "eth0", "type": map[string]interface{}{"value": "1000base-t"},
		"enabled": true, "device": map[string]interface{}{"id": dev["id"].(int)},
	})
	ip := f.Seed("ipam", "ip-addresses", client.Object{
		"address": "10.0.0.5/24", "assigned_object_type": "dcim.interface",
		"assigned_object_id": eth0["id"].(int),
	})
	dev["primary_ip4"] = map[string]interface{}{"id": ip["id"].(int)}

	res, err := importer.Import(c, importer.Options{Defaults: importer.DefaultsOptions(true, 3)})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	dir := t.TempDir()
	if err := res.Write(dir, false); err != nil {
		t.Fatalf("Write: %v", err)
	}

	dl := loader.NewDataLoader(dir, utils.NewLogger(false))
	ds, err := dl.LoadDataset(loader.DatasetOptions{})
	if err != nil {
		t.Fatalf("loading the imported YAML back failed: %v", err)
	}

	findings := lint.Check(ds, lint.Options{})

	var errs, redundant []string
	for _, fnd := range findings {
		if fnd.Severity == lint.Error {
			errs = append(errs, fnd.String())
		}
		if strings.HasPrefix(fnd.Check, "redundant-") {
			redundant = append(redundant, fnd.String())
		}
	}
	if len(errs) > 0 {
		t.Fatalf("imported YAML has lint errors:\n%s", strings.Join(errs, "\n"))
	}
	if len(redundant) > 0 {
		t.Fatalf("imported YAML has redundancy warnings the importer should avoid:\n%s", strings.Join(redundant, "\n"))
	}
}
