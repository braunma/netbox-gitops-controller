// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/export"
)

func sampleExport() *export.Result {
	return &export.Result{
		Files: []export.File{
			{Path: "definitions/sites/imported.yaml", Kind: "site", Objects: 1, Data: []byte("- name: A\n  slug: a\n")},
			{Path: "inventory/hardware/active/imported-a.yaml", Kind: "device", Objects: 1, Data: []byte("- name: d\n")},
		},
		Exported: map[string]int{"site": 1, "device": 1},
		Warnings: []string{"something was left out"},
	}
}

func TestWriteExportWritesEveryFile(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := writeExport(&out, dir, sampleExport(), false, false, false); err != nil {
		t.Fatalf("writeExport() error = %v", err)
	}
	for _, f := range sampleExport().Files {
		got, err := os.ReadFile(filepath.Join(dir, f.Path))
		if err != nil || string(got) != string(f.Data) {
			t.Errorf("%s = %q, %v; want %q", f.Path, got, err, f.Data)
		}
	}
	if !strings.Contains(out.String(), "something was left out") {
		t.Errorf("warnings not printed:\n%s", out.String())
	}
}

// An existing file is never replaced: the export is written next to it, so a
// second export adds to a merged first one instead of undeclaring its objects.
func TestWriteExportKeepsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "inventory/hardware/active/imported-a.yaml")
	if err := os.MkdirAll(filepath.Dir(existing), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	var dry bytes.Buffer
	if err := writeExport(&dry, dir, sampleExport(), false, true, false); err != nil {
		t.Fatalf("writeExport(dry-run) error = %v", err)
	}
	if !strings.Contains(dry.String(), "imported-a-2.yaml") {
		t.Errorf("dry run does not name the file it would write:\n%s", dry.String())
	}

	if err := writeExport(&bytes.Buffer{}, dir, sampleExport(), false, false, false); err != nil {
		t.Fatalf("writeExport() error = %v", err)
	}
	if got, _ := os.ReadFile(existing); string(got) != "mine" {
		t.Errorf("existing file was changed to %q", got)
	}
	next := filepath.Join(dir, "inventory/hardware/active/imported-a-2.yaml")
	if got, err := os.ReadFile(next); err != nil || string(got) != "- name: d\n" {
		t.Errorf("%s = %q, %v; want the export next to the existing file", next, got, err)
	}

	if err := writeExport(&bytes.Buffer{}, dir, sampleExport(), true, false, false); err != nil {
		t.Fatalf("writeExport(force) error = %v", err)
	}
	if got, _ := os.ReadFile(existing); string(got) == "mine" {
		t.Errorf("--force did not overwrite")
	}
}

// Two files under one path would lose the first without a word.
func TestWriteExportRefusesDuplicatePaths(t *testing.T) {
	dir := t.TempDir()
	res := sampleExport()
	res.Files = append(res.Files, res.Files[0])
	if err := writeExport(&bytes.Buffer{}, dir, res, false, false, false); err == nil {
		t.Fatal("writeExport() accepted two files with one path")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("wrote %d entries despite the refusal", len(entries))
	}
}

func TestWriteExportDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := writeExport(&out, dir, sampleExport(), false, true, false); err != nil {
		t.Fatalf("writeExport(dry-run) error = %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("dry run wrote %d entries", len(entries))
	}
	if !strings.Contains(out.String(), "Would write 2 file(s)") {
		t.Errorf("dry run output:\n%s", out.String())
	}
}

// A reference export repeats what the repository declares, so it may go
// anywhere except where the loader would read it back.
func TestReferenceExportMustLeaveTheDataDirectory(t *testing.T) {
	data := t.TempDir()
	cases := []struct {
		all     bool
		out     string
		refused bool
	}{
		{false, "", false},
		{true, "", true},
		{true, data, true},
		{true, filepath.Join(data, "definitions"), true},
		{true, filepath.Join(data, "inventory", "hardware"), true},
		{true, filepath.Join(data, "export-reference"), false},
		{true, t.TempDir(), false},
	}
	for _, tc := range cases {
		err := checkReferenceOut(tc.all, tc.out, data)
		if (err != nil) != tc.refused {
			t.Errorf("checkReferenceOut(all=%v, out=%q) error = %v, want refused=%v", tc.all, tc.out, err, tc.refused)
		}
	}
}

func TestReferenceExportIsNotPresentedAsSomethingToMerge(t *testing.T) {
	var out bytes.Buffer
	if err := writeExport(&out, t.TempDir(), sampleExport(), false, false, true); err != nil {
		t.Fatalf("writeExport(reference) error = %v", err)
	}
	if !strings.Contains(out.String(), "Do not merge these files") || strings.Contains(out.String(), "open the merge request") {
		t.Errorf("a reference export must say it is not for merging:\n%s", out.String())
	}
}
