// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// DeviceFolders and VMFolders are every folder of the data directory that
// declares concrete objects, in load order.
//
// inventory/discovered/ is where fact ingestion writes what it found. It sits
// beside the hand-written folders and mirrors their split, so a generated VM is
// loaded like any VM and a completed skeleton is loaded like any device the
// moment its parking underscore comes off — no special case anywhere in the
// reconciler.
var (
	DeviceFolders = []string{
		"inventory/hardware/active",
		"inventory/hardware/passive",
		"inventory/discovered/hardware",
	}
	VMFolders = []string{
		"inventory/virtual",
		"inventory/discovered/virtual",
	}
)

// Provenance records where a declared object came from: which file, and which
// node inside it.
//
// The reconciler never needs this — it only cares what the YAML says. Fact
// ingestion does: to write an observed serial into the file a human wrote, it
// has to find that human's block again, and edit it in place rather than
// re-emitting the file and losing every comment in it.
type Provenance struct {
	// File is the path the object was read from, as the loader saw it
	// (data-dir relative to the process, not to the data directory).
	File string
	// Doc is the document node the object's node belongs to. Every object read
	// from one file shares it, which is what lets an editor find the whole
	// file's structure from any one object in it.
	Doc *yaml.Node
	// Node is the object's own mapping node. Editing through it preserves the
	// rest of the document byte for byte.
	Node *yaml.Node
	// Index is the object's zero-based position among the file's objects, for
	// error messages.
	Index int
	// Parked is true when the file matches an ignore pattern, so the object is
	// declared in the repository but is not synced to NetBox. Ingest still has
	// to know about it: an object parked here must not be generated a second
	// time somewhere else.
	Parked bool
}

// Ref returns a short human-readable reference to the declaration, for errors.
func (p Provenance) Ref() string {
	if p.Node != nil && p.Node.Line > 0 {
		return fmt.Sprintf("%s:%d", p.File, p.Node.Line)
	}
	return p.File
}

// Declared is a loaded object together with where it was declared.
type Declared[T any] struct {
	Object T
	Prov   Provenance
}

// Inventory is the declared inventory with provenance attached: everything
// fact ingestion has to match against, and everything it may write into.
type Inventory struct {
	Devices []Declared[*models.DeviceConfig]
	VMs     []Declared[*models.VMConfig]
}

// LoadInventoryWithProvenance loads the device and VM inventory, recording for
// each object the file and node it came from.
//
// Unlike the reconciler's load, this one deliberately includes parked files.
// A parked object is still declared in this repository, and ingest must see it
// or it would generate a second copy of a device somebody has already started
// documenting — including the skeletons a previous ingest run wrote itself.
// Parked objects are marked as such and are not model-validated: a skeleton
// exists precisely because its required fields are not filled in yet.
func (dl *DataLoader) LoadInventoryWithProvenance() (Inventory, error) {
	var inv Inventory
	var errs []error

	for _, folder := range DeviceFolders {
		devices, err := loadDeclared[*models.DeviceConfig](dl, folder)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		inv.Devices = append(inv.Devices, devices...)
	}

	for _, folder := range VMFolders {
		vms, err := loadDeclared[*models.VMConfig](dl, folder)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		inv.VMs = append(inv.VMs, vms...)
	}

	if err := errors.Join(errs...); err != nil {
		return Inventory{}, err
	}
	return inv, nil
}

// loadDeclared reads every YAML file under a folder and returns its objects
// with provenance. A missing folder yields nothing, matching loadKind.
func loadDeclared[T models.Validator](dl *DataLoader, folder string) ([]Declared[T], error) {
	targetDir := filepath.Join(dl.basePath, folder)
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		return nil, nil
	}

	files, err := dl.findYAMLFiles(targetDir)
	if err != nil {
		return nil, fmt.Errorf("failed to find YAML files in %s: %w", targetDir, err)
	}

	var declared []Declared[T]
	for _, file := range files {
		items, err := declaredFromFile[T](file, dl.isIgnored(file))
		if err != nil {
			return nil, err
		}
		declared = append(declared, items...)
	}
	return declared, nil
}

// declaredFromFile parses one file into typed objects plus their nodes.
func declaredFromFile[T models.Validator](path string, parked bool) ([]Declared[T], error) {
	content, err := os.ReadFile(path) // #nosec G304 -- inventory path from the data directory
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		// A file of nothing but comments. Not an error; it declares nothing.
		return nil, nil
	}

	itemNodes, defaultsNode, err := ItemNodes(path, doc.Content[0])
	if err != nil {
		return nil, err
	}

	// The file's defaults, decoded once, so every item of the file merges
	// against the same map.
	var defaults map[string]interface{}
	if defaultsNode != nil {
		if err := defaultsNode.Decode(&defaults); err != nil {
			return nil, fmt.Errorf("%s: failed to read the defaults block: %w", path, err)
		}
	}

	declared := make([]Declared[T], 0, len(itemNodes))
	var errs []error
	for i, node := range itemNodes {
		var raw map[string]interface{}
		if err := node.Decode(&raw); err != nil {
			errs = append(errs, fmt.Errorf("%s: entry %d: expected a mapping: %w", path, i+1, err))
			continue
		}

		object, err := decodeMerged[T](mergeDefaults(defaults, raw))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: entry %d: %w", path, i+1, err))
			continue
		}

		// A parked object is a work in progress by definition; validating it
		// would reject the very skeletons this exists to keep track of.
		if !parked {
			if err := object.Validate(); err != nil {
				errs = append(errs, fmt.Errorf("%s: entry %d: %w", path, i+1, err))
				continue
			}
		}

		declared = append(declared, Declared[T]{
			Object: object,
			Prov: Provenance{
				File:   path,
				Doc:    &doc,
				Node:   node,
				Index:  i,
				Parked: parked,
			},
		})
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return declared, nil
}

// decodeMerged turns a defaults-merged mapping into the typed model.
func decodeMerged[T models.Validator](raw map[string]interface{}) (T, error) {
	var zero T
	data, err := yaml.Marshal(raw)
	if err != nil {
		return zero, err
	}
	var object T
	if err := yaml.Unmarshal(data, &object); err != nil {
		return zero, err
	}
	return object, nil
}

// ItemNodes returns the object nodes of a document, and the file's `defaults`
// mapping if it has one. Three top-level document shapes are accepted:
//
//  1. A list of mappings (the classic form).
//  2. A single mapping, treated as a one-element list (e.g. one device type
//     per file).
//  3. A grouped form with an optional `defaults` mapping and a list under
//     either `devices` or its synonym `items`. The defaults are merged into
//     every entry: entry values win over defaults, and `tags` are combined
//     (union) instead of replaced. This form exists so a file doesn't have to
//     repeat shared fields (site_slug, role_slug, rack_slug, tags, ...) on
//     every entry. `devices` reads naturally for inventory; `items` reads
//     naturally for definition kinds (sites, roles, ...) that a reverse
//     import writes. A file uses one spelling or the other, never both.
//
// It is exported because anything that rewrites an inventory file has to split
// it into objects by exactly these rules, or it would edit a different block
// than the loader read.
func ItemNodes(path string, root *yaml.Node) ([]*yaml.Node, *yaml.Node, error) {
	switch root.Kind {
	case yaml.SequenceNode:
		return root.Content, nil, nil

	case yaml.MappingNode:
		// The grouped form's list may be spelled `devices:` (the original,
		// natural for inventory) or `items:` (its synonym, natural for
		// definition kinds like sites or roles that a reverse import writes).
		// They are exact synonyms; a file may use one or the other, never both.
		devices := mappingValue(root, "devices")
		items := mappingValue(root, "items")
		if devices != nil && items != nil {
			return nil, nil, fmt.Errorf("%s: use either 'devices' or 'items' for the grouped list, not both", path)
		}
		listKey, list := "devices", devices
		if items != nil {
			listKey, list = "items", items
		}
		if list == nil {
			// A single object per file.
			return []*yaml.Node{root}, nil, nil
		}
		// Grouped form. Only defaults and the chosen list key may appear, so a
		// stray field next to it fails here as loudly as it does in the
		// reconciler's loader rather than being silently dropped.
		for i := 0; i < len(root.Content)-1; i += 2 {
			if key := root.Content[i].Value; key != "defaults" && key != listKey {
				return nil, nil, fmt.Errorf("%s: grouped form only allows 'defaults' and %q at the top level, found %q", path, listKey, key)
			}
		}
		if list.Kind != yaml.SequenceNode {
			return nil, nil, fmt.Errorf("%s: %q must be a list", path, listKey)
		}
		return list.Content, mappingValue(root, "defaults"), nil

	default:
		// An explicit empty document (`---` or `null`) declares nothing, like a
		// file of nothing but comments.
		if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("%s: expected a list or a mapping at the top level", path)
	}
}

// mappingValue returns the value node for a key of a mapping node, or nil.
func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}
