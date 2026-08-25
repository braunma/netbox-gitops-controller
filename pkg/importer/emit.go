// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// yamlIndent is the indentation every emitted file uses, matching the
// hand-written inventory.
const yamlIndent = 2

// renderFile renders items to YAML: a header comment, then either a grouped
// `defaults:` + list form (when shared keys were hoisted) or a plain list.
//
// listKey is "items" for definition kinds and "devices" for inventory, the two
// spellings the loader's grouped form accepts. items are the typed models
// (e.g. []*models.Site as []interface{}), encoded in struct-field order so the
// output is deterministic.
func renderFile(header string, listKey string, items []interface{}, opts defaultsOptions) ([]byte, error) {
	if len(items) == 0 {
		// Nothing to render but the header, so a caller that always writes a
		// file for a kind still produces a legible, empty-but-explained file.
		return []byte(headerBlock(header)), nil
	}

	nodes := make([]*yaml.Node, 0, len(items))
	for _, item := range items {
		var n yaml.Node
		if err := n.Encode(item); err != nil {
			return nil, fmt.Errorf("encode item: %w", err)
		}
		// Encode wraps scalars/maps in a document-ish node; for a struct it is
		// a MappingNode directly. Guard anyway.
		if n.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("item did not encode to a mapping (kind %d)", n.Kind)
		}
		nodes = append(nodes, &n)
	}

	defaults := extractDefaults(nodes, opts)

	var root *yaml.Node
	if defaults != nil && len(defaults.Content) > 0 {
		list := &yaml.Node{Kind: yaml.SequenceNode}
		for _, n := range nodes {
			list.Content = append(list.Content, n)
		}
		root = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "defaults"}, defaults,
			&yaml.Node{Kind: yaml.ScalarNode, Value: listKey}, list,
		)
	} else {
		root = &yaml.Node{Kind: yaml.SequenceNode}
		for _, n := range nodes {
			root.Content = append(root.Content, n)
		}
	}

	var body bytes.Buffer
	enc := yaml.NewEncoder(&body)
	enc.SetIndent(yamlIndent)
	if err := enc.Encode(root); err != nil {
		return nil, fmt.Errorf("marshal document: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}

	return append([]byte(headerBlock(header)), body.Bytes()...), nil
}

// headerBlock turns a header string (possibly multi-line, without leading "# ")
// into a YAML comment block ending in a blank line. An empty header yields no
// block, so a caller can opt out.
func headerBlock(header string) string {
	if strings.TrimSpace(header) == "" {
		return ""
	}
	var b strings.Builder
	for _, line := range strings.Split(header, "\n") {
		if line == "" {
			b.WriteString("#\n")
			continue
		}
		b.WriteString("# " + line + "\n")
	}
	b.WriteString("\n")
	return b.String()
}
