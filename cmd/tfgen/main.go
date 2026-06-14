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
	"path/filepath"

	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/tfgen"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

func main() {
	dataDir := flag.String("data-dir", ".", "Base directory containing inventory/ (falls back to example/ when not found)")
	out := flag.String("out", "terraform/generated.tfvars.json", "Output tfvars.json path (use '-' for stdout)")
	group := flag.String("group", "", "Inventory subfolder under inventory/virtual to load, e.g. an environment like 'prod' (empty = every environment)")
	flag.Parse()

	// When emitting to stdout, reserve it for the tfvars JSON and route all
	// informational logging to stderr (mirrors the controller's --output json).
	if *out == "-" {
		utils.SetDefaultOutput(os.Stderr)
	}

	logger := utils.NewLogger(false)

	dir, err := resolveDataDir(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}

	// Each environment (prod/stage/playground) lives in its own subfolder under
	// inventory/virtual and is provisioned into its own Terraform state. With no
	// --group, load every environment (back-compat / NetBox-style full scan).
	folder := "inventory/virtual"
	if *group != "" {
		folder = filepath.Join("inventory/virtual", *group)

		// Safety: a mistyped --group must fail loudly. The loader treats a
		// missing folder as "skip" (correct for optional NetBox folders), but
		// here a typo would yield an empty {"vms":{}} document and exit 0 — and
		// `terraform apply` against that env's state would then plan to DESTROY
		// every existing VM. Distinguish "typo" (folder absent) from
		// "legitimately empty" (folder present, no VMs yet) by requiring the
		// environment folder to exist.
		if _, statErr := os.Stat(filepath.Join(dir, folder)); os.IsNotExist(statErr) {
			fmt.Fprintf(os.Stderr, "❌ environment folder %q not found under %s (check --group for typos)\n",
				*group, filepath.Join(dir, "inventory/virtual"))
			os.Exit(1)
		}
	}

	dl := loader.NewDataLoader(dir, logger)
	vms, err := dl.LoadVMs(folder)
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

	// VMs without `provision: true` are NetBox-only and intentionally not
	// provisioned; report the count so a forgotten flag is visible, not silent.
	skipped := len(vms) - len(doc.VMs)

	if *out == "-" {
		os.Stdout.Write(data)
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "ℹ️  Skipped %d NetBox-only VM(s) (provision not set)\n", skipped)
		}
		return
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "❌ failed to write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Printf("✅ Wrote %d VM(s) to %s", len(doc.VMs), *out)
	if skipped > 0 {
		fmt.Printf(" (skipped %d NetBox-only VM(s); provision not set)", skipped)
	}
	fmt.Println()
}

// resolveDataDir mirrors the controller's auto-detection: use the given
// directory if it has an inventory/ folder, otherwise fall back to example/.
func resolveDataDir(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "inventory")); err == nil {
		return dir, nil
	}
	if _, err := os.Stat(filepath.Join("example", "inventory")); err == nil {
		return "example", nil
	}
	return "", fmt.Errorf("no inventory/ found in %q or example/", dir)
}
