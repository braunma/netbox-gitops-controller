// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"

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
	var errs []error
	for _, file := range files {
		items, err := declaredFromFile[T](file, dl.isIgnored(file))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		declared = append(declared, items...)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return declared, nil
}

// declaredFromFile parses one file into typed objects plus their nodes. Every
// problem in the file is reported, each on its own line prefixed with the file,
// the entry and the object (see errors.go).
func declaredFromFile[T models.Validator](path string, parked bool) ([]Declared[T], error) {
	file := displayPath(path)

	content, err := os.ReadFile(path) // #nosec G304 -- inventory path from the data directory
	if err != nil {
		return nil, fmt.Errorf("%s: cannot be read: %w", file, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("%s: is not valid YAML: %w", file, err)
	}
	if len(doc.Content) == 0 {
		// A file of nothing but comments. Not an error; it declares nothing.
		return nil, nil
	}

	itemNodes, defaultsNode, err := ItemNodes(file, doc.Content[0])
	if err != nil {
		return nil, err
	}

	// The file's defaults, decoded and normalized once, so every item of the
	// file merges against the same map. Per-interface defaults are taken out
	// first: they are keyed by interface name, not shaped like the model.
	modelType := reflect.TypeOf((*T)(nil)).Elem()
	kind := models.KindOf(reflect.New(modelType.Elem()).Interface().(models.Validator))
	var defaults map[string]interface{}
	var ifaceDefaults map[string]map[string]interface{}
	var alwaysIfaces map[string]bool
	var errs []error
	if defaultsNode != nil {
		if err := defaultsNode.Decode(&defaults); err != nil {
			return nil, prefixed(file+": defaults: must be a mapping of fields", err)
		}
		if ifaceDefaults, alwaysIfaces, err = interfaceDefaults(defaults, modelType); err != nil {
			errs = append(errs, err)
		}
		if err := normalizeFields(defaults, modelType, "defaults"); err != nil {
			errs = append(errs, err)
		}
	}
	// A broken defaults block would turn into a problem on every entry, so it
	// is reported on its own and the entries are not looked at.
	if len(errs) > 0 {
		return nil, prefixed(file, errors.Join(errs...))
	}
	usedIfaceDefaults := make(map[string]bool, len(ifaceDefaults))

	declared := make([]Declared[T], 0, len(itemNodes))
	for i, node := range itemNodes {
		// The line is the entry's own, so a problem in a long file can be
		// jumped to; the entry number stays for files where it is spoken of.
		entry := fmt.Sprintf("%s:%d: entry %d", file, node.Line, i+1)
		var raw map[string]interface{}
		if err := node.Decode(&raw); err != nil {
			errs = append(errs, fmt.Errorf("%s: must be a mapping of fields", entry))
			continue
		}
		label := objectLabel(kind, raw)
		// `interfaces:` may be a mapping keyed by name; the model wants a list.
		if err := widenInterfaceMap(raw); err != nil {
			errs = append(errs, prefixed(entry+": "+label, err))
			continue
		}
		// Normalized before merging, so an item's short `site:` overrides a
		// default `site_slug:` instead of the two colliding.
		if err := normalizeFields(raw, modelType, ""); err != nil {
			errs = append(errs, prefixed(entry+": "+label, err))
			continue
		}
		// `Eth1/{1-48}` and the like become one entry per number, after the
		// keys are checked (once, on the one entry) and before decoding.
		if err := expandRanges(raw, ""); err != nil {
			errs = append(errs, prefixed(entry+": "+label, err))
			continue
		}
		merged := mergeDefaults(defaults, raw)
		applyInterfaceDefaults(merged, ifaceDefaults, alwaysIfaces, usedIfaceDefaults)

		object, err := decodeMerged[T](merged)
		if err != nil {
			errs = append(errs, prefixed(entry+": "+label, err))
			continue
		}

		// A parked object is a work in progress by definition; validating it
		// would reject the very skeletons this exists to keep track of.
		if !parked {
			if err := object.Validate(); err != nil {
				errs = append(errs, prefixed(entry, err))
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

	// A per-interface default that no listed interface picked up is a typo in
	// its name, or a leftover: either way it does nothing, so say so. Skipped
	// when entries failed, since the interface that would have used it may be
	// among them.
	if len(errs) == 0 {
		for _, name := range sortedKeys(ifaceDefaults) {
			if !usedIfaceDefaults[name] {
				errs = append(errs, fmt.Errorf("%s: defaults.interfaces.%s matches no interface listed in this file", file, name))
			}
		}
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return declared, nil
}

// decodeMerged turns a defaults-merged mapping into the typed model. A value
// of the wrong type is reported per field, by its path (`interfaces[1].mtu`),
// since the line numbers the decoder knows are those of the re-serialized
// mapping, not of the file.
func decodeMerged[T models.Validator](raw map[string]interface{}) (T, error) {
	var zero T
	data, err := yaml.Marshal(raw)
	if err != nil {
		return zero, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return zero, err
	}
	var object T
	if err := doc.Decode(&object); err != nil {
		var typeErr *yaml.TypeError
		if errors.As(err, &typeErr) {
			return zero, fieldTypeErrors(typeErr, &doc)
		}
		return zero, err
	}
	return object, nil
}

// typeErrorLine matches the decoder's per-field message, `line 3: cannot
// unmarshal !!str `big` into int`.
var typeErrorLine = regexp.MustCompile(`^line (\d+): (.*)$`)

// fieldTypeErrors rewrites each message of a decoder type error to name the
// field by its path instead of by the line of the serialized document it was
// decoded from.
func fieldTypeErrors(typeErr *yaml.TypeError, doc *yaml.Node) error {
	paths := map[int]string{}
	var walk func(n *yaml.Node, path string)
	walk = func(n *yaml.Node, path string) {
		// A child is recorded after its parent, so a line shared by both
		// (a block sequence and its first item) names the more precise one.
		if path != "" {
			paths[n.Line] = path
		}
		switch n.Kind {
		case yaml.DocumentNode:
			for _, c := range n.Content {
				walk(c, path)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				walk(n.Content[i+1], joinPath(path, n.Content[i].Value))
			}
		case yaml.SequenceNode:
			for i, c := range n.Content {
				walk(c, fmt.Sprintf("%s[%d]", path, i+1))
			}
		}
	}
	walk(doc, "")

	errs := make([]error, 0, len(typeErr.Errors))
	for _, msg := range typeErr.Errors {
		if m := typeErrorLine.FindStringSubmatch(msg); m != nil {
			if line, err := strconv.Atoi(m[1]); err == nil {
				if path, ok := paths[line]; ok {
					errs = append(errs, fmt.Errorf("%s: %s", path, m[2]))
					continue
				}
			}
		}
		errs = append(errs, errors.New(msg))
	}
	return errors.Join(errs...)
}

// ItemNodes returns the object nodes of a document, and the file's `defaults`
// mapping if it has one. Three top-level document shapes are accepted:
//
//  1. A list of mappings (the classic form).
//  2. A single mapping, treated as a one-element list (e.g. one device type
//     per file).
//  3. A grouped form with an optional `defaults` mapping and an `items`
//     list (`devices` is accepted as an alias, the name the form had when it
//     existed only for inventory). The defaults are merged into every entry:
//     entry values win over defaults, and `tags` are combined (union) instead
//     of replaced. This form exists so files don't have to repeat shared
//     fields (site, role, rack, group, ...) on every
//     object, whatever its kind.
//
// It is exported because anything that rewrites an inventory file has to split
// it into objects by exactly these rules, or it would edit a different block
// than the loader read.
func ItemNodes(path string, root *yaml.Node) ([]*yaml.Node, *yaml.Node, error) {
	switch root.Kind {
	case yaml.SequenceNode:
		return root.Content, nil, nil

	case yaml.MappingNode:
		listKey := ""
		for _, key := range groupedListKeys {
			if mappingValue(root, key) == nil {
				continue
			}
			if listKey != "" {
				return nil, nil, fmt.Errorf("%s: grouped form takes one list, found both %q and %q", path, listKey, key)
			}
			listKey = key
		}
		if listKey == "" {
			if err := notGroupedForm(path, root); err != nil {
				return nil, nil, err
			}
			// A single object per file.
			return []*yaml.Node{root}, nil, nil
		}
		// Grouped form. Only defaults and the list may appear, so a stray
		// object field next to the list fails here as loudly as it does in
		// the reconciler's loader rather than being silently dropped.
		for i := 0; i < len(root.Content)-1; i += 2 {
			if key := root.Content[i].Value; key != "defaults" && key != listKey {
				return nil, nil, fmt.Errorf("%s: grouped form only allows 'defaults' and %q at the top level, found %q", path, listKey, key)
			}
		}
		list := mappingValue(root, listKey)
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

// groupedListKeys are the keys that hold a grouped form's objects. `items` is
// the general name; `devices` is kept so existing inventory files still load.
var groupedListKeys = []string{"items", "devices"}

// notGroupedForm reports a mapping that was meant as the grouped form but
// names its list wrong. Without this, `defaults:` next to `device:` is read
// as one object and fails as `unknown field "defaults"` and `unknown field
// "device"` on a device with no name, which points away from the mistake.
func notGroupedForm(path string, root *yaml.Node) error {
	hasDefaults := mappingValue(root, "defaults") != nil
	nearList := ""
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i].Value, root.Content[i+1]
		if key == "defaults" || value.Kind != yaml.SequenceNode {
			continue
		}
		for _, want := range groupedListKeys {
			if editDistance(key, want) <= 2 {
				nearList = key
			}
		}
	}
	switch {
	case hasDefaults && nearList != "":
		return fmt.Errorf("%s: grouped form has \"defaults\" but no \"items\" list; the objects are under %q (did you mean \"items\"?)", path, nearList)
	case hasDefaults:
		return fmt.Errorf("%s: grouped form has \"defaults\" but no \"items\" list; put the objects under \"items:\" (\"devices:\" is also accepted)", path)
	case nearList != "":
		return fmt.Errorf("%s: top-level key %q is not a field (did you mean \"items\"?); a file of several objects lists them under \"items:\", with shared fields under \"defaults:\"", path, nearList)
	}
	return nil
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
