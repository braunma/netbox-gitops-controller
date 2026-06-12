// yamlcheck validates the syntax of all YAML files under the given
// directories. It is used by the CI pipeline (yaml_check job) to catch
// malformed definitions before they reach the reconciler.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	dirs := os.Args[1:]
	if len(dirs) == 0 {
		dirs = []string{"definitions", "inventory", "example/definitions", "example/inventory"}
	}

	var files []string
	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && (strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to scan %s: %v\n", dir, err)
			os.Exit(1)
		}
	}

	if len(files) == 0 {
		fmt.Println("⚠️  No YAML files found to check.")
		return
	}

	fmt.Printf("Found %d YAML files to validate\n", len(files))
	failed := false
	for _, file := range files {
		fmt.Printf("Checking %s...\n", file)
		data, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ %s: %v\n", file, err)
			failed = true
			continue
		}
		var content interface{}
		if err := yaml.Unmarshal(data, &content); err != nil {
			fmt.Fprintf(os.Stderr, "❌ %s: %v\n", file, err)
			failed = true
		}
	}

	if failed {
		os.Exit(1)
	}
	fmt.Println("✅ All found YAML files are valid!")
}
