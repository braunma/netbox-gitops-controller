// SPDX-License-Identifier: Apache-2.0

package dotenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []Pair
	}{
		{
			name:  "plain assignment",
			input: "NETBOX_URL=https://netbox.example.com\n",
			want:  []Pair{{"NETBOX_URL", "https://netbox.example.com"}},
		},
		{
			name:  "comments and blank lines are skipped",
			input: "# a comment\n\n   \n  # indented comment\nA=1\n",
			want:  []Pair{{"A", "1"}},
		},
		{
			name:  "a leading export is allowed",
			input: "export NETBOX_TOKEN=abc123\n",
			want:  []Pair{{"NETBOX_TOKEN", "abc123"}},
		},
		{
			name:  "whitespace around the name and value is trimmed",
			input: "  A =  1  \n",
			want:  []Pair{{"A", "1"}},
		},
		{
			name:  "quotes protect spaces and are stripped",
			input: "A=\"a value\"\nB='another one'\n",
			want:  []Pair{{"A", "a value"}, {"B", "another one"}},
		},
		{
			name:  "an inline comment ends an unquoted value",
			input: "A=value # trailing note\n",
			want:  []Pair{{"A", "value"}},
		},
		{
			name:  "a hash inside a value survives when it is not a comment",
			input: "A=https://example.com/page#section\nB=\"quoted # hash\"\n",
			want:  []Pair{{"A", "https://example.com/page#section"}, {"B", "quoted # hash"}},
		},
		{
			name:  "an empty value is allowed",
			input: "A=\n",
			want:  []Pair{{"A", ""}},
		},
		{
			name:  "a value may contain = signs",
			input: "A=key=value\n",
			want:  []Pair{{"A", "key=value"}},
		},
		{
			name:  "the last line needs no newline",
			input: "A=1",
			want:  []Pair{{"A", "1"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(tc.input))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("pair %d: got %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// A line that declares nothing is a typo, and silence would surface much later
// as "NETBOX_URL must be set" with no hint where to look.
func TestParseRejectsLinesThatDeclareNothing(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantInErr string
	}{
		{"no equals sign", "NETBOX_URL https://netbox.example.com\n", "line 1"},
		{"empty name", "=value\n", "empty variable name"},
		{"error names the offending line", "A=1\nB=2\noops\n", "line 3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tc.input))
			if err == nil {
				t.Fatalf("expected an error for %q", tc.input)
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("error %q should mention %q", err, tc.wantInErr)
			}
		})
	}
}

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSetsVariables(t *testing.T) {
	path := writeEnv(t, "DOTENV_TEST_URL=https://netbox.example.com\nDOTENV_TEST_TOKEN=abc\n")
	t.Setenv("DOTENV_TEST_URL", "")
	os.Unsetenv("DOTENV_TEST_URL")
	t.Setenv("DOTENV_TEST_TOKEN", "")
	os.Unsetenv("DOTENV_TEST_TOKEN")

	applied, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(applied) != 2 {
		t.Errorf("got %v, want both names reported", applied)
	}
	if got := os.Getenv("DOTENV_TEST_URL"); got != "https://netbox.example.com" {
		t.Errorf("DOTENV_TEST_URL=%q", got)
	}
	if got := os.Getenv("DOTENV_TEST_TOKEN"); got != "abc" {
		t.Errorf("DOTENV_TEST_TOKEN=%q", got)
	}
}

// A CI variable, or anything the caller exported, has to win: otherwise a
// stale .env in the working directory would silently retarget a pipeline.
func TestLoadDoesNotOverrideTheEnvironment(t *testing.T) {
	path := writeEnv(t, "DOTENV_TEST_URL=https://from-file.example.com\nDOTENV_TEST_OTHER=file\n")
	t.Setenv("DOTENV_TEST_URL", "https://from-environment.example.com")
	t.Setenv("DOTENV_TEST_OTHER", "")
	os.Unsetenv("DOTENV_TEST_OTHER")

	applied, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := os.Getenv("DOTENV_TEST_URL"); got != "https://from-environment.example.com" {
		t.Errorf("the environment must win, got %q", got)
	}
	if len(applied) != 1 || applied[0] != "DOTENV_TEST_OTHER" {
		t.Errorf("got %v, want only the name that was actually set", applied)
	}
}

// An exported-but-empty variable counts as set: unsetting it is how a caller
// says "ignore the file's value", and LookupEnv is what tells them apart.
func TestLoadTreatsAnEmptyExportedValueAsSet(t *testing.T) {
	path := writeEnv(t, "DOTENV_TEST_EMPTY=from-file\n")
	t.Setenv("DOTENV_TEST_EMPTY", "")

	if _, err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := os.Getenv("DOTENV_TEST_EMPTY"); got != "" {
		t.Errorf("got %q, want the exported empty value to stand", got)
	}
}

func TestLoadReportsAMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent"))
	if !os.IsNotExist(err) {
		t.Errorf("got %v, want an os.IsNotExist error the caller can recognise", err)
	}
}

func TestLoadErrorNamesTheFile(t *testing.T) {
	path := writeEnv(t, "nonsense\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q should name the file", err)
	}
}

// A quoted value may be followed by a comment. Stripping the comment before
// the quotes would leave the quotes in the value — which is how a token
// arrives at NetBox as `"nbt_…"` and comes back 403.
func TestParseQuotedValueFollowedByComment(t *testing.T) {
	cases := map[string]string{
		`TOKEN="secret"   # rotated in May`: "secret",
		`TOKEN='secret'   # rotated in May`: "secret",
		`TOKEN="secret"`:                    "secret",
		`TOKEN="with space"  # note`:        "with space",
		`TOKEN="has#hash"  # note`:          "has#hash",
		`TOKEN=bare   # note`:               "bare",
		`TOKEN=frag#ment`:                   "frag#ment",
		`TOKEN="unterminated`:               `"unterminated`,
	}
	for line, want := range cases {
		pairs, err := Parse(strings.NewReader(line))
		if err != nil {
			t.Errorf("%s: %v", line, err)
			continue
		}
		if len(pairs) != 1 || pairs[0].Value != want {
			t.Errorf("%s -> %q, want %q", line, pairs[0].Value, want)
		}
	}
}

// A `#` inside the value does not end it, but a later whitespace-preceded one
// still starts a comment.
func TestParseHashInsideValueThenComment(t *testing.T) {
	pairs, err := Parse(strings.NewReader(`URL=https://host/path#frag  # the anchor matters`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pairs[0].Value, "https://host/path#frag"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
