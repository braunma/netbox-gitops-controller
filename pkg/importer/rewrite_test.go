// SPDX-License-Identifier: Apache-2.0

package importer_test

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/internal/nbtest"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/importer"
)

func seedForRewrite(t *testing.T) (*nbtest.FakeNetBox, *client.NetBoxClient) {
	t.Helper()
	f, c := nbtest.New(t)
	f.Seed("dcim", "sites", client.Object{"name": "Berlin", "slug": "berlin"})
	f.Seed("dcim", "sites", client.Object{"name": "Munich", "slug": "munich"})
	f.Seed("dcim", "devices", client.Object{
		"name": "srv-01", "site": map[string]interface{}{"slug": "berlin", "name": "Berlin"},
		"rack":        map[string]interface{}{"name": "Rack A01"},
		"role":        map[string]interface{}{"slug": "server"},
		"device_type": map[string]interface{}{"slug": "r640"},
	})
	f.Seed("ipam", "prefixes", client.Object{
		"prefix":     "10.0.0.0/24",
		"scope_type": "dcim.site",
		"scope":      map[string]interface{}{"slug": "berlin", "name": "Berlin"},
		"status":     map[string]interface{}{"value": "active"},
	})
	f.Seed("ipam", "vlans", client.Object{
		"name": "web", "vid": float64(100),
		"site": map[string]interface{}{"slug": "berlin", "name": "Berlin"},
	})
	return f, c
}

func rewriteOpts() importer.Options {
	return importer.Options{
		Defaults: importer.DefaultsOptions(true, 3),
		Rewrite: importer.RewriteOptions{
			Sites:      map[string]string{"*": "sandbox"},
			VRF:        "sbx",
			NamePrefix: "sbx-",
			Tag:        importer.SandboxTag,
		},
	}
}

func TestRewriteTransformsSiteNameAndVRF(t *testing.T) {
	_, c := seedForRewrite(t)
	res, err := importer.Import(c, rewriteOpts())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Device: name prefixed, landed under the sandbox site and prefixed rack.
	dev, ok := fileByPath(res, "inventory/hardware/active/sandbox/sbx-rack-a01.yaml")
	if !ok {
		t.Fatalf("device not under sandbox site/prefixed rack; files: %v", paths(res))
	}
	if !strings.Contains(string(dev.Bytes), "name: sbx-srv-01") ||
		!strings.Contains(string(dev.Bytes), "site_slug: sandbox") {
		t.Errorf("device not rewritten:\n%s", dev.Bytes)
	}

	// Prefix: rewritten into the scratch VRF and the sandbox site.
	pfx, _ := fileByPath(res, "definitions/prefixes/prefixes.yaml")
	if !strings.Contains(string(pfx.Bytes), "vrf_name: sbx") ||
		!strings.Contains(string(pfx.Bytes), "site_slug: sandbox") {
		t.Errorf("prefix not rewritten into scratch VRF/site:\n%s", pfx.Bytes)
	}

	// The only VRF emitted is the scratch VRF, with enforce_unique.
	vrf, _ := fileByPath(res, "definitions/vrfs/vrfs.yaml")
	if !strings.Contains(string(vrf.Bytes), "name: sbx") ||
		!strings.Contains(string(vrf.Bytes), "enforce_unique: true") {
		t.Errorf("scratch VRF not emitted with enforce_unique:\n%s", vrf.Bytes)
	}

	// VLAN identity: the VID is unchanged; only the site moves to sandbox. The
	// site-scoped lookup then makes the sync create a new VLAN in sandbox
	// rather than matching the production one.
	vlan, _ := fileByPath(res, "definitions/vlans/vlans.yaml")
	if !strings.Contains(string(vlan.Bytes), "vid: 100") ||
		!strings.Contains(string(vlan.Bytes), "site_slug: sandbox") {
		t.Errorf("VLAN identity not preserved under rewrite:\n%s", vlan.Bytes)
	}

	// Sandbox tag stamped on a taggable object.
	if !strings.Contains(string(pfx.Bytes), "sandbox") {
		t.Errorf("sandbox tag not stamped:\n%s", pfx.Bytes)
	}
}

func TestRewriteIsDeterministic(t *testing.T) {
	_, c := seedForRewrite(t)
	a, _ := importer.Import(c, rewriteOpts())
	b, _ := importer.Import(c, rewriteOpts())
	if len(a.Files) != len(b.Files) {
		t.Fatalf("file count differs")
	}
	for i := range a.Files {
		if a.Files[i].Path != b.Files[i].Path || string(a.Files[i].Bytes) != string(b.Files[i].Bytes) {
			t.Fatalf("rewrite output not deterministic at %s", a.Files[i].Path)
		}
	}
}

// A plain import excludes a leftover sandbox site under --exclude-site, and
// includes it without.
func TestExcludeSiteDropsSandbox(t *testing.T) {
	f, c := nbtest.New(t)
	f.Seed("dcim", "sites", client.Object{"name": "sandbox", "slug": "sandbox"})
	f.Seed("dcim", "sites", client.Object{"name": "Berlin", "slug": "berlin"})
	f.Seed("dcim", "devices", client.Object{
		"name": "sbx-srv-01", "site": map[string]interface{}{"slug": "sandbox", "name": "sandbox"},
		"role": map[string]interface{}{"slug": "server"}, "device_type": map[string]interface{}{"slug": "r640"},
	})

	with, _ := importer.Import(c, importer.Options{})
	if _, ok := fileByPath(with, "inventory/hardware/active/sandbox/unracked.yaml"); !ok {
		t.Fatalf("expected the sandbox device without --exclude-site; files: %v", paths(with))
	}

	without, _ := importer.Import(c, importer.Options{ExcludeSites: []string{"sandbox"}})
	if _, ok := fileByPath(without, "inventory/hardware/active/sandbox/unracked.yaml"); ok {
		t.Fatalf("--exclude-site did not drop the sandbox device; files: %v", paths(without))
	}
}
