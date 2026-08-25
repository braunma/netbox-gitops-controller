// SPDX-License-Identifier: Apache-2.0

package importer_test

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/internal/nbtest"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/importer"
)

func TestImportNetwork(t *testing.T) {
	f, c := nbtest.New(t)
	f.Seed("dcim", "sites", client.Object{"name": "Berlin", "slug": "berlin",
		"status": map[string]interface{}{"value": "active"}})

	f.Seed("ipam", "vrfs", client.Object{"name": "Production", "rd": "65000:1",
		"enforce_unique": true})

	// VLAN with a site: exported. VLAN without a site: skipped and reported.
	f.Seed("ipam", "vlans", client.Object{"name": "web", "vid": float64(100),
		"site":   map[string]interface{}{"slug": "berlin", "name": "Berlin"},
		"status": map[string]interface{}{"value": "active"}})
	f.Seed("ipam", "vlans", client.Object{"name": "orphan", "vid": float64(200)})

	// Prefix in a VRF, scoped to a site via the 4.2 generic scope.
	f.Seed("ipam", "prefixes", client.Object{"prefix": "10.0.0.0/24",
		"vrf":        map[string]interface{}{"name": "Production"},
		"scope_type": "dcim.site",
		"scope":      map[string]interface{}{"slug": "berlin", "name": "Berlin"},
		"status":     map[string]interface{}{"value": "active"}})

	res, err := importer.Import(c, importer.Options{
		Phases:   map[string]bool{"foundation": true, "network": true},
		Defaults: importer.DefaultsOptions(true, 3),
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	vrfs, ok := fileByPath(res, "definitions/vrfs/vrfs.yaml")
	if !ok || !strings.Contains(string(vrfs.Bytes), "enforce_unique: true") {
		t.Fatalf("VRF enforce_unique not emitted: %v\n%s", ok, vrfs.Bytes)
	}

	vlans, ok := fileByPath(res, "definitions/vlans/vlans.yaml")
	if !ok {
		t.Fatal("no vlans file")
	}
	if !strings.Contains(string(vlans.Bytes), "vid: 100") {
		t.Errorf("VLAN 100 not emitted:\n%s", vlans.Bytes)
	}
	if strings.Contains(string(vlans.Bytes), "orphan") {
		t.Errorf("site-less VLAN should have been skipped:\n%s", vlans.Bytes)
	}

	prefixes, ok := fileByPath(res, "definitions/prefixes/prefixes.yaml")
	if !ok {
		t.Fatal("no prefixes file")
	}
	if !strings.Contains(string(prefixes.Bytes), "10.0.0.0/24") ||
		!strings.Contains(string(prefixes.Bytes), "vrf_name: Production") ||
		!strings.Contains(string(prefixes.Bytes), "site_slug: berlin") {
		t.Errorf("prefix fields not mapped:\n%s", prefixes.Bytes)
	}

	// The site-less VLAN must be recorded as skipped, not silently dropped.
	if !res.Report.HasSkips() {
		t.Error("expected the site-less VLAN in the report skips")
	}
	if !strings.Contains(res.Report.Markdown(), "orphan") {
		t.Errorf("report does not name the skipped VLAN:\n%s", res.Report.Markdown())
	}
}
