package client

import (
	"testing"
)

// TestApplyRecordsCreate verifies that the create path of Apply records a
// create change labeled with the lookup criteria.
func TestApplyRecordsCreate(t *testing.T) {
	ats := newApplyTestServer(t, nil)
	c := newApplyTestClient(ats.srv, 7)

	if _, err := c.Apply("dcim", "sites",
		map[string]interface{}{"slug": "berlin-dc"},
		map[string]interface{}{"name": "Berlin DC", "slug": "berlin-dc"}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	records := c.Recorder().Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d: %+v", len(records), records)
	}
	rec := records[0]
	if rec.Action != ActionCreate || rec.App != "dcim" || rec.Endpoint != "sites" {
		t.Errorf("record = %+v, expected create of dcim/sites", rec)
	}
	if rec.Object != "slug=berlin-dc" {
		t.Errorf("record object = %q, expected \"slug=berlin-dc\"", rec.Object)
	}
	if rec.Fields["name"] != "Berlin DC" {
		t.Errorf("record fields = %v, expected payload with name", rec.Fields)
	}

	sum := c.Recorder().Summary()
	if sum.Create != 1 || !sum.HasChanges() {
		t.Errorf("summary = %+v, expected 1 create and HasChanges() == true", sum)
	}
}

// TestApplyRecordsUpdateWithChangedFieldsOnly verifies that the update path
// records only the diffed fields.
func TestApplyRecordsUpdateWithChangedFieldsOnly(t *testing.T) {
	existing := Object{
		"id":          float64(42),
		"name":        "Berlin DC",
		"slug":        "berlin-dc",
		"description": "old description",
		"tags":        []interface{}{map[string]interface{}{"id": float64(7)}},
	}
	ats := newApplyTestServer(t, []Object{existing})
	c := newApplyTestClient(ats.srv, 7)

	if _, err := c.Apply("dcim", "sites",
		map[string]interface{}{"slug": "berlin-dc"},
		map[string]interface{}{"name": "Berlin DC", "slug": "berlin-dc", "description": "new description"}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	records := c.Recorder().Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d: %+v", len(records), records)
	}
	rec := records[0]
	if rec.Action != ActionUpdate {
		t.Errorf("record action = %q, expected update", rec.Action)
	}
	if len(rec.Fields) != 1 || rec.Fields["description"] != "new description" {
		t.Errorf("record fields = %v, expected only the changed description", rec.Fields)
	}
}

// TestApplyRecordsUnchanged verifies that an in-sync Apply records an
// unchanged entry, which feeds the plan summary but is excluded from the
// change list.
func TestApplyRecordsUnchanged(t *testing.T) {
	existing := Object{
		"id":   float64(42),
		"name": "Berlin DC",
		"slug": "berlin-dc",
		"tags": []interface{}{map[string]interface{}{"id": float64(7)}},
	}
	ats := newApplyTestServer(t, []Object{existing})
	c := newApplyTestClient(ats.srv, 7)

	if _, err := c.Apply("dcim", "sites",
		map[string]interface{}{"slug": "berlin-dc"},
		map[string]interface{}{"name": "Berlin DC", "slug": "berlin-dc"}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	sum := c.Recorder().Summary()
	if sum.Unchanged != 1 || sum.HasChanges() {
		t.Errorf("summary = %+v, expected 1 unchanged and HasChanges() == false", sum)
	}
	if changes := c.Recorder().Changes(); len(changes) != 0 {
		t.Errorf("Changes() = %+v, expected unchanged records to be filtered out", changes)
	}
}

// TestDirectMutationsRecorded verifies that reconcilers calling
// Create/Update/Delete directly (cables, device bays, primary IPs) are
// counted as well.
func TestDirectMutationsRecorded(t *testing.T) {
	ats := newApplyTestServer(t, nil)
	c := newApplyTestClient(ats.srv, 7)

	if _, err := c.Create("dcim", "cables", map[string]interface{}{"type": "cat6a"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := c.Update("dcim", "devices", 5, map[string]interface{}{"primary_ip4": 12}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if err := c.Delete("dcim", "cables", 9); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	sum := c.Recorder().Summary()
	if sum.Create != 1 || sum.Update != 1 || sum.Delete != 1 {
		t.Errorf("summary = %+v, expected 1 create, 1 update, 1 delete", sum)
	}

	records := c.Recorder().Records()
	if records[1].Object != "id=5" || records[2].Object != "id=9" {
		t.Errorf("records = %+v, expected id-based labels for direct update/delete", records)
	}
}

// TestDryRunRecordsPlannedChanges verifies that dry-run mode records the
// pending changes without sending any mutating request — this is what makes
// `--dry-run --detailed-exitcode` usable as a drift monitor.
func TestDryRunRecordsPlannedChanges(t *testing.T) {
	ats := newApplyTestServer(t, nil)
	c := newApplyTestClient(ats.srv, 7)
	c.SetDryRun(true)

	if _, err := c.Apply("dcim", "sites",
		map[string]interface{}{"slug": "berlin-dc"},
		map[string]interface{}{"name": "Berlin DC", "slug": "berlin-dc"}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if len(ats.mutations) != 0 {
		t.Fatalf("dry-run sent %d mutating request(s): %+v", len(ats.mutations), ats.mutations)
	}
	sum := c.Recorder().Summary()
	if sum.Create != 1 || !sum.HasChanges() {
		t.Errorf("summary = %+v, expected the planned create to be recorded", sum)
	}
}

// TestObjectLabel covers the fallback chain used for direct creates.
func TestObjectLabel(t *testing.T) {
	cases := []struct {
		data map[string]interface{}
		want string
	}{
		{map[string]interface{}{"name": "srv-01", "slug": "srv-01-slug"}, "name=srv-01"},
		{map[string]interface{}{"slug": "berlin-dc"}, "slug=berlin-dc"},
		{map[string]interface{}{"address": "10.0.0.1/24"}, "address=10.0.0.1/24"},
		{map[string]interface{}{"model": "PowerEdge R640"}, "model=PowerEdge R640"},
		{map[string]interface{}{"type": "cat6a"}, ""},
	}
	for _, tc := range cases {
		if got := objectLabel(tc.data); got != tc.want {
			t.Errorf("objectLabel(%v) = %q, want %q", tc.data, got, tc.want)
		}
	}
}
