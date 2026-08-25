// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/spf13/cobra"

	"github.com/braunma/netbox-gitops-controller/pkg/importer"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// applyRewriteOptions layers the sandbox-rewrite settings onto the import
// options. The rewrite flags and their guards are added in a later change; this
// is the seam they plug into.
func applyRewriteOptions(cmd *cobra.Command, opts *importer.Options, logger *utils.Logger) error {
	return nil
}
