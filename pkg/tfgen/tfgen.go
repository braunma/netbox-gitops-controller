// Package tfgen transforms the typed VM inventory models into a Terraform
// variables document (tfvars.json) for Proxmox provisioning. It is pure and
// deterministic so it can be unit-tested without any NetBox or Proxmox access.
package tfgen

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// Document is the root tfvars.json object. It carries a single Terraform
// variable, `vms`, keyed by VM name so Terraform can `for_each` over it.
type Document struct {
	VMs map[string]VM `json:"vms"`
}

// VM is the Terraform-facing view of a virtual machine: the declared facts a
// Proxmox provider needs, derived only from YAML (no observed/runtime state).
type VM struct {
	Name       string      `json:"name"`
	VMID       int         `json:"vmid"`
	Node       string      `json:"node"`
	Cluster    string      `json:"cluster,omitempty"`
	Site       string      `json:"site,omitempty"`
	Platform   string      `json:"platform,omitempty"`
	VCPUs      int         `json:"vcpus,omitempty"`
	Memory     int         `json:"memory,omitempty"`
	Disk       int         `json:"disk,omitempty"`
	Tags       []string    `json:"tags,omitempty"`
	Interfaces []Interface `json:"interfaces"`
}

// Interface is the Terraform-facing view of a VM NIC. The MAC is deliberately
// absent: Proxmox generates it and it is not tracked anywhere.
type Interface struct {
	Name    string `json:"name"`
	VLAN    string `json:"vlan,omitempty"`
	IP      string `json:"ip,omitempty"`
	DNSName string `json:"dns_name,omitempty"`
	Primary bool   `json:"primary,omitempty"`
}

// Build converts loaded VM models into a tfvars Document. Only VMs that opt into
// Proxmox provisioning by declaring a positive vmid are included; VMs without a
// vmid are NetBox-only and skipped, so the two consumers stay independent. A VM
// that declares a vmid must also name its target node and a platform (the clone
// template), and VM names must be unique since they key the Terraform map.
func Build(vms []*models.VMConfig) (*Document, error) {
	doc := &Document{VMs: make(map[string]VM, len(vms))}

	for _, vm := range vms {
		if vm.VMID <= 0 {
			// NetBox-only VM (no Proxmox identity) — not provisioned here.
			continue
		}
		if vm.Node == "" {
			return nil, fmt.Errorf("VM %q: node is required for Proxmox provisioning (name the target Proxmox node)", vm.Name)
		}
		if vm.Platform == "" {
			return nil, fmt.Errorf("VM %q: platform is required for Proxmox provisioning (it selects the clone template)", vm.Name)
		}
		if _, dup := doc.VMs[vm.Name]; dup {
			return nil, fmt.Errorf("duplicate VM name %q: names must be unique to key the Terraform map", vm.Name)
		}

		out := VM{
			Name:     vm.Name,
			VMID:     vm.VMID,
			Node:     vm.Node,
			Cluster:  vm.Cluster,
			Site:     vm.SiteSlug,
			Platform: vm.Platform,
			VCPUs:    vm.VCPUs,
			Memory:   vm.Memory,
			Disk:     vm.Disk,
			Tags:     withManagedTag(vm.Tags),
		}

		out.Interfaces = make([]Interface, 0, len(vm.Interfaces))
		for _, iface := range vm.Interfaces {
			nic := Interface{
				Name:    iface.Name,
				VLAN:    iface.UntaggedVLAN,
				Primary: iface.AddressRole == "primary",
			}
			if iface.IP != nil {
				nic.IP = iface.IP.Address
				nic.DNSName = iface.IP.DNSName
			}
			out.Interfaces = append(out.Interfaces, nic)
		}

		doc.VMs[vm.Name] = out
	}

	return doc, nil
}

// withManagedTag returns the VM's tags with the GitOps managed tag guaranteed
// to be present, mirroring the NetBox controller's auto-stamping so a VM carries
// the same `gitops` marker in both systems. Input order is preserved (with the
// managed tag appended if absent) and duplicates are removed for stable output.
func withManagedTag(tags []string) []string {
	out := make([]string, 0, len(tags)+1)
	seen := make(map[string]bool, len(tags)+1)
	for _, t := range tags {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if !seen[constants.ManagedTagSlug] {
		out = append(out, constants.ManagedTagSlug)
	}
	return out
}

// Marshal renders the document as deterministic, indented JSON. Map key order
// in encoding/json is already sorted, so output is stable across runs; the
// sort below documents that intent and guards the slice-building above.
func Marshal(doc *Document) ([]byte, error) {
	names := make([]string, 0, len(doc.VMs))
	for name := range doc.VMs {
		names = append(names, name)
	}
	sort.Strings(names)

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
