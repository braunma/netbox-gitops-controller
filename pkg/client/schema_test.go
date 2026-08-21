// SPDX-License-Identifier: Apache-2.0

package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// optionsBody is the shape NetBox returns for OPTIONS on a list endpoint.
const optionsBody = `{
  "name": "Interface List",
  "actions": {
    "POST": {
      "device":     {"type": "field", "required": true},
      "name":       {"type": "string", "required": true, "max_length": 64},
      "label":      {"type": "string", "required": false, "max_length": 64},
      "type":       {"type": "choice", "required": true,
                     "choices": [{"value": "virtual", "display_name": "Virtual"},
                                 {"value": "25gbase-x-sfp28", "display_name": "SFP28 (25GE)"}]},
      "mode":       {"type": "choice", "required": false,
                     "choices": [{"value": "access", "display_name": "Access"}]},
      "mtu":        {"type": "integer", "required": false}
    }
  }
}`

func TestParseEndpointSchema(t *testing.T) {
	var raw Object
	if err := json.Unmarshal([]byte(optionsBody), &raw); err != nil {
		t.Fatal(err)
	}
	schema := parseEndpointSchema(raw)

	if got := len(schema.Choices); got != 2 {
		t.Errorf("got %d choice fields, want 2 (type, mode): %v", got, schema.Choices)
	}
	if got, want := len(schema.Choices["type"]), 2; got != want {
		t.Errorf("got %d type choices, want %d", got, want)
	}
	if got, want := schema.MaxLength["name"], 64; got != want {
		t.Errorf("name max_length = %d, want %d", got, want)
	}
	if !schema.Required["device"] || !schema.Required["type"] {
		t.Errorf("device and type are required in the response, got %v", schema.Required)
	}
	if schema.Required["label"] {
		t.Error("label is not required in the response")
	}
	if _, constrained := schema.Choices["mtu"]; constrained {
		t.Error("a field with no choices must not appear as constrained")
	}
}

// Some NetBox choice sets arrive grouped one level deep. Skipping the nesting
// would leave the field looking unconstrained, which silently accepts anything.
func TestParseEndpointSchemaFlattensGroupedChoices(t *testing.T) {
	var raw Object
	if err := json.Unmarshal([]byte(`{"actions": {"POST": {"type": {"choices": [
		{"display_name": "Ethernet (fixed)", "choices": [
			{"value": "1000base-t", "display_name": "1GE"},
			{"value": "10gbase-t",  "display_name": "10GE"}]},
		{"display_name": "Virtual", "choices": [{"value": "lag", "display_name": "LAG"}]}]}}}}`), &raw); err != nil {
		t.Fatal(err)
	}
	schema := parseEndpointSchema(raw)

	want := []string{"1000base-t", "10gbase-t", "lag"}
	if got := schema.Choices["type"]; len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, value := range want {
		if !schema.Allows("type", value) {
			t.Errorf("%q should be allowed after flattening", value)
		}
	}
}

// A token that cannot POST gets an OPTIONS response with no POST action. That
// must constrain nothing rather than reject everything.
func TestParseEndpointSchemaWithoutPostAction(t *testing.T) {
	for _, body := range []string{`{}`, `{"actions": {}}`, `{"actions": {"GET": {}}}`} {
		var raw Object
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatal(err)
		}
		schema := parseEndpointSchema(raw)
		if len(schema.Choices) != 0 {
			t.Errorf("%s: got choices %v, want none", body, schema.Choices)
		}
		if !schema.Allows("type", "anything-at-all") {
			t.Errorf("%s: an unknown schema must not reject values", body)
		}
	}
}

func TestEndpointSchemaAllows(t *testing.T) {
	schema := EndpointSchema{Choices: map[string][]string{"status": {"active", "offline"}}}

	cases := []struct {
		field, value string
		want         bool
	}{
		{"status", "active", true},
		{"status", "offline", true},
		{"status", "activ", false},
		{"status", "", true},        // optional: NetBox applies its default
		{"colour", "puce", true},    // field this endpoint does not constrain
		{"status", "ACTIVE", false}, // NetBox choice values are case-sensitive
	}
	for _, tc := range cases {
		if got := schema.Allows(tc.field, tc.value); got != tc.want {
			t.Errorf("Allows(%q, %q) = %v, want %v", tc.field, tc.value, got, tc.want)
		}
	}
}

// The schema of an endpoint is fetched once per run: validation asks about
// hundreds of values across a handful of endpoints.
func TestSchemaIsFetchedOncePerEndpoint(t *testing.T) {
	var options, other int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			atomic.AddInt32(&options, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(optionsBody))
			return
		}
		atomic.AddInt32(&other, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL, "token", true)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		schema, err := c.Schema("dcim", "interfaces")
		if err != nil {
			t.Fatalf("Schema: %v", err)
		}
		if !schema.Allows("type", "virtual") {
			t.Fatal("expected the parsed schema on every call")
		}
	}

	if got := atomic.LoadInt32(&options); got != 1 {
		t.Errorf("OPTIONS was sent %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&other); got != 0 {
		t.Errorf("the schema fetch made %d non-OPTIONS requests, want 0", got)
	}
}

// Reading a schema is a read, so it must happen even in dry-run — the mode
// validation runs in.
func TestSchemaIsReadInDryRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(optionsBody))
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL, "token", true)
	if err != nil {
		t.Fatal(err)
	}
	if !c.IsDryRun() {
		t.Fatal("expected a dry-run client")
	}

	schema, err := c.Schema("dcim", "interfaces")
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	if len(schema.Choices) == 0 {
		t.Error("dry-run suppressed the OPTIONS read")
	}
}

func TestSchemaReportsAnUnreadableEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission"}`))
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL, "token", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Schema("dcim", "interfaces"); err == nil {
		t.Error("a 403 must be reported, not treated as an empty schema")
	}
}
