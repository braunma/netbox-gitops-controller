// SPDX-License-Identifier: Apache-2.0

package schema

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The committed schema files are what editors read; they must be what the
// models say. `go run ./cmd/schemagen` rewrites them.
func TestCommittedSchemasAreCurrent(t *testing.T) {
	for _, kind := range Kinds {
		want, err := Generate(kind)
		if err != nil {
			t.Fatalf("%s: %v", kind.Name, err)
		}
		want = append(want, '\n')
		path := filepath.Join("..", "..", "schema", kind.Name+".json")
		have, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run `go run ./cmd/schemagen`)", path, err)
		}
		if !bytes.Equal(have, want) {
			t.Errorf("%s is behind the models; run `go run ./cmd/schemagen`", path)
		}
	}
}

// The device schema knows the short names, the choice values, the grouped
// form and the interface shorthands the loader accepts.
func TestDeviceSchemaShape(t *testing.T) {
	data, err := Generate(Kinds[0])
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	defs := doc["definitions"].(map[string]interface{})
	dev := defs["DeviceConfig"].(map[string]interface{})
	props := dev["properties"].(map[string]interface{})
	for _, key := range []string{"site", "site_slug", "role", "device_type", "rack", "interfaces", "cable_defaults", "custom_fields"} {
		if _, ok := props[key]; !ok {
			t.Errorf("device schema lacks %q", key)
		}
	}
	if face := props["face"].(map[string]interface{}); len(face["enum"].([]interface{})) != 2 {
		t.Errorf("face enum = %v", face["enum"])
	}
	if dev["additionalProperties"] != false {
		t.Error("unknown fields must be rejected, as the loader rejects them")
	}
	if _, ok := defs["InterfaceConfigPerNameDefaults"].(map[string]interface{})["properties"].(map[string]interface{})["always"]; !ok {
		t.Error("defaults.interfaces entries must allow always")
	}
	if len(doc["anyOf"].([]interface{})) != 3 {
		t.Error("a file is a list, the grouped form, or one object")
	}
}

func TestKindForPath(t *testing.T) {
	for path, want := range map[string]string{
		"inventory/hardware/active/servers.yaml":     "device",
		"inventory/discovered/hardware/x.yaml":       "device",
		"inventory/virtual/prod/web.yaml":            "virtual-machine",
		"definitions/virtualization/clusters/c.yaml": "cluster",
		"definitions/device_types/servers/r740.yaml": "device-type",
		"definitions/racks/a.yaml":                   "rack",
	} {
		kind, ok := KindForPath(path)
		if !ok || kind.Name != want {
			t.Errorf("KindForPath(%q) = %q, %v; want %q", path, kind.Name, ok, want)
		}
	}
	if _, ok := KindForPath("README.md"); ok {
		t.Error("a file outside the data folders has no kind")
	}
}
