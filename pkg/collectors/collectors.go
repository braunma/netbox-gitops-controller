// SPDX-License-Identifier: Apache-2.0

// Package collectors holds the compiled-in source adapters: the things that
// reach out to a live system, read what is there, and hand back a
// discovery.Snapshot.
//
// Every collector is read-only against its source, and no collector has a path
// to NetBox. A collector's only effect on the world is the Snapshot it returns;
// what happens to that Snapshot — matching it against the declared inventory,
// writing YAML, opening a merge request — is somebody else's job.
package collectors

import (
	"context"
	"fmt"
	"sort"

	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// Collector reads one configured source.
type Collector interface {
	// Name is the source name from collectors.yaml. It identifies the source
	// in log lines, in the run summary and in generated file paths.
	Name() string
	// Collect reads the source. It must make no write request of any kind, and
	// must honour ctx so a hung source cannot stall a pipeline.
	Collect(ctx context.Context) (*discovery.Snapshot, error)
}

// Factory builds a collector from one `sources:` entry. Registering a type is
// how a new compiled-in source becomes usable from configuration.
type Factory func(src Source, logger *utils.Logger) (Collector, error)

// registry maps a source `type:` to its factory.
var registry = map[string]Factory{}

// Register adds a source type to the registry. It is meant for package init
// functions, and panics on a duplicate: two factories claiming one type name is
// a programming error that must not be resolved by whichever init ran last.
func Register(typeName string, factory Factory) {
	if _, dup := registry[typeName]; dup {
		panic(fmt.Sprintf("collectors: source type %q registered twice", typeName))
	}
	registry[typeName] = factory
}

// RegisteredTypes returns the known source types, sorted, for error messages
// and `--help`.
func RegisteredTypes() []string {
	types := make([]string, 0, len(registry))
	for name := range registry {
		types = append(types, name)
	}
	sort.Strings(types)
	return types
}

// New builds the collector for one configured source.
func New(src Source, logger *utils.Logger) (Collector, error) {
	factory, ok := registry[src.Type]
	if !ok {
		return nil, fmt.Errorf("source %q: unknown type %q (known types: %v)", src.Name, src.Type, RegisteredTypes())
	}
	return factory(src, logger)
}

// Build returns one collector per configured source, in configuration order.
// A source whose type is unknown or whose settings are incomplete fails the
// whole build rather than being skipped: a scan that silently covers half the
// estate would show up as objects "disappearing" from the discovered files.
func Build(cfg *Config, logger *utils.Logger) ([]Collector, error) {
	built := make([]Collector, 0, len(cfg.Sources))
	for _, src := range cfg.Sources {
		c, err := New(src, logger)
		if err != nil {
			return nil, err
		}
		built = append(built, c)
	}
	return built, nil
}
