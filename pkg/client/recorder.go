package client

import "sync"

// ChangeAction classifies what a sync run did (or, in dry-run, would do) to
// a single object.
type ChangeAction string

const (
	ActionCreate    ChangeAction = "create"
	ActionUpdate    ChangeAction = "update"
	ActionDelete    ChangeAction = "delete"
	ActionUnchanged ChangeAction = "unchanged"
)

// ChangeRecord describes a single planned or applied change.
type ChangeRecord struct {
	Action   ChangeAction `json:"action"`
	App      string       `json:"app"`
	Endpoint string       `json:"endpoint"`
	// Object is a human-readable identifier, e.g. "slug=berlin-dc" or "id=42".
	Object string `json:"object,omitempty"`
	// Fields holds the full payload for creates and only the changed fields
	// for updates.
	Fields map[string]interface{} `json:"fields,omitempty"`
}

// ChangeSummary holds per-action counts for a sync run.
type ChangeSummary struct {
	Create    int `json:"create"`
	Update    int `json:"update"`
	Delete    int `json:"delete"`
	Unchanged int `json:"unchanged"`
}

// HasChanges reports whether the run produced (or, in dry-run, would
// produce) any mutation.
func (s ChangeSummary) HasChanges() bool {
	return s.Create > 0 || s.Update > 0 || s.Delete > 0
}

// ChangeRecorder collects the changes a client makes during a sync run so
// the CLI can print a plan summary and emit machine-readable output. Dry-run
// mode records the same entries without touching NetBox, which makes the
// records a plan of pending changes.
type ChangeRecorder struct {
	mu      sync.Mutex
	records []ChangeRecord
}

// NewChangeRecorder creates an empty recorder.
func NewChangeRecorder() *ChangeRecorder {
	return &ChangeRecorder{}
}

// Record appends a change record.
func (r *ChangeRecorder) Record(rec ChangeRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
}

// Records returns a copy of all collected records, including unchanged
// objects.
func (r *ChangeRecorder) Records() []ChangeRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ChangeRecord, len(r.records))
	copy(out, r.records)
	return out
}

// Changes returns only the records that mutate NetBox (everything except
// unchanged objects). The result is never nil so it serializes to a JSON
// array.
func (r *ChangeRecorder) Changes() []ChangeRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ChangeRecord, 0, len(r.records))
	for _, rec := range r.records {
		if rec.Action != ActionUnchanged {
			out = append(out, rec)
		}
	}
	return out
}

// Summary aggregates the records into per-action counts.
func (r *ChangeRecorder) Summary() ChangeSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	var s ChangeSummary
	for _, rec := range r.records {
		switch rec.Action {
		case ActionCreate:
			s.Create++
		case ActionUpdate:
			s.Update++
		case ActionDelete:
			s.Delete++
		case ActionUnchanged:
			s.Unchanged++
		}
	}
	return s
}

// objectLabel derives a human-readable identifier from a payload for
// changes recorded outside Apply (which has a lookup to label with).
func objectLabel(data map[string]interface{}) string {
	for _, key := range []string{"name", "slug", "address", "model", "display"} {
		if v, ok := data[key].(string); ok && v != "" {
			return key + "=" + v
		}
	}
	return ""
}
