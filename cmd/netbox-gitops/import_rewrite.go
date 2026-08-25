// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/braunma/netbox-gitops-controller/pkg/importer"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// Rewrite flag backing vars. These are the one carve-out from "every flag has
// an environment variable": they are flag-only, because a stray REWRITE_SITE in
// a project's CI variables would silently rewrite every future import and
// nothing about the output would look wrong.
var (
	rewriteSite  []string
	rewriteVRF   string
	namePrefix   string
	noNamePrefix bool
)

// addRewriteFlags registers the sandbox-rewrite flags on the import command.
func addRewriteFlags(cmd *cobra.Command) {
	cmd.Flags().StringSliceVar(&rewriteSite, "rewrite-site", nil,
		"Sandbox rehearsal: rewrite site OLD=NEW (repeatable; '*=NEW' maps every site). Flag-only, never read from the environment")
	cmd.Flags().StringVar(&rewriteVRF, "rewrite-vrf", "",
		"Put all imported IPAM into this scratch VRF (required with --rewrite-site when the network phase runs). Flag-only")
	cmd.Flags().StringVar(&namePrefix, "name-prefix", "",
		"Prefix every name-identified object (devices, VMs, racks, clusters) so a rewrite cannot collide with production. Flag-only")
	cmd.Flags().BoolVar(&noNamePrefix, "no-name-prefix", false,
		"Allow --rewrite-site without a --name-prefix (you accept the collision risk). Flag-only")
}

// applyRewriteOptions parses and validates the rewrite flags and layers them
// onto the import options. It enforces the guards that make the rehearsal safe,
// all as hard errors so a misconfigured sandbox cannot run.
func applyRewriteOptions(cmd *cobra.Command, opts *importer.Options, logger *utils.Logger) error {
	// Guard: rewrite-vrf / name-prefix only mean something under --rewrite-site.
	if len(rewriteSite) == 0 {
		if rewriteVRF != "" || namePrefix != "" || noNamePrefix {
			return fmt.Errorf("--rewrite-vrf, --name-prefix and --no-name-prefix require --rewrite-site")
		}
		return nil
	}

	mapping, err := parseRewriteSites(rewriteSite)
	if err != nil {
		return err
	}

	// Guard: a name prefix is mandatory unless explicitly waived, so a
	// rewritten dataset cannot collide with production identities.
	if namePrefix == "" && !noNamePrefix {
		return fmt.Errorf("--rewrite-site requires --name-prefix (or --no-name-prefix to accept the collision risk)")
	}

	// Guard: rewriting IPAM without a scratch VRF would match production
	// prefixes and addresses by CIDR/address, which the site rewrite cannot
	// isolate. Hard error, naming the flag, whenever the network phase runs.
	if opts.Phases["network"] && rewriteVRF == "" {
		return newGuardError("--rewrite-site with the network phase requires --rewrite-vrf: a prefix is identified by CIDR and an address by its address, neither of which the site rewrite changes, so without a scratch VRF the rehearsal would match and re-scope production IPAM. Pass --rewrite-vrf NAME, or --only to skip the network phase")
	}

	opts.Rewrite = importer.RewriteOptions{
		Sites:      mapping,
		VRF:        rewriteVRF,
		NamePrefix: namePrefix,
		Tag:        importer.SandboxTag,
	}
	logger.Warning("Sandbox rewrite active: output is a rehearsal copy and must NOT be merged")
	return nil
}

// parseRewriteSites turns OLD=NEW pairs into a map. "*" is a valid OLD (every
// site). A missing "=" or empty half is an error.
func parseRewriteSites(pairs []string) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range pairs {
		old, new, ok := strings.Cut(p, "=")
		old, new = strings.TrimSpace(old), strings.TrimSpace(new)
		if !ok || old == "" || new == "" {
			return nil, fmt.Errorf("--rewrite-site %q must be OLD=NEW", p)
		}
		out[old] = new
	}
	return out, nil
}
