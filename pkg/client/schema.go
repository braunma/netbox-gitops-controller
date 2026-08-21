// SPDX-License-Identifier: Apache-2.0

package client

import (
	"fmt"
	"sync"
)

// EndpointSchema is what a NetBox endpoint says about itself in its OPTIONS
// response: which values each field accepts, how long a string may be, and
// which fields must be present.
//
// This is the authority the hardcoded lists in pkg/models can only approximate.
// A NetBox instance's choices depend on its release and its plugins — there are
// 216 interface types on 4.6 alone — so the only way to know that a value will
// be accepted is to ask the server that will receive it.
type EndpointSchema struct {
	// Choices maps a field name to the values it accepts. A field absent from
	// the map is not a choice field on this instance.
	Choices map[string][]string
	// MaxLength maps a field name to its character limit, where it has one.
	MaxLength map[string]int
	// Required lists the fields the endpoint will not accept a POST without.
	Required map[string]bool
}

// Allows reports whether value is one of the field's choices. A field this
// endpoint does not constrain, or an empty value, is allowed: optional fields
// are left to NetBox's own default.
func (s EndpointSchema) Allows(field, value string) bool {
	if value == "" {
		return true
	}
	choices, constrained := s.Choices[field]
	if !constrained {
		return true
	}
	for _, choice := range choices {
		if choice == value {
			return true
		}
	}
	return false
}

// schemaCache memoises OPTIONS responses for the life of a run.
type schemaCache struct {
	mu      sync.Mutex
	entries map[string]EndpointSchema
}

// Schema returns the OPTIONS-reported schema of an endpoint, fetching it once
// per run. The path is the usual "app/endpoint" pair, e.g. "dcim/interfaces".
func (c *NetBoxClient) Schema(app, endpoint string) (EndpointSchema, error) {
	key := app + "/" + endpoint

	c.schemas.mu.Lock()
	if cached, ok := c.schemas.entries[key]; ok {
		c.schemas.mu.Unlock()
		return cached, nil
	}
	c.schemas.mu.Unlock()

	// OPTIONS is a read: it must run even under --dry-run, which is the mode
	// this is for.
	raw, err := c.Request("OPTIONS", "/api/"+key+"/", nil)
	if err != nil {
		return EndpointSchema{}, fmt.Errorf("failed to read the schema of %s: %w", key, err)
	}

	schema := parseEndpointSchema(raw)

	c.schemas.mu.Lock()
	if c.schemas.entries == nil {
		c.schemas.entries = make(map[string]EndpointSchema)
	}
	c.schemas.entries[key] = schema
	c.schemas.mu.Unlock()

	return schema, nil
}

// parseEndpointSchema reads the POST action of an OPTIONS response:
//
//	{"actions": {"POST": {"type": {"choices": [{"value": "virtual", ...}],
//	                               "required": true, "max_length": 64}}}}
//
// An instance that reports no POST action — a read-only token, an endpoint
// behind a permission — yields an empty schema, which constrains nothing
// rather than rejecting everything.
func parseEndpointSchema(raw Object) EndpointSchema {
	schema := EndpointSchema{
		Choices:   make(map[string][]string),
		MaxLength: make(map[string]int),
		Required:  make(map[string]bool),
	}

	actions, ok := raw["actions"].(map[string]interface{})
	if !ok {
		return schema
	}
	post, ok := actions["POST"].(map[string]interface{})
	if !ok {
		return schema
	}

	for field, spec := range post {
		details, ok := spec.(map[string]interface{})
		if !ok {
			continue
		}
		if required, ok := details["required"].(bool); ok && required {
			schema.Required[field] = true
		}
		if max, ok := details["max_length"].(float64); ok {
			schema.MaxLength[field] = int(max)
		}
		if values := parseChoiceValues(details["choices"]); len(values) > 0 {
			schema.Choices[field] = values
		}
	}
	return schema
}

// parseChoiceValues collects the "value" of each choice. NetBox groups some
// choice sets one level deep — [{"display_name": "Ethernet", "choices": [...]}]
// — so a nested list is flattened rather than skipped.
func parseChoiceValues(raw interface{}) []string {
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}

	var values []string
	for _, entry := range list {
		choice, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		if nested := parseChoiceValues(choice["choices"]); len(nested) > 0 {
			values = append(values, nested...)
			continue
		}
		switch value := choice["value"].(type) {
		case string:
			values = append(values, value)
		case float64:
			values = append(values, fmt.Sprintf("%g", value))
		case bool:
			values = append(values, fmt.Sprintf("%t", value))
		}
	}
	return values
}
