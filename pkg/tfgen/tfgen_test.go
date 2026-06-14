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
		Node:     "pve-01",
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
	if vm.VMID != 101 || vm.Node != "pve-01" || vm.Cluster != "berlin-prod-cluster" || vm.Platform != "ubuntu-22-04" {
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

func TestBuildPreservesInterfaceOrderAndPrimary(t *testing.T) {
	// Terraform attaches the default gateway to the primary interface and emits
	// ip_config blocks in NIC order, so tfgen must preserve interface order and
	// flag exactly the interface whose address_role is "primary".
	vm := clusteredVM()
	vm.Interfaces = []models.VMInterfaceConfig{
		{Name: "eth0", IP: &models.IPConfig{Address: "10.0.0.10/24"}},
		{Name: "eth1", AddressRole: "primary", IP: &models.IPConfig{Address: "10.0.1.10/24"}},
	}

	doc, err := Build([]*models.VMConfig{vm})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	got := doc.VMs["web-01"].Interfaces
	if len(got) != 2 {
		t.Fatalf("expected 2 interfaces, got %d", len(got))
	}
	if got[0].Name != "eth0" || got[1].Name != "eth1" {
		t.Errorf("interface order not preserved: %+v", got)
	}
	if got[0].Primary {
		t.Error("eth0 should not be primary")
	}
	if !got[1].Primary {
		t.Error("eth1 (address_role primary) should be flagged primary")
	}
}

func TestBuildSkipsVMsWithoutVMID(t *testing.T) {
	// A VM without a vmid is NetBox-only: skipped, not an error, and the
	// provisioned VMs alongside it are still emitted.
	netboxOnly := clusteredVM()
	netboxOnly.Name = "doc-only"
	netboxOnly.VMID = 0
	provisioned := clusteredVM()

	doc, err := Build([]*models.VMConfig{netboxOnly, provisioned})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if _, ok := doc.VMs["doc-only"]; ok {
		t.Error("VM without vmid should be skipped, but it was emitted")
	}
	if _, ok := doc.VMs["web-01"]; !ok {
		t.Error("VM with vmid should be emitted")
	}
}

func TestBuildAlwaysStampsManagedTag(t *testing.T) {
	// VM with no managed tag and a duplicate: gitops is appended once, dupes drop.
	vm := clusteredVM()
	vm.Tags = []string{"production", "production"}
	doc, err := Build([]*models.VMConfig{vm})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	got := doc.VMs["web-01"].Tags
	want := []string{"production", "gitops"}
	if len(got) != len(want) {
		t.Fatalf("expected tags %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected tags %v, got %v", want, got)
		}
	}

	// A VM that already lists gitops must not get a second copy.
	vm2 := clusteredVM() // Tags: ["gitops", "production"]
	doc2, _ := Build([]*models.VMConfig{vm2})
	count := 0
	for _, tag := range doc2.VMs["web-01"].Tags {
		if tag == "gitops" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one gitops tag, got %d in %v", count, doc2.VMs["web-01"].Tags)
	}
}

func TestBuildRequiresNode(t *testing.T) {
	vm := clusteredVM()
	vm.Node = ""
	if _, err := Build([]*models.VMConfig{vm}); err == nil {
		t.Fatal("expected error when node is missing")
	}
}

func TestBuildRequiresPlatform(t *testing.T) {
	// A provisioned VM clones from a template named by its platform, so a vmid
	// without a platform is an error rather than an opaque Terraform failure.
	vm := clusteredVM()
	vm.Platform = ""
	if _, err := Build([]*models.VMConfig{vm}); err == nil {
		t.Fatal("expected error when platform is missing")
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
		Node:     "pve-06",
		Platform: "debian-12",
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
