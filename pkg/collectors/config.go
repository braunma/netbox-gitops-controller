// SPDX-License-Identifier: Apache-2.0

package collectors

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultConfigFile is where --collectors-config looks when it is not given a
// path. A missing file there is ordinary: a repository that ingests only
// external JSON has no collectors to configure.
const DefaultConfigFile = "collectors.yaml"

// Config is the parsed collectors.yaml.
type Config struct {
	Sources []Source `yaml:"sources"`
}

// Source is one entry under `sources:`.
//
// No secret is ever written here. The file names the environment variable that
// holds the token, so the configuration is safe to commit and the token comes
// from the CI variable store like every other credential this project uses.
type Source struct {
	// Name identifies the source in logs, in --source, in the run summary and
	// in the path of any file generated from it.
	Name string `yaml:"name"`
	// Type selects the collector implementation, e.g. "proxmox".
	Type string `yaml:"type"`
	// URL is the source's API base URL.
	URL string `yaml:"url"`
	// TokenEnv is the *name* of the environment variable holding the API
	// token — never the token itself.
	TokenEnv string `yaml:"token_env"`
	// VerifyTLS defaults to true. Turning it off is per-source and loud, the
	// same bargain IGNORE_SSL_ERRORS strikes for the NetBox client: one lab
	// instance behind a self-signed certificate must not quietly disable
	// verification everywhere else.
	VerifyTLS *bool `yaml:"verify_tls"`
	// TimeoutSeconds bounds a single request. Zero means the collector's own
	// default.
	TimeoutSeconds int `yaml:"timeout_seconds"`
	// Cluster names the NetBox cluster the source's guests belong to. Proxmox
	// does not report a name a NetBox cluster can be matched on, so it is
	// declared here rather than guessed.
	Cluster string `yaml:"cluster"`
}

// ShouldVerifyTLS reports whether certificates are checked for this source.
func (s Source) ShouldVerifyTLS() bool {
	return s.VerifyTLS == nil || *s.VerifyTLS
}

// Token reads the source's API token from the environment variable the
// configuration names. An unset variable is an error naming the variable, so
// the fix is obvious from the message.
func (s Source) Token() (string, error) {
	if s.TokenEnv == "" {
		return "", fmt.Errorf("source %q: token_env is required (name the environment variable holding the token, not the token)", s.Name)
	}
	token := strings.TrimSpace(os.Getenv(s.TokenEnv))
	if token == "" {
		return "", fmt.Errorf("source %q: environment variable %s is empty or unset", s.Name, s.TokenEnv)
	}
	return token, nil
}

// LoadConfig reads and validates a collectors.yaml.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the path is operator-supplied configuration
	if err != nil {
		return nil, err
	}

	var cfg Config
	// Unknown keys are rejected: a mistyped `verify_tsl: false` that is
	// silently dropped leaves an operator believing verification is off when
	// it is on, or the reverse.
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

// Validate reports everything wrong with the configuration at once.
func (c *Config) Validate() error {
	var errs []error
	seen := make(map[string]bool, len(c.Sources))

	for i, src := range c.Sources {
		where := fmt.Sprintf("source %d", i+1)
		if src.Name != "" {
			where = fmt.Sprintf("source %q", src.Name)
		}

		if src.Name == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", where))
		} else if seen[src.Name] {
			// Two sources of one name would write over each other's generated
			// files and make --source ambiguous.
			errs = append(errs, fmt.Errorf("%s: declared twice; source names must be unique", where))
		} else if strings.ContainsAny(src.Name, `/\`) || src.Name == "." || src.Name == ".." {
			// The name becomes a directory under inventory/discovered/.
			errs = append(errs, fmt.Errorf("%s: name must not contain a path separator", where))
		}
		seen[src.Name] = true

		if src.Type == "" {
			errs = append(errs, fmt.Errorf("%s: type is required (known types: %v)", where, RegisteredTypes()))
		} else if _, ok := registry[src.Type]; !ok {
			errs = append(errs, fmt.Errorf("%s: unknown type %q (known types: %v)", where, src.Type, RegisteredTypes()))
		}
		if src.URL == "" {
			errs = append(errs, fmt.Errorf("%s: url is required", where))
		}
		if src.TokenEnv == "" {
			errs = append(errs, fmt.Errorf("%s: token_env is required", where))
		}
		if src.TimeoutSeconds < 0 {
			errs = append(errs, fmt.Errorf("%s: timeout_seconds must not be negative", where))
		}
	}

	return errors.Join(errs...)
}

// Select returns the sources named by --source, or every source when no name
// was given. A name that matches nothing is an error listing what is
// configured, rather than a run that quietly scans nothing.
func (c *Config) Select(names []string) ([]Source, error) {
	if len(names) == 0 {
		return c.Sources, nil
	}

	byName := make(map[string]Source, len(c.Sources))
	for _, src := range c.Sources {
		byName[src.Name] = src
	}

	selected := make([]Source, 0, len(names))
	var errs []error
	for _, name := range names {
		src, ok := byName[name]
		if !ok {
			errs = append(errs, fmt.Errorf("no source named %q is configured (configured: %s)", name, strings.Join(c.SourceNames(), ", ")))
			continue
		}
		selected = append(selected, src)
	}
	return selected, errors.Join(errs...)
}

// SourceNames returns the configured source names in file order.
func (c *Config) SourceNames() []string {
	names := make([]string, 0, len(c.Sources))
	for _, src := range c.Sources {
		names = append(names, src.Name)
	}
	return names
}

// EnvIsTrue reports whether an environment variable is set to something that
// means yes, matching how the NetBox client reads IGNORE_SSL_ERRORS: what
// strconv.ParseBool accepts, plus "yes" and "on".
func EnvIsTrue(name string) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	switch value {
	case "":
		return false
	case "yes", "on":
		return true
	}
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}
