// SPDX-License-Identifier: Apache-2.0

// schemagen writes the JSON Schemas for the data directory's YAML files to
// schema/, and can stamp the editor header that points a file at its schema.
//
//	go run ./cmd/schemagen                 # (re)write schema/*.json
//	go run ./cmd/schemagen --headers DIR   # add/refresh the header line in DIR's YAML files
//
// The header is a comment the YAML language server (VS Code "YAML" extension
// and others) reads:
//
//	# yaml-language-server: $schema=../../schema/device.json
//
// so the editor offers the fields, flags an unknown key and lists the allowed
// values while typing, exactly as the loader will judge the file.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/schema"
)

const headerPrefix = "# yaml-language-server: $schema="

func main() {
	var (
		out     string
		headers string
		check   bool
	)
	flag.StringVar(&out, "out", "schema", "directory the schema files are written to")
	flag.StringVar(&headers, "headers", "", "data directory whose YAML files get (or refresh) the editor header; schema paths are written relative to each file")
	flag.BoolVar(&check, "check", false, "write nothing; exit 1 if the committed schema files differ from the models")
	flag.Parse()

	if headers != "" {
		if err := stampHeaders(headers, out); err != nil {
			fmt.Fprintln(os.Stderr, "❌", err)
			os.Exit(1)
		}
		return
	}

	stale := 0
	for _, kind := range schema.Kinds {
		data, err := schema.Generate(kind)
		if err != nil {
			fmt.Fprintln(os.Stderr, "❌", kind.Name, err)
			os.Exit(1)
		}
		data = append(data, '\n')
		path := filepath.Join(out, kind.Name+".json")
		if check {
			have, _ := os.ReadFile(path) // #nosec G304 -- schema path from the repository
			if !bytes.Equal(have, data) {
				fmt.Fprintf(os.Stderr, "❌ %s is behind the models; run `go run ./cmd/schemagen`\n", path)
				stale++
			}
			continue
		}
		if err := os.MkdirAll(out, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "❌", err)
			os.Exit(1)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil { // #nosec G306 -- a schema file meant to be read
			fmt.Fprintln(os.Stderr, "❌", err)
			os.Exit(1)
		}
		fmt.Println("wrote", path)
	}
	if stale > 0 {
		os.Exit(1)
	}
}

// stampHeaders puts the editor header as the first line of every YAML file
// under dataDir whose folder names a kind, replacing one that is already
// there. The schema path is relative to the file, so the header works in a
// checkout at any location as long as schema/ sits beside definitions/ and
// inventory/.
func stampHeaders(dataDir, schemaDir string) error {
	absData, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	absSchema, err := filepath.Abs(schemaDir)
	if err != nil {
		return err
	}
	return filepath.Walk(absData, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if ext := filepath.Ext(path); ext != ".yaml" && ext != ".yml" {
			return nil
		}
		rel, _ := filepath.Rel(absData, path)
		kind, ok := schema.KindForPath(filepath.ToSlash(rel))
		if !ok {
			return nil
		}
		target, err := filepath.Rel(filepath.Dir(path), filepath.Join(absSchema, kind.Name+".json"))
		if err != nil {
			return err
		}
		header := headerPrefix + filepath.ToSlash(target)
		content, err := os.ReadFile(path) // #nosec G304 -- data file from the given directory
		if err != nil {
			return err
		}
		lines := strings.SplitN(string(content), "\n", 2)
		if strings.HasPrefix(lines[0], headerPrefix) {
			if lines[0] == header {
				return nil
			}
			rest := ""
			if len(lines) > 1 {
				rest = lines[1]
			}
			content = []byte(rest)
		}
		updated := header + "\n" + string(content)
		if err := os.WriteFile(path, []byte(updated), info.Mode()); err != nil {
			return err
		}
		fmt.Println("stamped", rel)
		return nil
	})
}
