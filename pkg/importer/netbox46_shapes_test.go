// SPDX-License-Identifier: Apache-2.0

package importer_test

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/internal/nbtest"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/importer"
)

// These pin two shapes real NetBox 4.6 returns that an earlier fake did not,
// each of which produced YAML that failed `yamlcheck --strict` against a live
// instance:
//
//   - a module type has no slug (its identity is manufacturer + model), and a
//     module's nested module_type carries `model`, not `slug`;
//   - a front port's rear port is given as a `rear_ports` list carrying the
//     rear port's id, not a nested object with a name.

func TestImportModuleTypeSlugSynthesisedFromModel(t *testing.T) {
	f, c := nbtest.New(t)
	mfg := map[string]interface{}{"name": "Dell", "slug": "dell"}

	// A module type with NO slug, as NetBox returns it.
	mt := f.Seed("dcim", "module-types", client.Object{
		"model": "Dell OCP 25GbE Mezz", "manufacturer": mfg,
	})
	// A device carrying a module whose nested module_type has model, not slug.
	f.Seed("dcim", "sites", client.Object{"name": "S", "slug": "s"})
	dev := f.Seed("dcim", "devices", client.Object{
		"name": "srv-1", "site": map[string]interface{}{"slug": "s", "name": "S"},
		"role": map[string]interface{}{"slug": "server"}, "device_type": map[string]interface{}{"slug": "r640"},
	})
	f.Seed("dcim", "modules", client.Object{
		"device":      map[string]interface{}{"id": dev["id"]},
		"module_bay":  map[string]interface{}{"name": "OCP-1"},
		"module_type": map[string]interface{}{"id": mt["id"], "model": "Dell OCP 25GbE Mezz"},
	})

	res, err := importer.Import(c, importer.Options{
		Phases: map[string]bool{"device-types": true, "devices": true}, SplitBy: "none",
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	wantSlug := "dell-ocp-25gbe-mezz"
	mtFile, _ := fileByPath(res, "definitions/module_types/dell.yaml")
	if !strings.Contains(string(mtFile.Bytes), "slug: "+wantSlug) {
		t.Errorf("module type definition missing synthesised slug %q:\n%s", wantSlug, mtFile.Bytes)
	}
	devFile, _ := fileByPath(res, "inventory/hardware/active/devices.yaml")
	if !strings.Contains(string(devFile.Bytes), "module_type_slug: "+wantSlug) {
		t.Errorf("module reference missing slug %q (was empty → --strict rejects it):\n%s", wantSlug, devFile.Bytes)
	}
}

func TestImportFrontPortResolvesRearPortName(t *testing.T) {
	f, c := nbtest.New(t)
	f.Seed("dcim", "sites", client.Object{"name": "S", "slug": "s"})
	panel := f.Seed("dcim", "devices", client.Object{
		"name": "panel-1", "site": map[string]interface{}{"slug": "s", "name": "S"},
		"role": map[string]interface{}{"slug": "patch"}, "device_type": map[string]interface{}{"slug": "pp"},
	})
	peer := f.Seed("dcim", "devices", client.Object{
		"name": "sw-1", "site": map[string]interface{}{"slug": "s", "name": "S"},
		"role": map[string]interface{}{"slug": "switch"}, "device_type": map[string]interface{}{"slug": "sw"},
	})
	peerIf := f.Seed("dcim", "interfaces", client.Object{"name": "Eth1",
		"device": map[string]interface{}{"id": peer["id"], "name": "sw-1"}})

	rear := f.Seed("dcim", "rear-ports", client.Object{"name": "R1",
		"type":   map[string]interface{}{"value": "8p8c"},
		"device": map[string]interface{}{"id": panel["id"], "name": "panel-1"}})
	// NetBox 4.6 front-port shape: rear_ports is a list carrying the rear port id.
	front := f.Seed("dcim", "front-ports", client.Object{"name": "F1",
		"type":   map[string]interface{}{"value": "8p8c"},
		"device": map[string]interface{}{"id": panel["id"], "name": "panel-1"},
		"rear_ports": []interface{}{map[string]interface{}{
			"rear_port": rear["id"], "rear_port_position": float64(1), "position": float64(1)}},
	})
	// A cable so the front port is emitted at all.
	f.Seed("dcim", "cables", client.Object{
		"type":           map[string]interface{}{"value": "cat6a"},
		"a_terminations": []interface{}{map[string]interface{}{"object_type": "dcim.frontport", "object_id": front["id"]}},
		"b_terminations": []interface{}{map[string]interface{}{"object_type": "dcim.interface", "object_id": peerIf["id"]}},
	})

	res, err := importer.Import(c, importer.Options{
		Phases: map[string]bool{"devices": true}, SplitBy: "none",
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	// The front port must carry the resolved rear port NAME, not an empty
	// rear_port (which --strict rejects as "rear_port is required").
	file, _ := fileByPath(res, "inventory/hardware/passive/devices.yaml")
	body := string(file.Bytes)
	if !strings.Contains(body, "name: F1") || !strings.Contains(body, "rear_port: R1") {
		t.Errorf("front port did not resolve its rear port name:\n%s", body)
	}
}
