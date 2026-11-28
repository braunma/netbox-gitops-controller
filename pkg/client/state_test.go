// SPDX-License-Identifier: Apache-2.0

package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

// stateServer is a NetBox that holds at most one tag: the sync state tag.
type stateServer struct {
	description *string // nil: the tag does not exist
	writes      []string
}

func newStateServer(t *testing.T, dryRun bool) (*stateServer, *NetBoxClient) {
	t.Helper()
	s := &stateServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasPrefix(r.URL.Path, "/api/extras/tags/") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.Method {
		case "GET":
			results := []Object{}
			if s.description != nil {
				results = append(results, Object{"id": 7, "slug": "gitops-state", "description": *s.description})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": results, "next": nil})
		case "POST", "PATCH":
			s.writes = append(s.writes, r.Method+" "+r.URL.Path)
			d, _ := body["description"].(string)
			s.description = &d
			_ = json.NewEncoder(w).Encode(Object{"id": 7})
		}
	}))
	t.Cleanup(srv.Close)
	c := &NetBoxClient{
		baseURL: srv.URL, token: "test-token", httpClient: srv.Client(),
		logger: utils.NewDiscardLogger(), dryRun: dryRun, recorder: NewChangeRecorder(),
	}
	c.tagManager = NewTagManager(c)
	return s, c
}

func TestSyncStateRoundTrip(t *testing.T) {
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	in := SyncState{Commit: testCommit, Settings: "abc123", At: at}
	encoded := in.encode()
	if len(encoded) > 200 {
		t.Fatalf("encoded state is %d characters; NetBox limits a tag description to 200", len(encoded))
	}
	if got := decodeSyncState(encoded); got != in {
		t.Errorf("decode(encode()) = %+v, want %+v", got, in)
	}
}

func TestDecodeSyncStateRejectsWhatItDidNotWrite(t *testing.T) {
	for _, description := range []string{
		"",
		stateInProgress,
		"commit=abc settings=x",             // not a full hash
		"commit=" + strings.Repeat("z", 40), // not hex
		"edited by hand",
	} {
		got := decodeSyncState(description)
		if got.Commit != "" || got.Note != description {
			t.Errorf("decode(%q) = %+v, want no commit and the text kept as the note", description, got)
		}
	}
}

func TestReadSyncStateWithoutATag(t *testing.T) {
	_, c := newStateServer(t, false)
	state, err := c.ReadSyncState()
	if err != nil || state != (SyncState{}) {
		t.Fatalf("ReadSyncState() = %+v, %v, want the zero state", state, err)
	}
	// Nothing on record: nothing to withdraw.
	if err := c.MarkSyncStarted(); err != nil {
		t.Fatal(err)
	}
}

func TestSyncStateLifecycle(t *testing.T) {
	s, c := newStateServer(t, false)

	// First completed sync: the tag is created.
	if _, err := c.ReadSyncState(); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteSyncState(SyncState{Commit: testCommit, Settings: "s1", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if len(s.writes) != 1 || s.writes[0] != "POST /api/extras/tags/" {
		t.Fatalf("writes = %v, want one POST", s.writes)
	}

	// The next run reads it back ...
	state, err := c.ReadSyncState()
	if err != nil || state.Commit != testCommit || state.Settings != "s1" {
		t.Fatalf("ReadSyncState() = %+v, %v", state, err)
	}
	// ... withdraws it before writing ...
	if err := c.MarkSyncStarted(); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.ReadSyncState(); got.Commit != "" || got.Note != stateInProgress {
		t.Fatalf("state while a sync runs = %+v, want no commit", got)
	}
	// ... and records the new commit by updating the same tag.
	other := strings.Repeat("a", 40)
	if err := c.WriteSyncState(SyncState{Commit: other, Settings: "s1", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.ReadSyncState(); got.Commit != other {
		t.Fatalf("state after the second sync = %+v", got)
	}
	want := []string{"POST /api/extras/tags/", "PATCH /api/extras/tags/7/", "PATCH /api/extras/tags/7/"}
	if strings.Join(s.writes, ",") != strings.Join(want, ",") {
		t.Errorf("writes = %v, want %v", s.writes, want)
	}

	// Bookkeeping is not a change to the infrastructure.
	if summary := c.Recorder().Summary(); summary.HasChanges() {
		t.Errorf("state writes were recorded as changes: %+v", summary)
	}
}

func TestDryRunNeverWritesTheSyncState(t *testing.T) {
	s, c := newStateServer(t, true)
	d := SyncState{Commit: testCommit, Settings: "s1", At: time.Now()}.encode()
	s.description = &d

	if state, err := c.ReadSyncState(); err != nil || state.Commit != testCommit {
		t.Fatalf("ReadSyncState() = %+v, %v", state, err)
	}
	if err := c.MarkSyncStarted(); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteSyncState(SyncState{Commit: strings.Repeat("b", 40)}); err != nil {
		t.Fatal(err)
	}
	if len(s.writes) != 0 {
		t.Errorf("a dry run wrote the state: %v", s.writes)
	}
}

func TestReconcileIncomplete(t *testing.T) {
	_, c := newStateServer(t, false)
	if c.ReconcileIncomplete() {
		t.Fatal("a fresh client reports an incomplete reconcile")
	}
	c.MarkReconcileIncomplete("dcim", "devices")
	if !c.ReconcileIncomplete() {
		t.Fatal("a skipped object was not reported")
	}
}
