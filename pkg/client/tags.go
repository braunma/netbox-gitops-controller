package client

import (
	"fmt"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// TagManager handles tag operations
type TagManager struct {
	client *NetBoxClient
}

// NewTagManager creates a new tag manager
func NewTagManager(client *NetBoxClient) *TagManager {
	return &TagManager{client: client}
}

// Ensure ensures a tag exists, creating it if necessary
func (tm *TagManager) Ensure(slug string) (int, error) {
	if tm.client.dryRun {
		return 0, nil
	}

	// Try to find existing tag
	tags, err := tm.client.Filter("extras", "tags", map[string]interface{}{"slug": slug})
	if err != nil {
		return 0, fmt.Errorf("failed to filter tags: %w", err)
	}

	if len(tags) > 0 {
		return utils.GetIDFromObject(tags[0]), nil
	}

	// Create the tag
	tagData := map[string]interface{}{
		"slug":  slug,
		"name":  constants.ManagedTagName,
		"color": constants.ManagedTagColor,
	}

	if slug == constants.ManagedTagSlug {
		tagData["description"] = constants.ManagedTagDescription
	}

	tag, err := tm.client.Create("extras", "tags", tagData)
	if err != nil {
		// Handle race condition - another process might have created it
		tm.client.logger.Warning("Tag creation failed, retrying lookup: %v", err)
		tags, err := tm.client.Filter("extras", "tags", map[string]interface{}{"slug": slug})
		if err != nil {
			return 0, fmt.Errorf("failed to retry tag lookup: %w", err)
		}
		if len(tags) > 0 {
			return utils.GetIDFromObject(tags[0]), nil
		}
		return 0, fmt.Errorf("failed to create tag: %w", err)
	}

	tm.client.logger.Success("Created system tag: %s", slug)
	return utils.GetIDFromObject(tag), nil
}

// GetID retrieves the ID of a tag by slug
func (tm *TagManager) GetID(slug string) (int, error) {
	tags, err := tm.client.Filter("extras", "tags", map[string]interface{}{"slug": slug})
	if err != nil {
		return 0, fmt.Errorf("failed to filter tags: %w", err)
	}

	if len(tags) == 0 {
		return 0, fmt.Errorf("tag %s not found", slug)
	}

	return utils.GetIDFromObject(tags[0]), nil
}

// IsManaged checks if an object is managed by gitops
func (tm *TagManager) IsManaged(obj Object, managedTagID int) bool {
	tags, ok := obj["tags"].([]interface{})
	if !ok {
		return false
	}

	for _, tag := range tags {
		if id := utils.GetIDFromObject(tag); id == managedTagID {
			return true
		}

		// Check slug as fallback
		if tagMap, ok := tag.(map[string]interface{}); ok {
			if slug, ok := tagMap["slug"].(string); ok && slug == constants.ManagedTagSlug {
				return true
			}
		}
	}

	return false
}

// InjectTag adds the managed tag to a payload
func (tm *TagManager) InjectTag(payload map[string]interface{}, tagID int) map[string]interface{} {
	if tagID == 0 {
		return payload
	}

	result := make(map[string]interface{})
	for k, v := range payload {
		result[k] = v
	}

	// Get existing tags
	existingTags, ok := result["tags"].([]interface{})
	if !ok {
		existingTags = []interface{}{}
	}

	// Convert to IDs and check if managed tag is present
	tagIDs := []int{}
	hasManaged := false

	for _, tag := range existingTags {
		switch v := tag.(type) {
		case int:
			tagIDs = append(tagIDs, v)
			if v == tagID {
				hasManaged = true
			}
		case map[string]interface{}:
			if id := utils.GetIDFromObject(v); id != 0 {
				tagIDs = append(tagIDs, id)
				if id == tagID {
					hasManaged = true
				}
			}
		}
	}

	// Add managed tag if not present
	if !hasManaged {
		tagIDs = append(tagIDs, tagID)
	}

	result["tags"] = tagIDs
	return result
}

// ExtractTagIDs extracts tag IDs from a tag list
func (tm *TagManager) ExtractTagIDs(tags []interface{}) []int {
	var ids []int

	for _, tag := range tags {
		if id := utils.GetIDFromObject(tag); id != 0 {
			ids = append(ids, id)
		}
	}

	return ids
}
