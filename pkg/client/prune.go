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
	for _, t := range targets {
		if err := c.pruneTarget(t); err != nil {
			return fmt.Errorf("failed to prune %s/%s: %w", t.App, t.Endpoint, err)
		}
	}
	return nil
}

// pruneTarget deletes the orphaned objects for a single endpoint.
func (c *NetBoxClient) pruneTarget(t PruneTarget) error {
	objects, err := c.Filter(t.App, t.Endpoint, map[string]interface{}{
		"tag": constants.ManagedTagSlug,
	})
	if err != nil {
		return err
	}

	keep := make(map[string]bool, len(t.KeepSlugs))
	for _, s := range t.KeepSlugs {
		keep[s] = true
	}

	seen := c.seenIDs(t.App, t.Endpoint)

	for _, obj := range objects {
		id := utils.GetIDFromObject(obj)
		if id == 0 || seen[id] {
			continue
		}
		if slug, ok := obj["slug"].(string); ok && keep[slug] {
			continue
		}

		label := pruneLabel(obj)
		c.logger.Warning("  ✗ Pruning orphaned %s: %s (ID: %d)", t.Endpoint, label, id)

		path := fmt.Sprintf("/api/%s/%s/%d/", t.App, t.Endpoint, id)
		if _, err := c.Request("DELETE", path, nil); err != nil {
			return fmt.Errorf("failed to delete %s (ID: %d): %w", label, id, err)
		}
		c.Recorder().Record(ChangeRecord{
			Action: ActionDelete, App: t.App, Endpoint: t.Endpoint,
			Object: label,
		})
	}

	return nil
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
