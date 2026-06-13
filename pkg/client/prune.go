package client

import (
	"fmt"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// PruneTarget identifies a NetBox endpoint whose orphaned gitops-managed
// objects should be deleted. Targets are processed in the order they are
// given, so callers must pass them in reverse dependency order (children
// before parents) to satisfy NetBox foreign-key constraints.
type PruneTarget struct {
	App      string
	Endpoint string
	// KeepSlugs lists object slugs that must never be pruned even when they
	// look orphaned, e.g. the managed gitops tag itself.
	KeepSlugs []string
}

// Prune deletes every gitops-tagged object at the given endpoints that was
// not reconciled during this run. An orphan is an object that still exists
// in NetBox but is no longer declared in YAML; because the query is scoped
// to the managed tag, manually created objects are never touched.
//
// In dry-run mode the deletions are recorded as a plan without issuing any
// destructive request.
func (c *NetBoxClient) Prune(targets []PruneTarget) error {
	total := 0
	for _, t := range targets {
		n, err := c.pruneTarget(t)
		if err != nil {
			return fmt.Errorf("failed to prune %s/%s: %w", t.App, t.Endpoint, err)
		}
		total += n
	}

	switch {
	case total == 0:
		c.logger.Info("No orphaned objects to prune")
	case c.dryRun:
		c.logger.Info("Prune plan: %d orphaned object(s) would be deleted", total)
	default:
		c.logger.Success("Pruned %d orphaned object(s)", total)
	}
	return nil
}

// pruneTarget deletes the orphaned objects for a single endpoint and returns
// the number deleted (or, in dry-run, the number that would be deleted).
func (c *NetBoxClient) pruneTarget(t PruneTarget) (int, error) {
	// If reconciliation skipped a declared object at this endpoint, we cannot
	// safely tell orphans from declared-but-unreconciled objects, so skip it.
	if c.reconcileIncomplete(t.App, t.Endpoint) {
		c.logger.Warning("  ⚠ Skipping prune for %s/%s: reconciliation was incomplete; not deleting to avoid removing a declared object",
			t.App, t.Endpoint)
		return 0, nil
	}

	objects, err := c.Filter(t.App, t.Endpoint, map[string]interface{}{
		"tag": constants.ManagedTagSlug,
	})
	if err != nil {
		return 0, err
	}

	keep := make(map[string]bool, len(t.KeepSlugs))
	for _, s := range t.KeepSlugs {
		keep[s] = true
	}

	seen := c.seenIDs(t.App, t.Endpoint)

	deleted := 0
	for _, obj := range objects {
		id := utils.GetIDFromObject(obj)
		if id == 0 || seen[id] {
			continue
		}
		if slug, ok := obj["slug"].(string); ok && keep[slug] {
			continue
		}

		label := pruneLabel(obj)
		verb := "Pruning"
		if c.dryRun {
			verb = "Would prune"
		}
		c.logger.Warning("  ✗ %s orphaned %s: %s (ID: %d)", verb, t.Endpoint, label, id)

		path := fmt.Sprintf("/api/%s/%s/%d/", t.App, t.Endpoint, id)
		if _, err := c.Request("DELETE", path, nil); err != nil {
			return deleted, fmt.Errorf("failed to delete %s (ID: %d): %w", label, id, err)
		}
		c.Recorder().Record(ChangeRecord{
			Action: ActionDelete, App: t.App, Endpoint: t.Endpoint,
			Object: label,
		})
		deleted++
	}

	return deleted, nil
}

// pruneLabel derives a human-readable identifier from a fetched object for
// logging and the change plan.
func pruneLabel(obj Object) string {
	for _, key := range []string{"name", "model", "slug", "prefix", "address", "display"} {
		if v, ok := obj[key].(string); ok && v != "" {
			return key + "=" + v
		}
	}
	return fmt.Sprintf("id=%d", utils.GetIDFromObject(obj))
}
