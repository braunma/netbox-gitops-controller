// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The loader reports problems as a list, one self-contained line each:
//
//	inventory/hardware/active/servers.yaml: entry 2: device "srv-02": both "site" and "site_slug" are set; ...
//
// so a reader can fix them top to bottom without wondering which file or
// object a line is about, and so one run shows all of them.

// Problems flattens an error the loader returned into the individual problems
// it holds, one line each, in the order they were found.
func Problems(err error) []error {
	return problems(err)
}

// problems flattens an error into the individual problems it joins.
func problems(err error) []error {
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var out []error
		for _, inner := range joined.Unwrap() {
			out = append(out, problems(inner)...)
		}
		return out
	}
	return []error{err}
}

// prefixed puts prefix in front of every problem err holds.
func prefixed(prefix string, err error) error {
	list := problems(err)
	for i, p := range list {
		list[i] = fmt.Errorf("%s: %w", prefix, p)
	}
	return errors.Join(list...)
}

// displayPath shortens a file path for a message: relative to the working
// directory when the file is below it, as given otherwise.
func displayPath(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(wd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return rel
}

// objectLabel names an entry the way its validation errors do, `device
// "srv-01"`, from the raw mapping — for problems found before the object is
// decoded and can name itself.
func objectLabel(kind string, raw map[string]interface{}) string {
	for _, key := range []string{"name", "model", "prefix"} {
		if name, ok := raw[key].(string); ok && name != "" {
			return fmt.Sprintf("%s %q", kind, name)
		}
	}
	return fmt.Sprintf("%s %q", kind, "<unnamed>")
}
