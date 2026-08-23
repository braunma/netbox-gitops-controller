// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"fmt"
	"sort"
)

// Conflict is two sources of one run disagreeing about one key of one
// declared object. The later source wins — sources are planned and applied in
// configuration order, and that order is the precedence — but an override
// nobody is told about would hide that the sources disagree, which is exactly
// the kind of fact a person should get to see.
type Conflict struct {
	// Object labels the declared object, e.g. `virtual machine "web-01"`.
	Object string `json:"object"`
	// File is the file that declares the object.
	File string `json:"file"`
	// Key is the YAML key both sources wrote; a custom field reads
	// `custom_fields.<name>`.
	Key string `json:"key"`
	// EarlierSource wrote EarlierValue first; LaterSource overrode it with
	// LaterValue, which is what ends up in the file.
	EarlierSource string `json:"earlier_source"`
	EarlierValue  string `json:"earlier_value"`
	LaterSource   string `json:"later_source"`
	LaterValue    string `json:"later_value"`
}

// ConflictTracker remembers, across the snapshots of one run, which source
// last wrote (or, in a dry run, planned to write) each key of each declared
// object, so a later snapshot writing a different value to the same key is
// reported instead of silently winning. One tracker spans one `collect` run;
// a run that processes a single snapshot has nothing to conflict with.
type ConflictTracker struct {
	seen map[conflictKey]sourcedValue
}

// conflictKey identifies one key of one declared object.
type conflictKey struct {
	file, object, key string
}

// sourcedValue is who wrote a key last, and what they wrote.
type sourcedValue struct {
	value, source string
}

// NewConflictTracker returns a tracker for one run's snapshots.
func NewConflictTracker() *ConflictTracker {
	return &ConflictTracker{seen: map[conflictKey]sourcedValue{}}
}

// observeEdits runs one snapshot's planned edits through the tracker and
// returns the conflicts they raise, in file order and then in the order the
// objects appear in their file, so the report is deterministic.
func (t *ConflictTracker) observeEdits(source string, editsByFile map[string][]objectEdit) []Conflict {
	files := make([]string, 0, len(editsByFile))
	for file := range editsByFile {
		files = append(files, file)
	}
	sort.Strings(files)

	var conflicts []Conflict
	for _, file := range files {
		edits := append([]objectEdit(nil), editsByFile[file]...)
		sort.SliceStable(edits, func(i, j int) bool { return edits[i].Prov.Index < edits[j].Prov.Index })
		for _, edit := range edits {
			for _, kv := range edit.Scalars {
				if c := t.observe(source, file, edit.Label, kv.Key, kv.Value); c != nil {
					conflicts = append(conflicts, *c)
				}
			}
			for _, kv := range edit.CustomFields {
				if c := t.observe(source, file, edit.Label, KeyDeviceCustomFields+"."+kv.Key, kv.Value); c != nil {
					conflicts = append(conflicts, *c)
				}
			}
		}
	}
	return conflicts
}

// observe records one planned write and reports the conflict when an earlier
// snapshot wrote a different value to the same key. Identical values are two
// sources agreeing, which warrants nothing. The newest write is kept either
// way, so a third source is compared against the value that actually stands.
func (t *ConflictTracker) observe(source, file, object, key string, value interface{}) *Conflict {
	// Values compare as what they mean, like sameValue: the same number may
	// arrive as an int from one adapter and an int64 from another.
	rendered := fmt.Sprintf("%v", value)
	k := conflictKey{file: file, object: object, key: key}
	previous, written := t.seen[k]
	t.seen[k] = sourcedValue{value: rendered, source: source}
	if !written || previous.value == rendered {
		return nil
	}
	return &Conflict{
		Object:        object,
		File:          file,
		Key:           key,
		EarlierSource: previous.source,
		EarlierValue:  previous.value,
		LaterSource:   source,
		LaterValue:    rendered,
	}
}
