// SPDX-License-Identifier: Apache-2.0

package client

import (
	"fmt"
	"strings"
	"time"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// SyncState is the controller's note to itself in NetBox: which commit of the
// data directory this NetBox was last brought fully up to date with.
//
// It lives in NetBox rather than in the repository or the pipeline because it
// describes NetBox: restore NetBox from a backup and the note goes back with
// it, point the controller at another instance and it reads that instance's
// own note.
type SyncState struct {
	// Commit is the full hash of the data directory's commit. Empty means no
	// sync is on record, or one started and did not finish.
	Commit string
	// Settings fingerprints the run settings that decide what gets loaded
	// (data directory, ignore patterns, libraries). A base loaded under other
	// settings is not comparable.
	Settings string
	// At is when the sync finished.
	At time.Time
	// Note is the description as found when it names no commit (an
	// interrupted sync), for the log.
	Note string
}

const stateInProgress = "sync in progress or interrupted; the next sync is a full one"

// encode renders the state as the tag's description (NetBox limits it to 200
// characters).
func (s SyncState) encode() string {
	return fmt.Sprintf("commit=%s settings=%s at=%s (written by netbox-gitops; do not edit)",
		s.Commit, s.Settings, s.At.UTC().Format(time.RFC3339))
}

// decodeSyncState parses a description written by encode. Anything else — a
// hand-edited text, the in-progress note — yields a state with no commit.
func decodeSyncState(description string) SyncState {
	s := SyncState{}
	for _, field := range strings.Fields(description) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "commit":
			s.Commit = value
		case "settings":
			s.Settings = value
		case "at":
			if t, err := time.Parse(time.RFC3339, value); err == nil {
				s.At = t
			}
		}
	}
	if !isCommitHash(s.Commit) {
		return SyncState{Note: description}
	}
	return s
}

func isCommitHash(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// ReadSyncState returns the sync state NetBox holds. A NetBox without the
// state tag returns the zero state: no sync on record.
func (c *NetBoxClient) ReadSyncState() (SyncState, error) {
	tags, err := c.Filter("extras", "tags", map[string]interface{}{"slug": constants.StateTagSlug})
	if err != nil {
		return SyncState{}, fmt.Errorf("failed to read the sync state tag: %w", err)
	}
	if len(tags) == 0 {
		return SyncState{}, nil
	}
	c.stateTagID = utils.GetIDFromObject(tags[0])
	description, _ := tags[0]["description"].(string)
	return decodeSyncState(description), nil
}

// MarkSyncStarted replaces a recorded commit with the in-progress note, so a
// sync that writes to NetBox and then fails leaves no claim behind that
// NetBox matches a commit. It does nothing when no commit is on record, and
// nothing in a dry run. Call ReadSyncState first.
func (c *NetBoxClient) MarkSyncStarted() error {
	if c.dryRun || c.stateTagID == 0 {
		return nil
	}
	return c.writeStateTag(stateInProgress)
}

// WriteSyncState records a completed sync. It does nothing in a dry run. The
// writes bypass the change recorder: the controller's own bookkeeping is not
// a change to the infrastructure it documents.
func (c *NetBoxClient) WriteSyncState(s SyncState) error {
	if c.dryRun {
		return nil
	}
	return c.writeStateTag(s.encode())
}

func (c *NetBoxClient) writeStateTag(description string) error {
	if c.stateTagID != 0 {
		path := fmt.Sprintf("/api/extras/tags/%d/", c.stateTagID)
		if _, err := c.Request("PATCH", path, map[string]interface{}{"description": description}); err != nil {
			return fmt.Errorf("failed to update the sync state tag: %w", err)
		}
		return nil
	}
	tag, err := c.Request("POST", "/api/extras/tags/", map[string]interface{}{
		"slug":        constants.StateTagSlug,
		"name":        constants.StateTagName,
		"color":       constants.StateTagColor,
		"description": description,
	})
	if err != nil {
		return fmt.Errorf("failed to create the sync state tag: %w", err)
	}
	c.stateTagID = utils.GetIDFromObject(tag)
	return nil
}

// ReconcileIncomplete reports whether any declared object was skipped this
// run (a missing reference, a parent only planned): NetBox then does not hold
// everything the YAML declares.
func (c *NetBoxClient) ReconcileIncomplete() bool {
	c.seenMu.Lock()
	defer c.seenMu.Unlock()
	return len(c.incomplete) > 0
}
