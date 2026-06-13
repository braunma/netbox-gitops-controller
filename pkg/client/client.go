package client

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// NetBoxClient handles all NetBox API operations
type NetBoxClient struct {
	baseURL      string
	token        string
	httpClient   *http.Client
	cache        *CacheManager
	tagManager   *TagManager
	logger       *utils.Logger
	recorder     *ChangeRecorder
	dryRun       bool
	managedTagID int

	// seen records the IDs of objects reconciled this run, keyed by
	// "app/endpoint". Prune uses it to tell declared objects apart from
	// orphans. incomplete marks endpoints where a declared object was
	// skipped (e.g. a missing dependency); Prune leaves those endpoints
	// alone so a transient skip never deletes a managed object. Both are
	// guarded by seenMu.
	seenMu     sync.Mutex
	seen       map[string]map[int]bool
	incomplete map[string]bool
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

const (
	// maxRetries is the number of additional attempts after a transient failure.
	maxRetries = 3
	// defaultPageSize overrides NetBox's default page size (50) for list requests.
	defaultPageSize = 250
)

// retryBaseDelay is doubled after each failed attempt (1s, 2s, 4s).
// Variable so tests can shorten it.
var retryBaseDelay = 1 * time.Second

// isRetryableStatus reports whether a response status may be retried.
// HTTP 429 is retryable for all methods. 5xx responses are retryable except
// for POST: the server may already have processed the creation, and a retry
// could create a duplicate object. Network errors (no response at all) are
// always retried by doWithRetry.
func isRetryableStatus(method string, statusCode int) bool {
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	if statusCode >= 500 {
		return method != "POST"
	}
	return false
}

// doWithRetry executes an HTTP request, retrying transient failures with
// exponential backoff. The request is rebuilt on every attempt because the
// body reader is consumed. It returns the response status code and body.
func (c *NetBoxClient) doWithRetry(method, requestURL string, jsonBody []byte) (int, []byte, error) {
	var lastErr error

	for attempt := 0; ; attempt++ {
		var bodyReader io.Reader
		if jsonBody != nil {
			bodyReader = bytes.NewReader(jsonBody)
		}

		req, err := http.NewRequest(method, requestURL, bodyReader)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "Token "+c.token)
		req.Header.Set("Accept", "application/json")
		if jsonBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err == nil {
			respBody, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				err = fmt.Errorf("failed to read response: %w", readErr)
			} else if !isRetryableStatus(method, resp.StatusCode) {
				return resp.StatusCode, respBody, nil
			} else {
				err = fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
			}
		} else {
			err = fmt.Errorf("request failed: %w", err)
		}

		lastErr = err
		if attempt >= maxRetries {
			break
		}

		delay := retryBaseDelay << attempt
		c.logger.Warning("Transient error on %s %s (attempt %d/%d), retrying in %s: %v",
			method, requestURL, attempt+1, maxRetries+1, delay, err)
		time.Sleep(delay)
	}

	return 0, nil, fmt.Errorf("giving up after %d attempts: %w", maxRetries+1, lastErr)
}

// Request makes an HTTP request to the NetBox API
func (c *NetBoxClient) Request(method, path string, body interface{}) (Object, error) {
	requestURL := c.baseURL + path

	var jsonBody []byte
	if body != nil {
		var err error
		jsonBody, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	if c.dryRun && (method == "POST" || method == "PATCH" || method == "PUT" || method == "DELETE") {
		c.logger.DryRun(method, path)
		return Object{"id": 0}, nil
	}

	statusCode, respBody, err := c.doWithRetry(method, requestURL, jsonBody)
	if err != nil {
		return nil, err
	}

	if statusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", statusCode, string(respBody))
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

// List makes a GET request and returns a list of objects, following the
// pagination links until all pages are fetched. NetBox paginates list
// responses (default page size: 50), so reading only the first page would
// silently truncate results in larger environments.
func (c *NetBoxClient) List(path string, filters map[string]interface{}) ([]Object, error) {
	queryParams := url.Values{}
	for k, v := range filters {
		queryParams.Add(k, fmt.Sprintf("%v", v))
	}
	if queryParams.Get("limit") == "" {
		queryParams.Set("limit", fmt.Sprintf("%d", defaultPageSize))
	}
	requestURL := c.baseURL + path + "?" + queryParams.Encode()

	var allResults []Object

	for requestURL != "" {
		statusCode, respBody, err := c.doWithRetry("GET", requestURL, nil)
		if err != nil {
			return nil, err
		}

		if statusCode >= 400 {
			return nil, fmt.Errorf("API error %d: %s", statusCode, string(respBody))
		}

		var result struct {
			Results []Object `json:"results"`
			Next    *string  `json:"next"`
		}

		if err := json.Unmarshal(respBody, &result); err != nil {
			// Try unmarshaling as direct array (non-paginated endpoint)
			var directResults []Object
			if err2 := json.Unmarshal(respBody, &directResults); err2 == nil {
				return append(allResults, directResults...), nil
			}
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}

		allResults = append(allResults, result.Results...)

		if result.Next != nil && *result.Next != "" {
			requestURL = *result.Next
		} else {
			requestURL = ""
		}
	}

	return allResults, nil
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
	obj, err := c.create(app, endpoint, data)
	if err == nil {
		c.Recorder().Record(ChangeRecord{
			Action: ActionCreate, App: app, Endpoint: endpoint,
			Object: objectLabel(data), Fields: data,
		})
	}
	return obj, err
}

// create issues the POST without recording; Apply records with a better label.
func (c *NetBoxClient) create(app, endpoint string, data map[string]interface{}) (Object, error) {
	path := fmt.Sprintf("/api/%s/%s/", app, endpoint)
	return c.Request("POST", path, data)
}

// Update updates an existing object
func (c *NetBoxClient) Update(app, endpoint string, id int, data map[string]interface{}) error {
	err := c.update(app, endpoint, id, data)
	if err == nil {
		c.Recorder().Record(ChangeRecord{
			Action: ActionUpdate, App: app, Endpoint: endpoint,
			Object: fmt.Sprintf("id=%d", id), Fields: data,
		})
	}
	return err
}

// update issues the PATCH without recording; Apply records with a better label.
func (c *NetBoxClient) update(app, endpoint string, id int, data map[string]interface{}) error {
	path := fmt.Sprintf("/api/%s/%s/%d/", app, endpoint, id)
	_, err := c.Request("PATCH", path, data)
	return err
}

// Delete deletes an object
func (c *NetBoxClient) Delete(app, endpoint string, id int) error {
	path := fmt.Sprintf("/api/%s/%s/%d/", app, endpoint, id)
	_, err := c.Request("DELETE", path, nil)
	if err == nil {
		c.Recorder().Record(ChangeRecord{
			Action: ActionDelete, App: app, Endpoint: endpoint,
			Object: fmt.Sprintf("id=%d", id),
		})
	}
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
		obj, err := c.create(app, endpoint, payload)
		if err != nil {
			return nil, err
		}
		c.Recorder().Record(ChangeRecord{
			Action: ActionCreate, App: app, Endpoint: endpoint,
			Object: c.formatLookup(lookup), Fields: payload,
		})
		c.markSeen(app, endpoint, utils.GetIDFromObject(obj))
		return obj, nil
	}

	// Update existing object
	obj := existing[0]
	objID := utils.GetIDFromObject(obj)
	if objID == 0 {
		// Enhanced error message with object details for debugging
		c.logger.Error("Failed to extract ID from object: %+v", nil, obj)
		if idVal, exists := obj["id"]; exists {
			c.logger.Error("ID field exists but has unexpected type: %T with value: %v", nil, idVal, idVal)
		} else {
			c.logger.Error("ID field does not exist in object. Available fields: %v", nil, getObjectKeys(obj))
		}
		return nil, fmt.Errorf("object has no ID (type: %s)", endpoint)
	}

	// Mark the object as reconciled so Prune does not treat it as an orphan,
	// whether or not it ends up changing below.
	c.markSeen(app, endpoint, objID)

	// Calculate diff
	changes := c.calculateDiff(obj, payload)
	if len(changes) > 0 {
		c.logger.Info("  ⟳ Updating %s (ID: %d): %v", endpoint, objID, c.formatLookup(lookup))
		c.printDiff("UPDATE", obj, changes)
		if err := c.update(app, endpoint, objID, changes); err != nil {
			return nil, fmt.Errorf("failed to update object: %w", err)
		}
		c.Recorder().Record(ChangeRecord{
			Action: ActionUpdate, App: app, Endpoint: endpoint,
			Object: c.formatLookup(lookup), Fields: changes,
		})
		c.logger.Success("  ✓ Update complete")
	} else {
		c.logger.Debug("  = No changes for %s (ID: %d)", endpoint, objID)
		c.Recorder().Record(ChangeRecord{
			Action: ActionUnchanged, App: app, Endpoint: endpoint,
			Object: c.formatLookup(lookup),
		})
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

		// Slice-valued fields (e.g. tags, tagged_vlans) are compared as an
		// order-insensitive set of referenced IDs. NetBox returns them as
		// nested objects ([{id, ...}]) while we send plain []int, so a direct
		// comparison would never match and the field would be re-PATCHed on
		// every run. Treating them as ID sets keeps list fields idempotent.
		if isListField(desiredValue) || isListField(existingValue) {
			if !c.idSetEqual(existingValue, desiredValue) {
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

// idSetEqual reports whether two slice-valued fields reference the same set of
// object IDs, ignoring order and representation (nested objects vs. plain IDs).
// It is used for list fields such as tags and tagged_vlans.
func (c *NetBoxClient) idSetEqual(existing, desired interface{}) bool {
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

// isListField reports whether a value is a slice that must be compared as an
// ID set rather than by direct equality (a plain == on two slices either
// always reports unequal across representations or panics on identical
// uncomparable types).
func isListField(v interface{}) bool {
	switch v.(type) {
	case []interface{}, []int, []string:
		return true
	}
	return false
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

// Recorder returns the change recorder, creating it on first use so that
// directly constructed clients (e.g. in tests) work without extra setup.
func (c *NetBoxClient) Recorder() *ChangeRecorder {
	if c.recorder == nil {
		c.recorder = NewChangeRecorder()
	}
	return c.recorder
}

// markSeen records that the object with the given ID at app/endpoint was
// reconciled during this run, so Prune will not delete it as an orphan.
// Zero IDs (e.g. dry-run creates) are ignored.
func (c *NetBoxClient) markSeen(app, endpoint string, id int) {
	if id == 0 {
		return
	}
	c.seenMu.Lock()
	defer c.seenMu.Unlock()
	if c.seen == nil {
		c.seen = make(map[string]map[int]bool)
	}
	key := app + "/" + endpoint
	if c.seen[key] == nil {
		c.seen[key] = make(map[int]bool)
	}
	c.seen[key][id] = true
}

// seenIDs returns the set of object IDs reconciled at app/endpoint this run.
// The returned map must not be mutated; it is safe to read after the
// reconciliation phases complete, since Prune runs once no further objects
// are applied.
func (c *NetBoxClient) seenIDs(app, endpoint string) map[int]bool {
	c.seenMu.Lock()
	defer c.seenMu.Unlock()
	return c.seen[app+"/"+endpoint]
}

// MarkReconcileIncomplete records that reconciliation could not fully process
// the given endpoint this run — for example an object declared in YAML was
// skipped because a dependency was missing. Prune leaves such endpoints
// untouched: a skipped object is not marked seen and would otherwise look
// like an orphan, so pruning it would delete a declared object.
func (c *NetBoxClient) MarkReconcileIncomplete(app, endpoint string) {
	c.seenMu.Lock()
	defer c.seenMu.Unlock()
	if c.incomplete == nil {
		c.incomplete = make(map[string]bool)
	}
	c.incomplete[app+"/"+endpoint] = true
}

// reconcileIncomplete reports whether MarkReconcileIncomplete was called for
// the given endpoint this run.
func (c *NetBoxClient) reconcileIncomplete(app, endpoint string) bool {
	c.seenMu.Lock()
	defer c.seenMu.Unlock()
	return c.incomplete[app+"/"+endpoint]
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

// getObjectKeys returns a list of keys in an object for debugging
func getObjectKeys(obj Object) []string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	return keys
}
