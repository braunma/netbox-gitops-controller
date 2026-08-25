// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/braunma/netbox-gitops-controller/internal/nbtest"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/importer"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// resetRewriteFlags clears the package-level rewrite flag vars between cases.
func resetRewriteFlags() {
	rewriteSite = nil
	rewriteVRF = ""
	namePrefix = ""
	noNamePrefix = false
}

func TestRewriteGuards(t *testing.T) {
	logger := utils.NewLogger(false)
	allPhases := map[string]bool{}
	for _, p := range importer.Phases {
		allPhases[p] = true
	}

	t.Run("network without scratch VRF is refused", func(t *testing.T) {
		resetRewriteFlags()
		rewriteSite = []string{"*=sandbox"}
		namePrefix = "sbx-"
		opts := importer.Options{Phases: allPhases}
		err := applyRewriteOptions(nil, &opts, logger)
		if err == nil {
			t.Fatal("expected a guard error for rewrite+network without --rewrite-vrf")
		}
		var g *guardError
		if !errors.As(err, &g) {
			t.Fatalf("expected a *guardError (exit 3), got %T: %v", err, err)
		}
	})

	t.Run("name prefix is mandatory", func(t *testing.T) {
		resetRewriteFlags()
		rewriteSite = []string{"*=sandbox"}
		rewriteVRF = "sbx"
		opts := importer.Options{Phases: allPhases}
		if err := applyRewriteOptions(nil, &opts, logger); err == nil {
			t.Fatal("expected an error requiring --name-prefix")
		}
	})

	t.Run("rewrite-vrf without rewrite-site is refused", func(t *testing.T) {
		resetRewriteFlags()
		rewriteVRF = "sbx"
		opts := importer.Options{Phases: allPhases}
		if err := applyRewriteOptions(nil, &opts, logger); err == nil {
			t.Fatal("expected an error: --rewrite-vrf without --rewrite-site")
		}
	})

	t.Run("valid sandbox config populates options", func(t *testing.T) {
		resetRewriteFlags()
		rewriteSite = []string{"berlin=sandbox", "*=sandbox"}
		rewriteVRF = "sbx"
		namePrefix = "sbx-"
		opts := importer.Options{Phases: allPhases}
		if err := applyRewriteOptions(nil, &opts, logger); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.Rewrite.VRF != "sbx" || opts.Rewrite.NamePrefix != "sbx-" {
			t.Fatalf("rewrite options not set: %+v", opts.Rewrite)
		}
		if opts.Rewrite.Sites["berlin"] != "sandbox" || opts.Rewrite.Sites["*"] != "sandbox" {
			t.Fatalf("site mapping not parsed: %+v", opts.Rewrite.Sites)
		}
	})

	t.Run("network skipped needs no scratch VRF", func(t *testing.T) {
		resetRewriteFlags()
		rewriteSite = []string{"*=sandbox"}
		namePrefix = "sbx-"
		opts := importer.Options{Phases: map[string]bool{"devices": true}}
		if err := applyRewriteOptions(nil, &opts, logger); err != nil {
			t.Fatalf("a DCIM-only rehearsal should not require --rewrite-vrf: %v", err)
		}
	})
}

// The rewrite flags are flag-only: setting them in the environment must have no
// effect, so a leftover REWRITE_SITE in CI variables cannot silently rewrite
// every import. Two full command runs — one with the env vars set, one without
// — must produce byte-identical output.
func TestRewriteFlagsAreNotEnvBound(t *testing.T) {
	f, _ := nbtest.New(t)
	f.Seed("dcim", "sites", client.Object{"name": "Berlin", "slug": "berlin"})
	f.Seed("dcim", "devices", client.Object{
		"name": "srv-01", "site": map[string]interface{}{"slug": "berlin", "name": "Berlin"},
		"role": map[string]interface{}{"slug": "server"}, "device_type": map[string]interface{}{"slug": "r640"},
	})
	t.Setenv("NETBOX_URL", f.URL())
	t.Setenv("NETBOX_TOKEN", "x")

	run := func() map[string]string {
		resetRewriteFlags()
		dir := t.TempDir()
		dataDir = dir
		configFile = ".env"
		cmd := newImportCommand()
		cmd.SetArgs([]string{"--report", "-"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("import: %v", err)
		}
		return readTree(t, dir)
	}

	plain := run()

	// Now set the rewrite env vars — they must be ignored entirely.
	t.Setenv("REWRITE_SITE", "*=sandbox")
	t.Setenv("REWRITE_VRF", "sbx")
	t.Setenv("NAME_PREFIX", "sbx-")
	withEnv := run()

	if len(plain) != len(withEnv) {
		t.Fatalf("file set differs with rewrite env set: %d vs %d", len(plain), len(withEnv))
	}
	for path, body := range plain {
		if withEnv[path] != body {
			t.Fatalf("rewrite env changed output for %s — it must be flag-only", path)
		}
	}
}

// readTree reads every file under dir keyed by its relative path.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}
