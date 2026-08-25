// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"gopkg.in/yaml.v3"
)

// defaultsOptions controls per-file defaults extraction.
type defaultsOptions struct {
	// enabled turns extraction on. When off, every item keeps every key.
	enabled bool
	// minItems is the fewest items a file must hold before any key is hoisted.
	// A default shared by two lines saves little and reads worse than repeating
	// it, so the floor is 3 by default.
	minItems int
}

// neverHoist are keys a shared default must never absorb: object identities,
// and the structural/nested keys that describe one object's own components.
// Hoisting an identity would collapse distinct objects; hoisting a component
// list would make an item that omits it silently inherit another's ports.
var neverHoist = map[string]bool{
	// identities
	"name": true, "slug": true, "vid": true, "prefix": true, "model": true,
	"address": true, "rd": true, "rename_from": true,
	// per-object structural / nested
	"interfaces": true, "modules": true, "front_ports": true, "rear_ports": true,
	"device_bays": true, "module_bays": true, "console_ports": true,
	"console_server_ports": true, "power_ports": true, "power_outlets": true,
	"custom_fields": true, "parent_device": true, "device_bay": true,
	"position": true, "serial": true, "asset_tag": true,
}

// extractDefaults hoists every key that is present in every item with an
// identical value into a returned defaults mapping node, removing it from each
// item. It returns nil when nothing qualifies (extraction off, too few items,
// or no shared key).
//
// The rule is deliberately the conservative one. A key is hoisted only when it
// is present in every item — and because the models marshal with omitempty, a
// zero-valued field is simply absent, so this automatically refuses to hoist a
// key any item leaves at its zero value. That is the invariant that keeps a
// hoisted default from silently teleporting an object (a device that omits
// rack_slug must not inherit another file's rack), and it applies uniformly to
// strings, numbers, booleans and tag lists alike: identical-across-all, or not
// hoisted.
func extractDefaults(items []*yaml.Node, opts defaultsOptions) *yaml.Node {
	if !opts.enabled || len(items) < opts.minItems || len(items) == 0 {
		return nil
	}

	// Candidate keys, in the order the first item declares them, so the emitted
	// defaults block follows the struct's field order rather than map order.
	first := items[0]
	var candidates []string
	for i := 0; i+1 < len(first.Content); i += 2 {
		key := first.Content[i].Value
		if neverHoist[key] {
			continue
		}
		candidates = append(candidates, key)
	}

	defaults := &yaml.Node{Kind: yaml.MappingNode}
	hoisted := map[string]bool{}
	for _, key := range candidates {
		val, ok := sharedValue(items, key)
		if !ok {
			continue
		}
		defaults.Content = append(defaults.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			val)
		hoisted[key] = true
	}

	if len(hoisted) == 0 {
		return nil
	}

	for _, item := range items {
		removeKeys(item, hoisted)
	}
	return defaults
}

// sharedValue returns the value node for key when every item carries key with
// an identical rendering, and a copy of that value node to place in defaults.
func sharedValue(items []*yaml.Node, key string) (*yaml.Node, bool) {
	var canonical string
	var valNode *yaml.Node
	for i, item := range items {
		v := mappingValueNode(item, key)
		if v == nil {
			return nil, false // absent in this item (incl. zero value) → cannot hoist
		}
		rendered, err := renderNode(v)
		if err != nil {
			return nil, false
		}
		if i == 0 {
			canonical = rendered
			valNode = v
		} else if rendered != canonical {
			return nil, false
		}
	}
	return valNode, true
}

// mappingValueNode returns the value node for a key in a mapping node, or nil.
func mappingValueNode(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// removeKeys deletes the named keys from a mapping node.
func removeKeys(m *yaml.Node, keys map[string]bool) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	kept := make([]*yaml.Node, 0, len(m.Content))
	for i := 0; i+1 < len(m.Content); i += 2 {
		if keys[m.Content[i].Value] {
			continue
		}
		kept = append(kept, m.Content[i], m.Content[i+1])
	}
	m.Content = kept
}

// renderNode marshals a node to its canonical YAML text, for equality checks.
func renderNode(n *yaml.Node) (string, error) {
	b, err := yaml.Marshal(n)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DefaultsOptions builds the defaults-extraction settings for Options.Defaults.
// It is the exported constructor for the unexported options value, so callers
// (including the command and external tests) configure extraction without the
// package having to export the field's type.
func DefaultsOptions(enabled bool, minItems int) defaultsOptions {
	if minItems < 1 {
		minItems = 1
	}
	return defaultsOptions{enabled: enabled, minItems: minItems}
}
