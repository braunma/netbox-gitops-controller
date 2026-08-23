// SPDX-License-Identifier: Apache-2.0

package collectors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "collectors.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfig(t *testing.T) {
	path := writeConfig(t, `sources:
  - name: pve-prod
    type: proxmox
    url: https://pve.example.com:8006
    token_env: PROXMOX_TOKEN
    verify_tls: true
    cluster: berlin-prod-cluster
  - name: pve-lab
    type: proxmox
    url: https://lab.example.com:8006
    token_env: PVE_LAB_TOKEN
    verify_tls: false
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Sources) != 2 {
		t.Fatalf("loaded %d sources, want 2", len(cfg.Sources))
	}
	if !cfg.Sources[0].ShouldVerifyTLS() {
		t.Error("verify_tls: true was read as false")
	}
	if cfg.Sources[1].ShouldVerifyTLS() {
		t.Error("verify_tls: false was read as true")
	}
	if cfg.Sources[0].Cluster != "berlin-prod-cluster" {
		t.Errorf("cluster = %q, want the declared NetBox cluster", cfg.Sources[0].Cluster)
	}
}

// Verification is on unless a source explicitly turns it off.
func TestVerifyTLSDefaultsToOn(t *testing.T) {
	if !(Source{}).ShouldVerifyTLS() {
		t.Error("a source that says nothing about TLS must still verify certificates")
	}
}

// A mistyped key that is silently dropped would leave an operator believing
// verification is off when it is on, or the reverse.
func TestLoadConfigRejectsUnknownKeys(t *testing.T) {
	path := writeConfig(t, `sources:
  - name: pve-prod
    type: proxmox
    url: https://pve.example.com:8006
    token_env: PROXMOX_TOKEN
    verify_tsl: false
`)
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected an error for a mistyped key")
	}
	if !strings.Contains(err.Error(), "verify_tsl") {
		t.Errorf("error = %q, want it to name the key it did not recognise", err)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		cfg    Config
		wantIn []string
	}{
		{
			"everything missing at once",
			Config{Sources: []Source{{}}},
			[]string{"name is required", "type is required", "url is required", "token_env is required"},
		},
		{
			"unknown type",
			Config{Sources: []Source{{Name: "s", Type: "vcenter", URL: "https://x", TokenEnv: "T"}}},
			[]string{`unknown type "vcenter"`, "proxmox"},
		},
		{
			"duplicate names would overwrite each other's generated files",
			Config{Sources: []Source{
				{Name: "pve", Type: "proxmox", URL: "https://a", TokenEnv: "T"},
				{Name: "pve", Type: "proxmox", URL: "https://b", TokenEnv: "T"},
			}},
			[]string{"declared twice"},
		},
		{
			"a name that is a path would escape inventory/discovered/",
			Config{Sources: []Source{{Name: "../etc", Type: "proxmox", URL: "https://a", TokenEnv: "T"}}},
			[]string{"path separator"},
		},
		{
			"negative timeout",
			Config{Sources: []Source{{Name: "s", Type: "proxmox", URL: "https://a", TokenEnv: "T", TimeoutSeconds: -1}}},
			[]string{"must not be negative"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if err == nil {
				t.Fatal("expected a validation error")
			}
			for _, want := range tt.wantIn {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to mention %q", err, want)
				}
			}
		})
	}

	valid := Config{Sources: []Source{{Name: "pve", Type: "proxmox", URL: "https://a", TokenEnv: "T"}}}
	if err := valid.Validate(); err != nil {
		t.Errorf("a complete source was rejected: %v", err)
	}
}

func TestSelect(t *testing.T) {
	cfg := &Config{Sources: []Source{
		{Name: "pve-prod", Type: "proxmox"},
		{Name: "pve-lab", Type: "proxmox"},
	}}

	all, err := cfg.Select(nil)
	if err != nil || len(all) != 2 {
		t.Fatalf("Select(nil) = %d sources, %v; want every source", len(all), err)
	}

	one, err := cfg.Select([]string{"pve-lab"})
	if err != nil || len(one) != 1 || one[0].Name != "pve-lab" {
		t.Fatalf("Select([pve-lab]) = %+v, %v", one, err)
	}

	// A typo must not turn into a run that quietly scans nothing.
	_, err = cfg.Select([]string{"pve-labb"})
	if err == nil {
		t.Fatal("expected an error for a source name that matches nothing")
	}
	if !strings.Contains(err.Error(), "pve-prod, pve-lab") {
		t.Errorf("error = %q, want it to list what is configured", err)
	}
}

func TestToken(t *testing.T) {
	t.Setenv("SOME_TOKEN", "  root@pam!id=secret  ")
	got, err := Source{Name: "s", TokenEnv: "SOME_TOKEN"}.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != "root@pam!id=secret" {
		t.Errorf("Token = %q, want it trimmed", got)
	}

	t.Setenv("SOME_TOKEN", "")
	if _, err := (Source{Name: "s", TokenEnv: "SOME_TOKEN"}).Token(); err == nil {
		t.Error("expected an error for an empty token variable")
	}
}
