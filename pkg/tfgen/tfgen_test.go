package tfgen

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

func clusteredVM() *models.VMConfig {
	return &models.VMConfig{
		Name:     "web-01",
		VMID:     101,
		Cluster:  "berlin-prod-cluster",
		Platform: "ubuntu-22-04",
		VCPUs:    4,
		Memory:   8192,
		Disk:     100,
		Tags:     []string{"gitops", "production"},
		Interfaces: []models.VMInterfaceConfig{
			{
				Name:         "eth0",
				UntaggedVLAN: "Management",
				AddressRole:  "primary",
				IP: &models.IPConfig{
					Address: "10.0.100.21/24",
					DNSName: "web-01.berlin.example.com",
				},
			},
		},
	}
}

func TestBuildMapsFields(t *testing.T) {
	doc, err := Build([]*models.VMConfig{clusteredVM()})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	vm, ok := doc.VMs["web-01"]
	if !ok {
		t.Fatalf("expected VM keyed by name, got keys %v", doc.VMs)
	}
	if vm.VMID != 101 || vm.Cluster != "berlin-prod-cluster" || vm.Platform != "ubuntu-22-04" {
		t.Errorf("unexpected VM scalar fields: %+v", vm)
	}
	if vm.VCPUs != 4 || vm.Memory != 8192 || vm.Disk != 100 {
		t.Errorf("unexpected VM sizing: %+v", vm)
	}
	if len(vm.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(vm.Interfaces))
	}
	nic := vm.Interfaces[0]
	if nic.Name != "eth0" || nic.VLAN != "Management" || nic.IP != "10.0.100.21/24" {
		t.Errorf("unexpected NIC: %+v", nic)
	}
	if !nic.Primary {
		t.Errorf("expected NIC promoted to primary")
	}
	if nic.DNSName != "web-01.berlin.example.com" {
		t.Errorf("unexpected dns_name: %q", nic.DNSName)
	}
}

func TestBuildRequiresVMID(t *testing.T) {
	vm := clusteredVM()
	vm.VMID = 0
	if _, err := Build([]*models.VMConfig{vm}); err == nil {
		t.Fatal("expected error when vmid is missing")
	}
}

func TestBuildRejectsDuplicateNames(t *testing.T) {
	a := clusteredVM()
	b := clusteredVM()
	b.VMID = 102
	if _, err := Build([]*models.VMConfig{a, b}); err == nil {
		t.Fatal("expected error on duplicate VM names")
	}
}

func TestBuildSiteOnlyVMNoMAC(t *testing.T) {
	vm := &models.VMConfig{
		Name:     "edge-01",
		VMID:     201,
		SiteSlug: "test-lab",
		Interfaces: []models.VMInterfaceConfig{
			{Name: "eth0", MACAddress: "de:ad:be:ef:00:01"},
		},
	}
	doc, err := Build([]*models.VMConfig{vm})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	out := doc.VMs["edge-01"]
	if out.Site != "test-lab" || out.Cluster != "" {
		t.Errorf("expected site-only VM, got %+v", out)
	}

	// The MAC must never appear in the generated Terraform output.
	data, err := Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if containsMAC(data) {
		t.Errorf("MAC address leaked into tfvars output: %s", data)
	}
}

func TestMarshalIsValidJSONAndStable(t *testing.T) {
	doc, err := Build([]*models.VMConfig{clusteredVM()})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	a, err := Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	b, _ := Marshal(doc)
	if string(a) != string(b) {
		t.Error("Marshal output is not deterministic")
	}

	// The top-level key must be the `vms` Terraform variable.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(a, &probe); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if _, ok := probe["vms"]; !ok {
		t.Errorf("expected top-level `vms` variable, got keys %v", probe)
	}
}

func containsMAC(data []byte) bool {
	return bytes.Contains(data, []byte("de:ad:be:ef")) || bytes.Contains(data, []byte("mac"))
}
