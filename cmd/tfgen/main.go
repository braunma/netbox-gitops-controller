// tfgen converts the virtual-machine inventory YAML (the same files the NetBox
// reconciler consumes) into a Terraform variables file (tfvars.json) describing
// the VMs to provision in Proxmox. It is the bridge that lets one YAML source
// of truth drive both NetBox documentation and Proxmox provisioning.
//
// It performs no network calls: it loads and validates the YAML through the
// shared loader, then emits a deterministic JSON document. The output is a
// single Terraform variable, `vms`, keyed by VM name.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/tfgen"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

func main() {
	dataDir := flag.String("data-dir", ".", "Base directory containing inventory/ (falls back to example/ when not found)")
	out := flag.String("out", "terraform/generated.tfvars.json", "Output tfvars.json path (use '-' for stdout)")
	flag.Parse()

	logger := utils.NewLogger(false)

	dir, err := resolveDataDir(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	dl := loader.NewDataLoader(dir, logger)
	vms, err := dl.LoadVMs("inventory/virtual")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ failed to load VMs: %v\n", err)
		os.Exit(1)
	}

	doc, err := tfgen.Build(vms)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	data, err := tfgen.Marshal(doc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ failed to encode tfvars: %v\n", err)
		os.Exit(1)
	}

	if *out == "-" {
		os.Stdout.Write(data)
		return
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "❌ failed to write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Printf("✅ Wrote %d VM(s) to %s\n", len(doc.VMs), *out)
}

// resolveDataDir mirrors the controller's auto-detection: use the given
// directory if it has an inventory/ folder, otherwise fall back to example/.
func resolveDataDir(dir string) (string, error) {
	if _, err := os.Stat(dir + "/inventory"); err == nil {
		return dir, nil
	}
	if _, err := os.Stat("example/inventory"); err == nil {
		return "example", nil
	}
	return "", fmt.Errorf("no inventory/ found in %q or example/", dir)
}
