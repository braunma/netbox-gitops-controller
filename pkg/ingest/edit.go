// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/braunma/netbox-gitops-controller/pkg/loader"
)

// objectEdit is everything ingest wants to write into one declared object.
//
// Nothing outside the whitelist ever reaches here: the planner puts only
// whitelisted keys into Scalars and only prefixed custom fields into
// CustomFields, and this file's job is to place them without disturbing
// anything else.
type objectEdit struct {
	// Label names the object in errors and in the change list, e.g.
	// `device "srv-01"`.
	Label string
	Prov  loader.Provenance
	// Scalars are top-level keys of the object's mapping.
	Scalars []keyValue
	// CustomFields are entries of the object's `custom_fields:` mapping.
	CustomFields []keyValue
}

// FileChange is a rewritten file, before it is written to disk.
type FileChange struct {
	Path string
	// Before and After are the whole file, so --dry-run can print a diff and
	// an apply is a single atomic write.
	Before string
	After  string
	// Objects lists the labels of the objects whose facts changed.
	Objects []string
	// Keys lists the keys actually written, deduplicated and sorted, for the
	// machine-readable change list.
	Keys []string
}

// editFile applies every edit destined for one file and returns the rewritten
// content.
//
// The edits are made as text, not by re-encoding the parsed document. Encoding
// a yaml.Node back out loses blank lines and re-flows anything the author
// spaced by hand, which would turn a one-line fact update into a whole-file
// diff and destroy the review gate this design rests on. Instead the parsed
// nodes are used only to locate the exact span of each value, and the file is
// otherwise passed through byte for byte.
//
// If a value cannot be located and replaced safely, the whole file is abandoned
// with an error naming the object and the key. A partially rewritten inventory
// file is far worse than an unwritten one.
func editFile(path, content string, edits []objectEdit) (*FileChange, error) {
	if len(edits) == 0 {
		return nil, nil
	}

	lines := strings.Split(content, "\n")
	replacements := map[int]*lineReplacement{}
	insertions := map[int][]string{}

	// Deterministic order: objects as they appear in the file.
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].Prov.Index < edits[j].Prov.Index })

	var touched []string
	writtenKeys := map[string]bool{}

	for _, edit := range edits {
		node := edit.Prov.Node
		if node == nil || node.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s: cannot locate the block of %s in the file", path, edit.Label)
		}

		wrote := false

		for _, kv := range edit.Scalars {
			did, err := placeKey(path, edit, lines, node, kv, replacements, insertions, node.Column)
			if err != nil {
				return nil, err
			}
			if did {
				wrote = true
				writtenKeys[kv.Key] = true
			}
		}

		if len(edit.CustomFields) > 0 {
			did, err := placeCustomFields(path, edit, lines, node, replacements, insertions)
			if err != nil {
				return nil, err
			}
			if did {
				wrote = true
				for _, kv := range edit.CustomFields {
					writtenKeys[KeyDeviceCustomFields+"."+kv.Key] = true
				}
			}
		}

		if wrote {
			touched = append(touched, edit.Label)
		}
	}

	if len(replacements) == 0 && len(insertions) == 0 {
		return nil, nil
	}

	after := renderLines(lines, replacements, insertions)
	if after == content {
		return nil, nil
	}

	keys := make([]string, 0, len(writtenKeys))
	for key := range writtenKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return &FileChange{Path: path, Before: content, After: after, Objects: touched, Keys: keys}, nil
}

// lineReplacement rewrites one span of one line.
type lineReplacement struct {
	start, end int // byte offsets into the original line
	text       string
}

// placeKey writes one key into a mapping: replacing the existing value in
// place, or queueing a new line at the mapping's indent. It returns whether
// anything is to be written.
func placeKey(path string, edit objectEdit, lines []string, mapping *yaml.Node, kv keyValue,
	replacements map[int]*lineReplacement, insertions map[int][]string, indentColumn int) (bool, error) {

	keyNode, valueNode := mappingEntry(mapping, kv.Key)
	if keyNode == nil {
		line, err := renderKeyLine(indentColumn-1, kv)
		if err != nil {
			return false, fmt.Errorf("%s: %s: %w", path, edit.Label, err)
		}
		anchor, err := insertionAnchor(mapping)
		if err != nil {
			return false, fmt.Errorf("%s: %s: cannot place %s: %w", path, edit.Label, kv.Key, err)
		}
		insertions[anchor] = append(insertions[anchor], line)
		return true, nil
	}

	if valueNode.Kind != yaml.ScalarNode || valueNode.Line != keyNode.Line {
		return false, fmt.Errorf(
			"%s:%d: %s: %s is not a plain value on its own key's line, so it cannot be rewritten in place; "+
				"simplify it by hand or remove it and let the next run write it",
			path, keyNode.Line, edit.Label, kv.Key)
	}
	if valueNode.Line-1 >= len(lines) {
		return false, fmt.Errorf("%s: %s: %s points past the end of the file", path, edit.Label, kv.Key)
	}

	line := lines[valueNode.Line-1]
	start, end, err := scalarSpan(line, valueNode.Column)
	if err != nil {
		return false, fmt.Errorf("%s:%d: %s: %s: %w", path, valueNode.Line, edit.Label, kv.Key, err)
	}

	text, err := formatScalar(kv.Value, valueNode.Style)
	if err != nil {
		return false, fmt.Errorf("%s: %s: %s: %w", path, edit.Label, kv.Key, err)
	}
	if line[start:end] == text {
		return false, nil
	}
	if existing, taken := replacements[valueNode.Line]; taken {
		return false, fmt.Errorf("%s:%d: %s: two facts want to rewrite the same line (%q and %q)",
			path, valueNode.Line, edit.Label, existing.text, text)
	}
	replacements[valueNode.Line] = &lineReplacement{start: start, end: end, text: text}
	return true, nil
}

// placeCustomFields writes the `hw_*` entries, creating the `custom_fields:`
// block if the object has none.
func placeCustomFields(path string, edit objectEdit, lines []string, object *yaml.Node,
	replacements map[int]*lineReplacement, insertions map[int][]string) (bool, error) {

	keyNode, valueNode := mappingEntry(object, KeyDeviceCustomFields)

	// No block, or an empty one: write the whole thing.
	if keyNode == nil || valueNode.Kind == yaml.ScalarNode {
		indent := object.Column - 1
		anchor := 0
		var block []string

		if keyNode == nil {
			var err error
			anchor, err = insertionAnchor(object)
			if err != nil {
				return false, fmt.Errorf("%s: %s: cannot place %s: %w", path, edit.Label, KeyDeviceCustomFields, err)
			}
			block = append(block, strings.Repeat(" ", indent)+KeyDeviceCustomFields+":")
		} else {
			// `custom_fields:` with nothing under it. Keep the existing line and
			// hang the entries off it.
			anchor = keyNode.Line
			indent = keyNode.Column - 1
		}

		for _, kv := range edit.CustomFields {
			line, err := renderKeyLine(indent+2, kv)
			if err != nil {
				return false, fmt.Errorf("%s: %s: %w", path, edit.Label, err)
			}
			block = append(block, line)
		}
		insertions[anchor] = append(insertions[anchor], block...)
		return true, nil
	}

	if valueNode.Kind != yaml.MappingNode || valueNode.Style&yaml.FlowStyle != 0 {
		return false, fmt.Errorf(
			"%s:%d: %s: custom_fields is not a plain block mapping, so its entries cannot be rewritten in place",
			path, keyNode.Line, edit.Label)
	}

	wrote := false
	for _, kv := range edit.CustomFields {
		did, err := placeKey(path, edit, lines, valueNode, kv, replacements, insertions, valueNode.Column)
		if err != nil {
			return false, err
		}
		wrote = wrote || did
	}
	return wrote, nil
}

// insertionAnchor returns the line a new key of this mapping should follow:
// the last direct key whose value is a plain scalar on the key's own line.
//
// Appending after the last such key keeps a new `serial` among the object's
// other scalars instead of dropping it into whatever nested block happens to
// come last, and it is the only position that is well defined for every shape
// these files take.
func insertionAnchor(mapping *yaml.Node) (int, error) {
	anchor := 0
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]
		if value.Kind == yaml.ScalarNode && value.Line == key.Line && key.Line > anchor {
			anchor = key.Line
		}
	}
	if anchor == 0 {
		return 0, fmt.Errorf("the block holds no simple key to append after")
	}
	return anchor, nil
}

// mappingEntry returns the key and value nodes for a key of a mapping node.
func mappingEntry(mapping *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i], mapping.Content[i+1]
		}
	}
	return nil, nil
}

// renderKeyLine renders `<indent><key>: <value>` for a key the file does not
// have yet. A new string value is double-quoted, which is how the hand-written
// inventory spells strings throughout, so an inserted line does not stand out
// from the ones around it.
func renderKeyLine(indent int, kv keyValue) (string, error) {
	text, err := formatScalar(kv.Value, yaml.DoubleQuotedStyle)
	if err != nil {
		return "", fmt.Errorf("%s: %w", kv.Key, err)
	}
	return strings.Repeat(" ", indent) + kv.Key + ": " + text, nil
}

// formatScalar renders a value as a YAML scalar, keeping the quoting style the
// author used where the value is still a string.
//
// The encoder is the authority on quoting: it knows that "On" and "yes" have to
// be quoted or they come back as booleans, and that 1.14.2 does not.
func formatScalar(value interface{}, style yaml.Style) (string, error) {
	node := &yaml.Node{}
	if err := node.Encode(value); err != nil {
		return "", err
	}
	if node.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("%v is not a simple value", value)
	}
	if _, isString := value.(string); isString && (style == yaml.DoubleQuotedStyle || style == yaml.SingleQuotedStyle) {
		node.Style = style
	}

	out, err := yaml.Marshal(node)
	if err != nil {
		return "", err
	}
	text := strings.TrimRight(string(out), "\n")
	if strings.Contains(text, "\n") {
		// A value the encoder had to break across lines cannot be spliced into
		// one. Refusing is right: the alternative is emitting broken YAML.
		return "", fmt.Errorf("value %q does not fit on one line", value)
	}
	return text, nil
}

// scalarSpan returns the byte range of the scalar token starting at a 1-based
// column of a line, so the value can be replaced without touching the
// indentation before it or a comment after it.
func scalarSpan(line string, column int) (int, int, error) {
	start := column - 1
	if start < 0 || start >= len(line) {
		return 0, 0, fmt.Errorf("the value does not start where the parser said it does")
	}

	switch line[start] {
	case '"':
		for i := start + 1; i < len(line); i++ {
			if line[i] == '\\' {
				i++
				continue
			}
			if line[i] == '"' {
				return start, i + 1, nil
			}
		}
		return 0, 0, fmt.Errorf("the quoted value is not closed on this line")
	case '\'':
		for i := start + 1; i < len(line); i++ {
			if line[i] == '\'' {
				// '' inside a single-quoted scalar is an escaped quote.
				if i+1 < len(line) && line[i+1] == '\'' {
					i++
					continue
				}
				return start, i + 1, nil
			}
		}
		return 0, 0, fmt.Errorf("the quoted value is not closed on this line")
	}

	// A plain scalar runs to the end of the line, or to a `#` that follows
	// whitespace — which is exactly where YAML says a comment begins, so a `#`
	// inside the value itself is left alone.
	end := len(line)
	for i := start + 1; i < len(line); i++ {
		if line[i] == '#' && (line[i-1] == ' ' || line[i-1] == '\t') {
			end = i
			break
		}
	}
	for end > start && (line[end-1] == ' ' || line[end-1] == '\t') {
		end--
	}
	return start, end, nil
}

// renderLines rebuilds the file from the original lines plus the replacements
// and insertions. Every line nobody touched comes through unchanged.
func renderLines(lines []string, replacements map[int]*lineReplacement, insertions map[int][]string) string {
	out := make([]string, 0, len(lines)+len(insertions))
	for i, line := range lines {
		lineNo := i + 1
		if r, ok := replacements[lineNo]; ok {
			line = line[:r.start] + r.text + line[r.end:]
		}
		out = append(out, line)
		out = append(out, insertions[lineNo]...)
	}
	return strings.Join(out, "\n")
}

// verifyFileChange re-reads the rewritten file and checks that it says what it
// was meant to say and nothing else: every object still parses, the edited
// objects carry the new values, and every other object in the file is
// byte-for-byte identical in meaning.
//
// This is the backstop for the text surgery above. Locating a value by line and
// column is simple enough to be right, but "simple enough to be right" is not a
// guarantee, and the cost of being wrong is a corrupted inventory file in a
// merge request that looks plausible.
func verifyFileChange(change *FileChange, edits []objectEdit) error {
	before, err := rawObjects(change.Before)
	if err != nil {
		return fmt.Errorf("%s: could not re-read the original: %w", change.Path, err)
	}
	after, err := rawObjects(change.After)
	if err != nil {
		return fmt.Errorf("%s: the rewritten file does not parse (%w); it was not written", change.Path, err)
	}
	if len(before) != len(after) {
		return fmt.Errorf("%s: the rewrite changed the number of objects from %d to %d; it was not written",
			change.Path, len(before), len(after))
	}

	expected := make([]map[string]interface{}, len(before))
	copy(expected, before)
	for _, edit := range edits {
		if edit.Prov.Index >= len(expected) {
			return fmt.Errorf("%s: %s is not where the loader said it was; it was not written", change.Path, edit.Label)
		}
		expected[edit.Prov.Index] = applyExpected(expected[edit.Prov.Index], edit)
	}

	for i := range expected {
		want, gotValue := canonical(expected[i]), canonical(after[i])
		if want != gotValue {
			return fmt.Errorf("%s: rewriting entry %d did not produce what was intended; it was not written\nwant: %s\ngot:  %s",
				change.Path, i+1, want, gotValue)
		}
	}
	return nil
}

// applyExpected returns the object as it should read once the edit is applied.
func applyExpected(object map[string]interface{}, edit objectEdit) map[string]interface{} {
	updated := make(map[string]interface{}, len(object)+len(edit.Scalars)+1)
	for key, value := range object {
		updated[key] = value
	}
	for _, kv := range edit.Scalars {
		updated[kv.Key] = kv.Value
	}
	if len(edit.CustomFields) > 0 {
		fields := map[string]interface{}{}
		if existing, ok := updated[KeyDeviceCustomFields].(map[string]interface{}); ok {
			for key, value := range existing {
				fields[key] = value
			}
		}
		for _, kv := range edit.CustomFields {
			fields[kv.Key] = kv.Value
		}
		updated[KeyDeviceCustomFields] = fields
	}
	return updated
}

// rawObjects decodes a file's objects as plain maps, without merging defaults:
// the comparison is about what each block literally says.
func rawObjects(content string) ([]map[string]interface{}, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}

	nodes, _, err := loader.ItemNodes("", doc.Content[0])
	if err != nil {
		return nil, err
	}

	objects := make([]map[string]interface{}, 0, len(nodes))
	for _, node := range nodes {
		var raw map[string]interface{}
		if err := node.Decode(&raw); err != nil {
			return nil, err
		}
		objects = append(objects, raw)
	}
	return objects, nil
}

// canonical renders a decoded object in a comparable form. Numbers are
// normalized through JSON so that an int written as 32 and read back as 32
// compare equal regardless of which Go type the decoder chose.
func canonical(object map[string]interface{}) string {
	data, err := json.Marshal(normalizeNumbers(object))
	if err != nil {
		return fmt.Sprintf("%v", object)
	}
	return string(data)
}

// normalizeNumbers converts every integral value to float64, so that the
// intended value (an int from a fact) and the parsed value (whatever the YAML
// decoder produced) compare on what they mean rather than on their Go type.
func normalizeNumbers(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, inner := range v {
			out[key] = normalizeNumbers(inner)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, inner := range v {
			out[i] = normalizeNumbers(inner)
		}
		return out
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return value
	}
}
