// SPDX-License-Identifier: Apache-2.0

// Package dotenv reads a KEY=value configuration file into the process
// environment.
//
// The controller takes its credentials from the environment, which is what a
// CI runner provides. On a workstation that is inconvenient enough that the
// README has always told people to write a .env file instead — so this makes
// the file real. Values already present in the environment are never
// overwritten: an exported NETBOX_URL, or a CI variable, beats a file that
// happens to be lying in the working directory.
package dotenv

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Load reads path and sets every variable it declares that is not already set
// in the environment. It reports the names it set, in file order.
//
// A missing file is not an error — the caller decides whether the path was a
// default worth ignoring or an explicit argument worth failing on.
func Load(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	pairs, err := Parse(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var applied []string
	for _, pair := range pairs {
		if _, set := os.LookupEnv(pair.Key); set {
			continue
		}
		if err := os.Setenv(pair.Key, pair.Value); err != nil {
			return applied, fmt.Errorf("%s: %s: %w", path, pair.Key, err)
		}
		applied = append(applied, pair.Key)
	}
	return applied, nil
}

// Pair is one declaration from the file.
type Pair struct {
	Key   string
	Value string
}

// Parse reads KEY=value lines. Blank lines and lines whose first non-blank
// character is `#` are skipped, a leading `export ` is allowed so the same
// file can be sourced by a shell, and a value may be single- or double-quoted
// to protect spaces, a `#`, or trailing whitespace. An unquoted value ends at
// the first ` #`, which is how the same file reads in a shell.
//
// A line that declares nothing — no `=`, or an empty name — is an error
// naming its number, because the alternative is a typo that silently leaves
// the variable unset and surfaces as "must be set" much later.
func Parse(r io.Reader) ([]Pair, error) {
	var pairs []Pair
	scanner := bufio.NewScanner(r)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")

		name, rawValue, found := strings.Cut(text, "=")
		if !found {
			return nil, fmt.Errorf("line %d: expected KEY=value, got %q", line, scanner.Text())
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("line %d: empty variable name", line)
		}

		pairs = append(pairs, Pair{Key: name, Value: unquote(strings.TrimSpace(rawValue))})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return pairs, nil
}

// unquote returns the value a line declares: what is inside the quotes if it
// opens with one, else the text up to an inline comment.
//
// A quoted value ends at its closing quote and whatever follows is discarded,
// which is what makes `TOKEN="secret"  # rotated May` mean the secret and not
// the secret with quotes and a sentence attached.
func unquote(value string) string {
	for _, quote := range []string{`"`, `'`} {
		if !strings.HasPrefix(value, quote) {
			continue
		}
		if end := strings.Index(value[1:], quote); end >= 0 {
			return value[1 : end+1]
		}
		// An opening quote with no closing one is not a quoted value; fall
		// through and treat the line as unquoted rather than guessing.
	}
	// Unquoted: only a whitespace-preceded `#` starts a comment, so a value
	// that merely contains one (a URL fragment, a token) survives.
	for index := strings.Index(value, "#"); index > 0; {
		if prev := value[index-1]; prev == ' ' || prev == '\t' {
			return strings.TrimSpace(value[:index])
		}
		next := strings.Index(value[index+1:], "#")
		if next < 0 {
			break
		}
		index += next + 1
	}
	return value
}
