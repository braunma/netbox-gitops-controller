// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// resetIngestFlags puts the package-level flag state back to its defaults, so
// one test's flags cannot leak into the next.
func resetIngestFlags(t *testing.T, dir string) {
	t.Helper()
	previous := dataDir
	t.Cleanup(func() {
		dataDir = previous
		ingestDryRun, ingestOutput, ingestDetailedExitcode = false, "text", false
		customFieldPrefix, collectorsConfig, collectSources = "hw_", "", nil
		ingestFormat, ingestInput, ingestSource = "", "", ""
		exitCode = 0
	})
	dataDir = dir
	ingestDryRun, ingestOutput, ingestDetailedExitcode = false, "text", false
	customFieldPrefix, collectorsConfig, collectSources = "hw_", "", nil
	ingestFormat, ingestInput, ingestSource = "", "", ""
	exitCode = 0
}

// captureOutput redirects the logger's output for the duration of a test, so
// the suite's own log stays readable.
func captureOutput(t *testing.T) {
	t.Helper()
	previous := utils.DefaultOutput()
	utils.SetDefaultOutput(&strings.Builder{})
	t.Cleanup(func() { utils.SetDefaultOutput(previous) })
}

func ingestRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	// resolveDataDir insists on a definitions/ folder, the same as a real run.
	if err := os.MkdirAll(filepath.Join(root, "definitions"), 0o750); err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const cliServersYAML = `defaults:
  site_slug: "berlin-dc"
  role_slug: "server"
  device_type_slug: "example-server"

devices:
  - name: "srv-01"
    serial: "OLD"
`

const cliScanJSON = `{"servers":[
  {"host":"10.0.1.11","hostname":"srv-01","collected_at":"2026-08-23T06:14:02Z",
   "serial_number":"NEW","service_tag":"TAG1","cpu_count":2,"cpus":[{"cores":24}],
   "power_state":"On","total_memory_gib":384}
], "stats":{"total_servers":1,"successful_count":1}}`

// runIngestCommand drives the subcommand the way cobra does. --data-dir is a
// persistent flag of the root command, so a standalone subcommand does not
// carry it; the tests set dataDir directly instead, which is the same variable
// the flag writes to.
func runIngestCommand(t *testing.T, args ...string) error {
	t.Helper()
	cmd := newIngestCommand()
	cmd.SetArgs(args)
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	return cmd.Execute()
}

func TestIngestWritesFactsAndSetsDetailedExitcode(t *testing.T) {
	root := ingestRepo(t, map[string]string{
		"inventory/hardware/active/servers.yaml": cliServersYAML,
		"scan.json":                              cliScanJSON,
	})
	resetIngestFlags(t, root)
	captureOutput(t)

	if err := runIngestCommand(t,
		"--format", "idrac-json",
		"--input", filepath.Join(root, "scan.json"), "--detailed-exitcode",
	); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	if exitCode != 2 {
		t.Errorf("exit code = %d, want 2 so CI can decide to open a merge request", exitCode)
	}

	written, err := os.ReadFile(filepath.Join(root, "inventory/hardware/active/servers.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`serial: "NEW"`, `asset_tag: "TAG1"`, "hw_cpu_count: 2"} {
		if !strings.Contains(string(written), want) {
			t.Errorf("the file does not carry %q:\n%s", want, written)
		}
	}

	// A second run has nothing to do, so CI opens no merge request.
	resetIngestFlags(t, root)
	if err := runIngestCommand(t,
		"--format", "idrac-json",
		"--input", filepath.Join(root, "scan.json"), "--detailed-exitcode",
	); err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d after an unchanged re-run, want 0", exitCode)
	}
}

func TestIngestDryRunWritesNothing(t *testing.T) {
	root := ingestRepo(t, map[string]string{
		"inventory/hardware/active/servers.yaml": cliServersYAML,
		"scan.json":                              cliScanJSON,
	})
	resetIngestFlags(t, root)
	captureOutput(t)

	if err := runIngestCommand(t,
		"--format", "idrac-json",
		"--input", filepath.Join(root, "scan.json"), "--dry-run", "--detailed-exitcode",
	); err != nil {
		t.Fatalf("ingest --dry-run: %v", err)
	}

	written, err := os.ReadFile(filepath.Join(root, "inventory/hardware/active/servers.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `serial: "OLD"`) {
		t.Errorf("--dry-run wrote to the file:\n%s", written)
	}
	if exitCode != 2 {
		t.Errorf("exit code = %d, want 2: a dry run still reports that the repository would change", exitCode)
	}
}

func TestIngestRejectsBadArguments(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		wantIn string
	}{
		{"no format", []string{"--input", "scan.json"}, "--format is required"},
		{"unknown format", []string{"--format", "redfish-xml", "--input", "scan.json"}, `unsupported --format "redfish-xml"`},
		{"no input", []string{"--format", "idrac-json"}, "--input is required"},
		{"bad output", []string{"--format", "idrac-json", "--input", "scan.json", "--output", "yaml"}, `invalid --output format "yaml"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetIngestFlags(t, t.TempDir())
			captureOutput(t)
			err := runIngestCommand(t, tt.args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantIn)
			}
		})
	}
}

// The JSON output is what a pipeline reads to decide whether to open a merge
// request, so it goes to stdout with the logs out of the way.
func TestIngestJSONOutput(t *testing.T) {
	root := ingestRepo(t, map[string]string{
		"inventory/hardware/active/servers.yaml": cliServersYAML,
		"scan.json":                              cliScanJSON,
	})
	resetIngestFlags(t, root)
	captureOutput(t)

	stdout, restore := captureStdout(t)
	err := runIngestCommand(t,
		"--format", "idrac-json",
		"--input", filepath.Join(root, "scan.json"), "--output", "json", "--dry-run",
	)
	out := restore()
	if err != nil {
		t.Fatalf("ingest --output json: %v", err)
	}
	_ = stdout

	for _, want := range []string{`"dry_run": true`, `"summary"`, `"facts_updated": 1`, `"changes"`, `"action": "update"`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output is missing %q:\n%s", want, out)
		}
	}
}

// captureStdout redirects os.Stdout and returns a function that restores it and
// yields what was written.
func captureStdout(t *testing.T) (*os.File, func() string) {
	t.Helper()
	previous := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()

	return w, func() string {
		_ = w.Close()
		os.Stdout = previous
		return <-done
	}
}

func TestResolveCollectorsConfig(t *testing.T) {
	resetIngestFlags(t, "/data")

	if got, want := resolveCollectorsConfig(), filepath.Join("/data", "collectors.yaml"); got != want {
		t.Errorf("default = %q, want %q", got, want)
	}

	t.Setenv("COLLECTORS_CONFIG", "/etc/sources.yaml")
	if got := resolveCollectorsConfig(); got != "/etc/sources.yaml" {
		t.Errorf("with COLLECTORS_CONFIG = %q, want the environment honoured", got)
	}

	collectorsConfig = "/flag/sources.yaml"
	if got := resolveCollectorsConfig(); got != "/flag/sources.yaml" {
		t.Errorf("with the flag set = %q, want the flag to win", got)
	}
}

// The collect command must say what is missing rather than scanning nothing.
func TestCollectWithoutAConfigurationSaysSo(t *testing.T) {
	root := ingestRepo(t, nil)
	resetIngestFlags(t, root)
	captureOutput(t)

	cmd := newCollectCommand()
	cmd.SetArgs(nil)
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error when no collector configuration exists")
	}
	if !strings.Contains(err.Error(), "collectors.yaml.example") {
		t.Errorf("error = %q, want it to say where to start", err)
	}
}

// processSnapshot is the shared body of both commands; an empty snapshot must
// be a quiet no-op rather than an error or a stray directory.
func TestProcessSnapshotWithNothingFound(t *testing.T) {
	root := ingestRepo(t, nil)
	resetIngestFlags(t, root)
	captureOutput(t)

	changed, err := processSnapshot(&discovery.Snapshot{Source: "pve-prod"}, utils.NewLogger(false))
	if err != nil {
		t.Fatalf("processSnapshot: %v", err)
	}
	if changed {
		t.Error("an empty snapshot reported changes")
	}
	if _, err := os.Stat(filepath.Join(root, "inventory", "discovered")); !os.IsNotExist(err) {
		t.Error("an empty snapshot created the discovered directory")
	}
}

// The subcommands must not quietly shadow the root command's persistent flags.
func TestIngestCommandsShareTheRootFlags(t *testing.T) {
	for _, cmd := range []*cobra.Command{newCollectCommand(), newIngestCommand()} {
		if cmd.Flags().Lookup("data-dir") != nil {
			t.Errorf("%s declares its own --data-dir, shadowing the root's persistent flag", cmd.Name())
		}
		for _, name := range []string{"dry-run", "output", "detailed-exitcode", "custom-field-prefix"} {
			if cmd.Flags().Lookup(name) == nil {
				t.Errorf("%s is missing --%s", cmd.Name(), name)
			}
		}
	}
}
