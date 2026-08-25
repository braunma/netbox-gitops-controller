// SPDX-License-Identifier: Apache-2.0

package importer_test

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/internal/nbtest"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/importer"
)

func TestImportVMs(t *testing.T) {
	f, c := nbtest.New(t)
	f.Seed("dcim", "sites", client.Object{"name": "Berlin", "slug": "berlin"})
	f.Seed("virtualization", "cluster-types", client.Object{"name": "Proxmox", "slug": "proxmox"})
	f.Seed("virtualization", "clusters", client.Object{
		"name": "berlin-prod", "type": map[string]interface{}{"slug": "proxmox"},
	})

	// A clustered VM with a vmid custom field and an interface holding an IP.
	vm := f.Seed("virtualization", "virtual-machines", client.Object{
		"name":    "web-01",
		"cluster": map[string]interface{}{"name": "berlin-prod"},
		"status":  map[string]interface{}{"value": "active"},
		"vcpus":   float64(4), "memory": float64(8192), "disk": float64(50),
		"custom_fields": map[string]interface{}{"vmid": float64(101)},
	})
	nic := f.Seed("virtualization", "interfaces", client.Object{
		"name": "eth0", "enabled": true,
		"virtual_machine": map[string]interface{}{"id": vm["id"].(int)},
	})
	ip := f.Seed("ipam", "ip-addresses", client.Object{
		"address": "10.0.0.9/24", "assigned_object_type": "virtualization.vminterface",
		"assigned_object_id": nic["id"].(int),
	})
	vm["primary_ip4"] = map[string]interface{}{"id": ip["id"].(int)}

	// A VM with neither cluster nor site: parked.
	f.Seed("virtualization", "virtual-machines", client.Object{"name": "lost-01"})

	res, err := importer.Import(c, importer.Options{
		Phases:   map[string]bool{"virtualization": true},
		Defaults: importer.DefaultsOptions(true, 3),
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	clustered, ok := fileByPath(res, "inventory/virtual/berlin-prod.yaml")
	if !ok {
		t.Fatalf("clustered VM file missing; files: %v", paths(res))
	}
	body := string(clustered.Bytes)
	for _, want := range []string{"name: web-01", "vmid: 101", "vcpus: 4", "address: 10.0.0.9/24", "address_role: primary"} {
		if !strings.Contains(body, want) {
			t.Errorf("VM file missing %q:\n%s", want, body)
		}
	}

	parked, ok := fileByPath(res, "inventory/virtual/_unplaced.yaml")
	if !ok {
		t.Fatalf("unplaced VM not parked; files: %v", paths(res))
	}
	if !strings.Contains(string(parked.Bytes), "lost-01") ||
		!strings.Contains(string(parked.Bytes), "NOT APPLIED") {
		t.Errorf("parked file wrong:\n%s", parked.Bytes)
	}
}
