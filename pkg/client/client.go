// SPDX-License-Identifier: Apache-2.0

package client

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braunma/netbox-gitops-controller/internal/constants"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// NetBoxClient handles all NetBox API operations
type NetBoxClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
	cache      *CacheManager
	tagManager *TagManager
	logger     *utils.Logger
	recorder   *ChangeRecorder
	dryRun     bool
	// adopt, when set, restricts every UPDATE to the managed tag alone: an
	// existing object is brought under management (tagged) without any other
	// field being written. Creates are unaffected. This makes a first sync over
	// a populated NetBox structurally incapable of reformatting a field.
	adopt        bool
	managedTagID int
	// version is the NetBox release reported by /api/status/, learned by
	// CheckVersion. The zero value means "not determined".
	version NetBoxVersion

	// seen records the IDs of objects reconciled this run, keyed by
	// "app/endpoint". Prune uses it to tell declared objects apart from
	// orphans. incomplete marks endpoints where a declared object was
	// skipped (e.g. a missing dependency); Prune leaves those endpoints
	// alone so a transient skip never deletes a managed object. Both are
	// guarded by seenMu.
	seenMu     sync.Mutex
	seen       map[string]map[int]bool
	incomplete map[string]bool
	// referenced records objects that something reconciled this run points at,
	// keyed by "app/endpoint". Prune refuses to delete them: an object still in
	// use is not an orphan, whatever the YAML no longer says about it.
	referenced map[string]map[int]bool
	// schemas memoises the OPTIONS response of each endpoint, so live
	// validation asks the server what it accepts once rather than per object.
	schemas schemaCache
	// assertSites arms the --assert-site destination guard (see guard.go).
	assertSites   map[string]bool
	assertSiteIDs map[int]bool
	sharedTouched map[string]bool
}

// envIsTrue reports whether an environment variable is set to something that
// means yes. Accepts what strconv.ParseBool does, plus "yes" and "on", so a
// config file written by hand behaves the way it reads.
func envIsTrue(name string) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	switch value {
	case "":
		return false
	case "yes", "on":
		return true
	}
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

// NewClient creates a new NetBox API client.
//
// TLS certificates are verified. A NetBox behind a self-signed or internal CA
// certificate can opt out with IGNORE_SSL_ERRORS, which is loud about it:
// the alternative — the unconditional skip this had before — silently
// accepted any certificate against production too.
func NewClient(baseURL, token string, dryRun bool) (*NetBoxClient, error) {
	logger := utils.NewLogger(dryRun)

	skipVerify := envIsTrue("IGNORE_SSL_ERRORS")
	if skipVerify {
		logger.Warning("IGNORE_SSL_ERRORS is set: the NetBox TLS certificate will not be verified")
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify}, // #nosec G402 -- opt-in, warned about, for self-signed dev instances
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
		req.Header.Set("Authorization", c.authHeader())
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

// netboxV2TokenPrefix marks a NetBox 4.5+ "v2" API token. Such a token is
// presented whole as "Bearer nbt_<key>.<secret>", while the older v1 token is
// sent as "Token <key>". The prefix on the token itself is what tells them
// apart, so an operator can paste whichever their NetBox issued without
// configuring anything else.
//
// This matters for containerised deployments: the official NetBox image's
// bootstrap only ever creates a v2 token, and v1 tokens are removed entirely
// in NetBox 4.7.
const netboxV2TokenPrefix = "nbt_"

// authHeader returns the Authorization header value for the configured token.
func (c *NetBoxClient) authHeader() string {
	if strings.HasPrefix(c.token, netboxV2TokenPrefix) {
		return "Bearer " + c.token
	}
	return "Token " + c.token
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
	// Apply has its own filtered path (it calls c.update directly), so the
	// safety rules are enforced here for the direct callers: setting a primary
	// IP, installing a device into a bay, updating a cable. Without this they
	// would write fields that --adopt promises not to touch, and land writes
	// that --assert-site never sees.
	if c.adopt {
		data = adoptChanges(data)
		if len(data) == 0 {
			c.logger.Debug("  = Adoption mode: skipping non-tag update of %s/%s (ID: %d)", app, endpoint, id)
			return nil
		}
	}
	if err := c.guardDirectUpdate(app, endpoint, id, data); err != nil {
		return err
	}

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
	// Inject managed tag, except on endpoints whose objects are not taggable
	// (NetBox would reject an unknown `tags` field).
	if !constants.UntaggableEndpoints[endpoint] {
		payload = c.tagManager.InjectTag(payload, c.managedTagID)
	}

	payload = trimStringValues(payload)

	c.logger.Debug("  → Applying %s with lookup: %v", endpoint, lookup)
	c.markReferenced(payload)

	// A lookup scoped by a reference that does not exist yet cannot match
	// anything, so skip the query and plan a create. This happens in --dry-run
	// against an empty NetBox: an object planned but never written is
	// registered with id 0, and NetBox rejects "site_id=0" as an invalid
	// choice rather than returning an empty result — which used to abort the
	// first dry-run, the very run the documentation tells you to start with.
	// In a real apply the referenced object has been created, so its id is
	// never 0 and this never triggers.
	var existing []Object
	if unresolved, ok := unresolvedReference(lookup); ok {
		c.logger.Debug("  → %s is scoped by %s, which is only planned in this run; treating as new", endpoint, unresolved)
	} else {
		var err error
		existing, err = c.Filter(app, endpoint, lookup)
		if err != nil {
			return nil, fmt.Errorf("failed to filter objects: %w", err)
		}
	}

	// Destination guard (--assert-site): resolve this write to a site and
	// refuse it before anything is written if it lands, or already sits,
	// outside the allowed set. Runs in --dry-run too, so a pipeline checks it
	// before an apply job exists.
	var existingForGuard Object
	if len(existing) > 0 {
		existingForGuard = existing[0]
	}
	if err := c.checkSiteGuard(endpoint, existingForGuard, payload); err != nil {
		return nil, err
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
	if c.adopt {
		// Adoption mode: bring the object under management without touching
		// anything else. Keep only the managed tag; drop every other change so
		// a first sync over a populated NetBox cannot reformat a field.
		changes = adoptChanges(changes)
	}
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

// trimStringValues returns a copy of the payload with leading and trailing
// whitespace removed from its string values.
//
// Django REST Framework strips whitespace from character fields on write, so a
// value ending in a newline — which a YAML block scalar produces, and which
// the community library uses for `comments` — is stored trimmed. Sending the
// untrimmed value therefore never matches what comes back, and the object is
// re-PATCHed on every run. Trimming here makes the payload say what NetBox
// will actually store; the whitespace could not have been preserved anyway.
func trimStringValues(payload map[string]interface{}) map[string]interface{} {
	trimmed := make(map[string]interface{}, len(payload))
	for key, value := range payload {
		if s, ok := value.(string); ok {
			trimmed[key] = strings.TrimSpace(s)
			continue
		}
		trimmed[key] = value
	}
	return trimmed
}

// unresolvedReference reports the first lookup key that scopes the query by a
// reference id of 0 — an object planned in this run but not yet written.
func unresolvedReference(lookup map[string]interface{}) (string, bool) {
	for key, value := range lookup {
		if !strings.HasSuffix(key, "_id") {
			continue
		}
		if n, ok := toFloat(value); ok && n == 0 {
			return key, true
		}
	}
	return "", false
}

// referenceFieldEndpoints maps the payload fields that carry an object
// reference to the endpoint that object lives at. Only unambiguous fields are
// listed: mis-attributing a reference would make Prune skip a genuine orphan,
// which is worse than missing one and letting NetBox's own foreign-key check
// catch it. Fields whose target depends on context ("group", "parent") are
// deliberately absent.
var referenceFieldEndpoints = map[string]string{
	"site":            "dcim/sites",
	"rack":            "dcim/racks",
	"device":          "dcim/devices",
	"device_type":     "dcim/device-types",
	"module_type":     "dcim/module-types",
	"platform":        "dcim/platforms",
	"manufacturer":    "dcim/manufacturers",
	"role":            "dcim/device-roles",
	"tenant":          "tenancy/tenants",
	"vrf":             "ipam/vrfs",
	"vlan":            "ipam/vlans",
	"untagged_vlan":   "ipam/vlans",
	"tagged_vlans":    "ipam/vlans",
	"cluster":         "virtualization/clusters",
	"virtual_machine": "virtualization/virtual-machines",
	"lag":             "dcim/interfaces",
	"rear_port":       "dcim/rear-ports",
}

// markReferenced records every object reference carried by an outgoing
// payload. A string value is ignored: some fields (a prefix's `role`) are sent
// as a name rather than an ID.
func (c *NetBoxClient) markReferenced(payload map[string]interface{}) {
	for key, value := range payload {
		endpoint, ok := referenceFieldEndpoints[key]
		if !ok {
			continue
		}
		for _, id := range referenceIDs(value) {
			c.markReferencedID(endpoint, id)
		}
	}

	// rear_ports (NetBox 4.6 front port terminations) nests its references one
	// level down, as {position, rear_port, rear_port_position}.
	if mappings, ok := payload["rear_ports"].([]interface{}); ok {
		for _, m := range mappings {
			entry, ok := m.(map[string]interface{})
			if !ok {
				continue
			}
			for _, id := range referenceIDs(entry["rear_port"]) {
				c.markReferencedID("dcim/rear-ports", id)
			}
		}
	}
}

// referenceIDs extracts the object IDs a payload value refers to, covering the
// scalar and list forms the reconcilers send.
func referenceIDs(value interface{}) []int {
	switch v := value.(type) {
	case int:
		if v > 0 {
			return []int{v}
		}
	case []int:
		out := make([]int, 0, len(v))
		for _, id := range v {
			if id > 0 {
				out = append(out, id)
			}
		}
		return out
	case []interface{}:
		out := make([]int, 0, len(v))
		for _, item := range v {
			if id := utils.GetIDFromObject(item); id > 0 {
				out = append(out, id)
			}
		}
		return out
	}
	return nil
}

func (c *NetBoxClient) markReferencedID(endpoint string, id int) {
	c.seenMu.Lock()
	defer c.seenMu.Unlock()
	if c.referenced == nil {
		c.referenced = make(map[string]map[int]bool)
	}
	if c.referenced[endpoint] == nil {
		c.referenced[endpoint] = make(map[int]bool)
	}
	c.referenced[endpoint][id] = true
}

// isReferenced reports whether something reconciled this run points at the
// given object.
func (c *NetBoxClient) isReferenced(app, endpoint string, id int) bool {
	c.seenMu.Lock()
	defer c.seenMu.Unlock()
	return c.referenced[app+"/"+endpoint][id]
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

		// vid_ranges (NetBox 4.2 VLAN-group VID bounds) is a list of [min, max]
		// pairs, not a list of object references, so the ID-set comparison below
		// can't see it (every pair extracts to no ID and the field would look
		// permanently equal). Compare it as a canonical set of integer ranges,
		// tolerant of the array and "min-max" string representations NetBox may
		// return.
		if key == "vid_ranges" {
			if !vidRangesEqual(existingValue, desiredValue) {
				changes[key] = desiredValue
			}
			continue
		}

		// rear_ports (NetBox 4.6 front port terminations) is a list of
		// {position, rear_port, rear_port_position} mappings. Its entries carry
		// no "id", so the ID-set comparison below would read both sides as
		// empty and report the field permanently equal — a changed mapping
		// would never be written. Compare the mappings themselves.
		if key == "rear_ports" {
			if !portMappingsEqual(existingValue, desiredValue) {
				changes[key] = desiredValue
			}
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

		// Map-valued desired fields (currently only custom_fields) are plain
		// dictionaries of scalar values, not related-object references. NetBox
		// returns the full set of defined custom fields, so we compare the
		// desired entries as a subset: the field is changed only when a declared
		// value differs from what NetBox reports. Without this, the generic
		// nested-object handling below reduced the existing map to a scalar (0),
		// which never matched the desired map and re-PATCHed it on every run.
		if desiredMap, ok := desiredValue.(map[string]interface{}); ok {
			existingMap, _ := existingValue.(map[string]interface{})
			if !mapSubsetEqual(existingMap, desiredMap) {
				changes[key] = desiredValue
			}
			continue
		}

		// Handle nested objects. NetBox renders two different things as nested
		// objects in API responses:
		//   - related objects (site, role, device_type, ...): {"id": N, ...}
		//   - choice/enum fields (status, type, mode, face, ...):
		//     {"value": "active", "label": "Active"}
		// We send the former as a plain ID and the latter as a plain string, so
		// to stay idempotent we must reduce the existing value to the matching
		// scalar: the "id" for relations, the "value" for choice fields. The
		// previous code always extracted "id", so choice fields resolved to 0,
		// never matched the desired string, and were re-PATCHed on every run.
		if existingMap, ok := existingValue.(map[string]interface{}); ok {
			existingValue = scalarFromNetBoxObject(existingMap)
		}

		// Compare values
		if !valuesEqual(existingValue, desiredValue) {
			changes[key] = desiredValue
		}
	}

	return changes
}

// portMapping is a canonical front-port-to-rear-port termination.
type portMapping struct{ position, rearPort, rearPortPosition int }

// portMappingsEqual compares two rear_ports values as an order-insensitive set
// of terminations, tolerating the nested-object form NetBox returns for
// rear_port and the plain ID we send.
func portMappingsEqual(existing, desired interface{}) bool {
	a, b := canonicalPortMappings(existing), canonicalPortMappings(desired)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// canonicalPortMappings normalises a rear_ports value to a sorted slice.
func canonicalPortMappings(v interface{}) []portMapping {
	items, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]portMapping, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		out = append(out, portMapping{
			position:         utils.GetIDFromObject(m["position"]),
			rearPort:         utils.GetIDFromObject(m["rear_port"]),
			rearPortPosition: utils.GetIDFromObject(m["rear_port_position"]),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].position != out[j].position {
			return out[i].position < out[j].position
		}
		if out[i].rearPort != out[j].rearPort {
			return out[i].rearPort < out[j].rearPort
		}
		return out[i].rearPortPosition < out[j].rearPortPosition
	})
	return out
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

// scalarFromNetBoxObject reduces a nested NetBox object to the scalar that a
// flat desired payload is compared against. Related objects carry an "id";
// choice/enum fields instead carry a "value" (e.g. {"value": "active"}). For
// choices we compare on "value" so a string payload like "active" matches.
func scalarFromNetBoxObject(m map[string]interface{}) interface{} {
	if _, ok := m["id"]; ok {
		return utils.GetIDFromObject(m)
	}
	if v, ok := m["value"]; ok {
		return v
	}
	return utils.GetIDFromObject(m)
}

// mapSubsetEqual reports whether every entry in desired is present in existing
// with an equal value. It is used for dictionary-valued fields such as
// custom_fields, where NetBox returns every defined field but the desired
// payload only declares the subset the controller manages. A declared field
// that is absent from existing (e.g. a custom field defined after the object was
// created) counts as a difference so the value gets written.
func mapSubsetEqual(existing, desired map[string]interface{}) bool {
	for key, desiredValue := range desired {
		existingValue, ok := existing[key]
		if !ok || !valuesEqual(existingValue, desiredValue) {
			return false
		}
	}
	return true
}

// vidRangesEqual reports whether two vid_ranges values describe the same set of
// VID ranges, independent of order and representation. NetBox 4.2 may serialize
// a range either as a two-element array ([1, 4094]) or as a "min-max" string
// ("1-4094"); both reduce to the same canonical pairs here.
func vidRangesEqual(existing, desired interface{}) bool {
	a := canonicalVIDRanges(existing)
	b := canonicalVIDRanges(desired)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// canonicalVIDRanges normalizes a vid_ranges value into a sorted slice of
// [min, max] pairs. Unrecognized shapes yield an empty slice.
func canonicalVIDRanges(v interface{}) [][2]int {
	list, ok := v.([]interface{})
	if !ok {
		return nil
	}

	ranges := make([][2]int, 0, len(list))
	for _, item := range list {
		switch r := item.(type) {
		case []interface{}:
			if len(r) == 2 {
				ranges = append(ranges, [2]int{utils.GetIDFromObject(r[0]), utils.GetIDFromObject(r[1])})
			}
		case string:
			var lo, hi int
			if _, err := fmt.Sscanf(r, "%d-%d", &lo, &hi); err == nil {
				ranges = append(ranges, [2]int{lo, hi})
			}
		case map[string]interface{}:
			// Some serializers render a range as {"start": N, "end": M}.
			ranges = append(ranges, [2]int{utils.GetIDFromObject(r["start"]), utils.GetIDFromObject(r["end"])})
		}
	}

	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i][0] != ranges[j][0] {
			return ranges[i][0] < ranges[j][0]
		}
		return ranges[i][1] < ranges[j][1]
	})
	return ranges
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

	// NetBox renders decimal fields (u_height, weight, ...) as JSON strings
	// when its serializer coerces decimals, so a numeric payload would never
	// match the string it reads back and the object would be re-PATCHed on
	// every run. Compare numerically whenever one side is a number and the
	// other is a string that cleanly parses as one; a non-numeric string
	// falls through to the plain comparison below.
	if n, s, ok := numberAndNumericString(a, b); ok {
		return n == s
	}

	return a == b
}

// numberAndNumericString reports whether a and b are a number and a numeric
// string in either order, returning both as float64.
func numberAndNumericString(a, b interface{}) (float64, float64, bool) {
	an, aNum := toFloat(a)
	bn, bNum := toFloat(b)

	as, aStr := a.(string)
	bs, bStr := b.(string)

	switch {
	case aNum && bStr:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(bs), 64)
		return an, parsed, err == nil
	case bNum && aStr:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(as), 64)
		return parsed, bn, err == nil
	}
	return 0, 0, false
}

// toFloat converts the numeric types that reach valuesEqual (JSON decoding
// yields float64; payloads may carry Go ints) to float64.
func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
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

// SetAdopt enables adoption mode: UPDATEs write only the managed tag.
func (c *NetBoxClient) SetAdopt(enabled bool) {
	c.adopt = enabled
}

// IsAdopting reports whether adoption mode is on.
func (c *NetBoxClient) IsAdopting() bool {
	return c.adopt
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

// adoptChanges reduces a calculated diff to the managed-tag change alone, for
// adoption mode. Every field other than tags is dropped, so an UPDATE writes
// only the tag that brings an existing object under management.
func adoptChanges(changes map[string]interface{}) map[string]interface{} {
	if tags, ok := changes["tags"]; ok {
		return map[string]interface{}{"tags": tags}
	}
	return nil
}
