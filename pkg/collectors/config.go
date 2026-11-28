// SPDX-License-Identifier: Apache-2.0

package collectors

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sort"
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

	// The keys below belong to scan sources (type "idrac"), which `scan`
	// reads. A scan source has no url: the hosts it reaches are the addresses
	// the inventory declares.

	// UsernameEnv and PasswordEnv are the *names* of the environment variables
	// holding the BMC credentials — never the credentials themselves.
	UsernameEnv string `yaml:"username_env"`
	PasswordEnv string `yaml:"password_env"`
	// Targets says where the list of hosts comes from. The only value is
	// "inventory": the address each selected device declares on Interface.
	Targets string `yaml:"targets"`
	// Interface is the name of the device interface whose address is scanned,
	// e.g. "idrac". It is declared rather than assumed, so the file says which
	// port of a machine a scan talks to.
	Interface string `yaml:"interface"`
	// Select narrows the source to some of the declared devices. Without it
	// the source covers every device that has an address on Interface.
	Select Selector `yaml:"select"`
	// Port is the HTTPS port of the BMC. Zero means 443.
	Port int `yaml:"port"`
	// Concurrency is how many hosts are scanned at once. Zero means
	// DefaultScanConcurrency.
	Concurrency int `yaml:"concurrency"`
}

// Selector picks devices by what the inventory already says about them. Every
// key that is set must match; an empty selector matches every device.
type Selector struct {
	Site string `yaml:"site"`
	Rack string `yaml:"rack"`
	Role string `yaml:"role"`
	// Tags must all be carried by the device.
	Tags []string `yaml:"tags"`
	// Prefix is a CIDR the scanned address must fall into, e.g. the
	// out-of-band management network.
	Prefix string `yaml:"prefix"`
}

// IsZero reports whether the selector selects everything.
func (s Selector) IsZero() bool {
	return s.Site == "" && s.Rack == "" && s.Role == "" && len(s.Tags) == 0 && s.Prefix == ""
}

// TypeIDRAC is the scan source type for Dell iDRACs, read over Redfish.
const TypeIDRAC = "idrac"

// TargetsInventory is the one supported value of a scan source's `targets`.
const TargetsInventory = "inventory"

// DefaultScanConcurrency is how many hosts a scan source reads at once when
// its `concurrency` is not set. MaxScanConcurrency bounds what may be set.
const (
	DefaultScanConcurrency = 5
	MaxScanConcurrency     = 50
)

// scanTypes are the source types `scan` reads. They write hardware facts onto
// devices NetBox already holds; every other type is a `collect` source, whose
// facts are written into the YAML.
var scanTypes = map[string]bool{TypeIDRAC: true}

// IsScan reports whether the source is read by `scan` rather than `collect`.
func (s Source) IsScan() bool {
	return scanTypes[s.Type]
}

// KnownTypes returns every source type either command understands, sorted.
func KnownTypes() []string {
	types := RegisteredTypes()
	for name := range scanTypes {
		types = append(types, name)
	}
	sort.Strings(types)
	return types
}

// Credentials reads a scan source's username and password from the environment
// variables the configuration names.
func (s Source) Credentials() (username, password string, err error) {
	username = strings.TrimSpace(os.Getenv(s.UsernameEnv))
	password = os.Getenv(s.PasswordEnv)
	var missing []string
	if username == "" {
		missing = append(missing, s.UsernameEnv)
	}
	if password == "" {
		missing = append(missing, s.PasswordEnv)
	}
	if len(missing) > 0 {
		return "", "", fmt.Errorf("source %q: environment variable(s) %s empty or unset", s.Name, strings.Join(missing, ", "))
	}
	return username, password, nil
}

// ScanConcurrency is the number of hosts the source scans at once.
func (s Source) ScanConcurrency() int {
	if s.Concurrency > 0 {
		return s.Concurrency
	}
	return DefaultScanConcurrency
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

		if src.IsScan() {
			errs = append(errs, validateScanSource(where, src)...)
		} else {
			// A source with no type yet is checked as a `collect` source, so
			// everything missing is reported in one pass.
			if src.Type == "" {
				errs = append(errs, fmt.Errorf("%s: type is required (known types: %v)", where, KnownTypes()))
			} else if _, ok := registry[src.Type]; !ok {
				errs = append(errs, fmt.Errorf("%s: unknown type %q (known types: %v)", where, src.Type, KnownTypes()))
			}
			if src.URL == "" {
				errs = append(errs, fmt.Errorf("%s: url is required", where))
			}
			if src.TokenEnv == "" {
				errs = append(errs, fmt.Errorf("%s: token_env is required", where))
			}
			if src.Type != "" {
				errs = append(errs, rejectScanKeys(where, src)...)
			}
		}
		if src.TimeoutSeconds < 0 {
			errs = append(errs, fmt.Errorf("%s: timeout_seconds must not be negative", where))
		}
	}

	return errors.Join(errs...)
}

// validateScanSource checks the keys of a scan source.
func validateScanSource(where string, src Source) []error {
	var errs []error
	if src.UsernameEnv == "" {
		errs = append(errs, fmt.Errorf("%s: username_env is required (name the environment variable holding the username)", where))
	}
	if src.PasswordEnv == "" {
		errs = append(errs, fmt.Errorf("%s: password_env is required (name the environment variable holding the password)", where))
	}
	if src.Targets != TargetsInventory {
		errs = append(errs, fmt.Errorf("%s: targets must be %q — the hosts to scan are the addresses the inventory declares", where, TargetsInventory))
	}
	if src.Interface == "" {
		errs = append(errs, fmt.Errorf("%s: interface is required (the device interface whose address is scanned, e.g. \"idrac\")", where))
	}
	if src.Select.Prefix != "" {
		if _, err := netip.ParsePrefix(src.Select.Prefix); err != nil {
			errs = append(errs, fmt.Errorf("%s: select.prefix %q is not a CIDR such as 192.0.2.0/24", where, src.Select.Prefix))
		}
	}
	if src.Port < 0 || src.Port > 65535 {
		errs = append(errs, fmt.Errorf("%s: port must be between 1 and 65535", where))
	}
	if src.Concurrency < 0 || src.Concurrency > MaxScanConcurrency {
		errs = append(errs, fmt.Errorf("%s: concurrency must be between 1 and %d", where, MaxScanConcurrency))
	}
	// A scan source reaches the hosts the inventory names, with a username
	// and a password. A url, a token or a cluster on it would be read by
	// nothing, and a key that is read by nothing is a misunderstanding.
	for key, set := range map[string]bool{"url": src.URL != "", "token_env": src.TokenEnv != "", "cluster": src.Cluster != ""} {
		if set {
			errs = append(errs, fmt.Errorf("%s: %s does not apply to a source of type %q", where, key, src.Type))
		}
	}
	sortErrors(errs)
	return errs
}

// rejectScanKeys reports scan-only keys set on a `collect` source.
func rejectScanKeys(where string, src Source) []error {
	set := map[string]bool{
		"username_env": src.UsernameEnv != "",
		"password_env": src.PasswordEnv != "",
		"targets":      src.Targets != "",
		"interface":    src.Interface != "",
		"select":       !src.Select.IsZero(),
		"port":         src.Port != 0,
		"concurrency":  src.Concurrency != 0,
	}
	var errs []error
	for key, isSet := range set {
		if isSet {
			errs = append(errs, fmt.Errorf("%s: %s does not apply to a source of type %q", where, key, src.Type))
		}
	}
	sortErrors(errs)
	return errs
}

// sortErrors orders errors by message, so a run reports them the same way
// every time.
func sortErrors(errs []error) {
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
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
