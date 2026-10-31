// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

// TestSyncStopsBeforeNetBoxOnBadYAML verifies that a sync over a data
// directory with mistakes in two files reports both and never sends a request
// to NetBox — not even the version check — so nothing is half-applied.
func TestSyncStopsBeforeNetBoxOnBadYAML(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("NETBOX_URL", srv.URL)
	t.Setenv("NETBOX_TOKEN", "test")

	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("definitions/racks/racks.yaml", "- { name: \"R1\", slug: \"r1\", sight: \"berlin\" }\n")
	write("inventory/hardware/active/servers.yaml", "- { name: \"srv-01\", site: \"s\", role: \"r\" }\n")

	oldDir, oldConfig, oldOutput := dataDir, configFile, outputFormat
	defer func() { dataDir, configFile, outputFormat = oldDir, oldConfig, oldOutput }()
	dataDir = root
	configFile = filepath.Join(root, "absent.env")
	outputFormat = "text"

	cmd := &cobra.Command{}
	cmd.Flags().String("config", "", "")
	cmd.Flags().String("data-dir", "", "")
	if err := cmd.Flags().Set("data-dir", root); err != nil {
		t.Fatal(err)
	}

	err := runSync(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "2 problem(s)") {
		t.Fatalf("runSync() = %v, want both files' problems counted", err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("NetBox received %d request(s); a sync over invalid YAML must not contact it", n)
	}
}
