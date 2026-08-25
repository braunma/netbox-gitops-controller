// SPDX-License-Identifier: Apache-2.0

package importer_test

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/internal/nbtest"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/importer"
)

func TestImportDeviceTypeWithTemplates(t *testing.T) {
	f, c := nbtest.New(t)
	dt := f.Seed("dcim", "device-types", client.Object{
		"model":         "PowerEdge R640",
		"slug":          "poweredge-r640",
		"manufacturer":  map[string]interface{}{"name": "Dell", "slug": "dell"},
		"u_height":      float64(1),
		"is_full_depth": true,
	})
	dtID := int(dt["id"].(int))
	f.Seed("dcim", "interface-templates", client.Object{
		"name": "eth0", "type": map[string]interface{}{"value": "1000base-t"},
		"device_type": map[string]interface{}{"id": dtID},
	})
	f.Seed("dcim", "interface-templates", client.Object{
		"name": "idrac", "type": map[string]interface{}{"value": "1000base-t"},
		"mgmt_only": true, "enabled": false,
		"device_type": map[string]interface{}{"id": dtID},
	})

	res, err := importer.Import(c, importer.Options{
		Phases:   map[string]bool{"device-types": true},
		Defaults: importer.DefaultsOptions(true, 3),
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	file, ok := fileByPath(res, "definitions/device_types/dell/poweredge-r640.yaml")
	if !ok {
		t.Fatalf("device type file not at expected manufacturer path; files: %v", paths(res))
	}
	body := string(file.Bytes)
	for _, want := range []string{
		"model: PowerEdge R640", "manufacturer: Dell", "is_full_depth: true",
		"name: eth0", "name: idrac", "mgmt_only: true", "enabled: false",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("device type file missing %q:\n%s", want, body)
		}
	}
	// The enabled interface (eth0) must NOT carry an explicit enabled: it
	// inherits NetBox's default, keeping the file terse and round-tripping.
	if strings.Count(body, "enabled:") != 1 {
		t.Errorf("expected exactly one explicit enabled (the disabled port):\n%s", body)
	}
}

func paths(res *importer.Result) []string {
	var p []string
	for _, f := range res.Files {
		p = append(p, f.Path)
	}
	return p
}
