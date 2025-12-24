package client

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// NetBoxClient handles all NetBox API operations
type NetBoxClient struct {
	baseURL       string
	token         string
	httpClient    *http.Client
	cache         *CacheManager
	tagManager    *TagManager
	logger        *utils.Logger
	dryRun        bool
	managedTagID  int
}

// NewClient creates a new NetBox API client
func NewClient(baseURL, token string, dryRun bool) (*NetBoxClient, error) {
	logger := utils.NewLogger(dryRun)

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	client := &NetBoxClient{
		baseURL:    baseURL,
		token:      token,
		httpClient: httpClient,
		logger:     logger,
		dryRun:     dryRun,
	}

	client.cache = NewCacheManager(client)
	client.tagManager = NewTagManager(client)

	// Ensure managed tag exists
	tagID, err := client.tagManager.Ensure(constants.ManagedTagSlug)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure managed tag: %w", err)
	}
	client.managedTagID = tagID

	return client, nil
}

// Object represents a generic NetBox object
type Object map[string]interface{}

// Request makes an HTTP request to the NetBox API
func (c *NetBoxClient) Request(method, path string, body interface{}) (Object, error) {
	url := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if c.dryRun && (method == "POST" || method == "PATCH" || method == "PUT" || method == "DELETE") {
		c.logger.DryRun(method, path)
		return Object{"id": 0}, nil
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	if len(respBody) == 0 {
		return nil, nil
	}

	var result Object
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return result, nil
}

// List makes a GET request and returns a list of objects
func (c *NetBoxClient) List(path string, filters map[string]interface{}) ([]Object, error) {
	url := c.baseURL + path

	if len(filters) > 0 {
		url += "?"
		for k, v := range filters {
			url += fmt.Sprintf("%s=%v&", k, v)
		}
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Results []Object `json:"results"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		// Try unmarshaling as direct array
		var directResults []Object
		if err2 := json.Unmarshal(respBody, &directResults); err2 == nil {
			return directResults, nil
		}
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return result.Results, nil
}

// Get retrieves a single object by ID
func (c *NetBoxClient) Get(app, endpoint string, id int) (Object, error) {
	path := fmt.Sprintf("/api/%s/%s/%d/", app, endpoint, id)
	return c.Request("GET", path, nil)
}

// Filter retrieves objects matching the given filters
func (c *NetBoxClient) Filter(app, endpoint string, filters map[string]interface{}) ([]Object, error) {
	path := fmt.Sprintf("/api/%s/%s/", app, endpoint)
	return c.List(path, filters)
}

// Create creates a new object
func (c *NetBoxClient) Create(app, endpoint string, data map[string]interface{}) (Object, error) {
	path := fmt.Sprintf("/api/%s/%s/", app, endpoint)
	return c.Request("POST", path, data)
}

// Update updates an existing object
func (c *NetBoxClient) Update(app, endpoint string, id int, data map[string]interface{}) error {
	path := fmt.Sprintf("/api/%s/%s/%d/", app, endpoint, id)
	_, err := c.Request("PATCH", path, data)
	return err
}

// Delete deletes an object
func (c *NetBoxClient) Delete(app, endpoint string, id int) error {
	path := fmt.Sprintf("/api/%s/%s/%d/", app, endpoint, id)
	_, err := c.Request("DELETE", path, nil)
	return err
}

// Apply creates or updates an object (idempotent)
func (c *NetBoxClient) Apply(app, endpoint string, lookup, payload map[string]interface{}) (Object, error) {
	// Inject managed tag
	payload = c.tagManager.InjectTag(payload, c.managedTagID)

	c.logger.Debug("  → Applying %s with lookup: %v", endpoint, lookup)

	// Try to find existing object
	existing, err := c.Filter(app, endpoint, lookup)
	if err != nil {
		return nil, fmt.Errorf("failed to filter objects: %w", err)
	}

	if len(existing) == 0 {
		// Create new object
		c.logger.Success("  ✓ Creating %s: %v", endpoint, c.formatLookup(lookup))
		c.printDiff("CREATE", nil, payload)
		return c.Create(app, endpoint, payload)
	}

	// Update existing object
	obj := existing[0]
	objID := utils.GetIDFromObject(obj)
	if objID == 0 {
		return nil, fmt.Errorf("object has no ID")
	}

	// Calculate diff
	changes := c.calculateDiff(obj, payload)
	if len(changes) > 0 {
		c.logger.Info("  ⟳ Updating %s (ID: %d): %v", endpoint, objID, c.formatLookup(lookup))
		c.printDiff("UPDATE", obj, changes)
		if err := c.Update(app, endpoint, objID, changes); err != nil {
			return nil, fmt.Errorf("failed to update object: %w", err)
		}
		c.logger.Success("  ✓ Update complete")
	} else {
		c.logger.Debug("  = No changes for %s (ID: %d)", endpoint, objID)
	}

	return obj, nil
}

// formatLookup formats lookup criteria for display
func (c *NetBoxClient) formatLookup(lookup map[string]interface{}) string {
	if name, ok := lookup["name"]; ok {
		return fmt.Sprintf("name=%v", name)
	}
	if slug, ok := lookup["slug"]; ok {
		return fmt.Sprintf("slug=%v", slug)
	}
	// Return first key-value pair
	for k, v := range lookup {
		return fmt.Sprintf("%s=%v", k, v)
	}
	return "{}"
}

// printDiff prints a visual diff for pipeline console visibility
func (c *NetBoxClient) printDiff(action string, existing Object, changes map[string]interface{}) {
	if c.dryRun {
		return // Dry run already shows the action
	}

	if action == "CREATE" {
		c.logger.Debug("    ┌─ Changes ────────────────────")
		for key, val := range changes {
			if key == "tags" {
				continue // Skip tags in diff
			}
			c.logger.Success("    │ + %s: %v", key, c.formatValue(val))
		}
		c.logger.Debug("    └──────────────────────────────")
		return
	}

	if action == "UPDATE" {
		c.logger.Debug("    ┌─ Changes ────────────────────")
		for key, newVal := range changes {
			if key == "tags" {
				continue
			}

			oldVal := existing[key]
			// Handle nested objects
			if oldMap, ok := oldVal.(map[string]interface{}); ok {
				if id, ok := oldMap["id"]; ok {
					oldVal = id
				}
			}

			c.logger.Warning("    │ ~ %s:", key)
			c.logger.Warning("    │   - %v", c.formatValue(oldVal))
			c.logger.Success("    │   + %v", c.formatValue(newVal))
		}
		c.logger.Debug("    └──────────────────────────────")
	}
}

// formatValue formats a value for display
func (c *NetBoxClient) formatValue(val interface{}) string {
	if val == nil {
		return "<nil>"
	}

	switch v := val.(type) {
	case string:
		return fmt.Sprintf("\"%s\"", v)
	case []interface{}:
		if len(v) == 0 {
			return "[]"
		}
		return fmt.Sprintf("[...%d items]", len(v))
	case map[string]interface{}:
		if id, ok := v["id"]; ok {
			return fmt.Sprintf("{id: %v}", id)
		}
		return "{...}"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// calculateDiff compares existing object with desired state
func (c *NetBoxClient) calculateDiff(existing Object, desired map[string]interface{}) map[string]interface{} {
	changes := make(map[string]interface{})

	for key, desiredValue := range desired {
		if desiredValue == nil {
			continue
		}

		existingValue, exists := existing[key]
		if !exists {
			changes[key] = desiredValue
			continue
		}

		// Handle tags specially
		if key == "tags" {
			if !c.tagsEqual(existingValue, desiredValue) {
				changes[key] = desiredValue
			}
			continue
		}

		// Handle nested objects (extract ID)
		if existingMap, ok := existingValue.(map[string]interface{}); ok {
			existingValue = utils.GetIDFromObject(existingMap)
		}

		// Compare values
		if !valuesEqual(existingValue, desiredValue) {
			changes[key] = desiredValue
		}
	}

	return changes
}

// tagsEqual compares two tag lists
func (c *NetBoxClient) tagsEqual(existing, desired interface{}) bool {
	existingTags := c.extractTagIDs(existing)
	desiredTags := c.extractTagIDs(desired)

	if len(existingTags) != len(desiredTags) {
		return false
	}

	existingMap := make(map[int]bool)
	for _, id := range existingTags {
		existingMap[id] = true
	}

	for _, id := range desiredTags {
		if !existingMap[id] {
			return false
		}
	}

	return true
}

// extractTagIDs extracts tag IDs from various formats
func (c *NetBoxClient) extractTagIDs(tags interface{}) []int {
	var ids []int

	switch v := tags.(type) {
	case []interface{}:
		for _, tag := range v {
			if id := utils.GetIDFromObject(tag); id != 0 {
				ids = append(ids, id)
			}
		}
	case []int:
		ids = v
	}

	return ids
}

// valuesEqual compares two values for equality
func valuesEqual(a, b interface{}) bool {
	// Handle type conversions
	switch av := a.(type) {
	case float64:
		if bv, ok := b.(int); ok {
			return av == float64(bv)
		}
	case int:
		if bv, ok := b.(float64); ok {
			return float64(av) == bv
		}
	case string:
		if bv, ok := b.(string); ok {
			return av == bv
		}
	}

	return a == b
}

// Cache returns the cache manager
func (c *NetBoxClient) Cache() *CacheManager {
	return c.cache
}

// Tags returns the tag manager
func (c *NetBoxClient) Tags() *TagManager {
	return c.tagManager
}

// SetDryRun sets the dry-run mode
func (c *NetBoxClient) SetDryRun(enabled bool) {
	c.dryRun = enabled
}

// IsDryRun returns the dry-run status
func (c *NetBoxClient) IsDryRun() bool {
	return c.dryRun
}

// ManagedTagID returns the managed tag ID
func (c *NetBoxClient) ManagedTagID() int {
	return c.managedTagID
}

// Logger returns the logger
func (c *NetBoxClient) Logger() *utils.Logger {
	return c.logger
}
