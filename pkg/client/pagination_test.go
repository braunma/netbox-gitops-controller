// SPDX-License-Identifier: Apache-2.0

package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

func newTestClient(srv *httptest.Server) *NetBoxClient {
	return &NetBoxClient{
		baseURL:    srv.URL,
		token:      "test-token",
		httpClient: srv.Client(),
		logger:     utils.NewLogger(false),
	}
}

// TestListFollowsPagination is the regression test for the truncation bug:
// List must follow the `next` links until all pages are fetched.
func TestListFollowsPagination(t *testing.T) {
	const totalPages = 3
	const itemsPerPage = 50

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 1
		fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)

		results := make([]Object, 0, itemsPerPage)
		for i := 0; i < itemsPerPage; i++ {
			results = append(results, Object{"id": float64((page-1)*itemsPerPage + i + 1)})
		}

		resp := map[string]interface{}{"results": results}
		if page < totalPages {
			resp["next"] = fmt.Sprintf("%s/api/dcim/devices/?page=%d", srv.URL, page+1)
		} else {
			resp["next"] = nil
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	objects, err := c.List("/api/dcim/devices/", nil)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	want := totalPages * itemsPerPage
	if len(objects) != want {
		t.Errorf("List() returned %d objects, expected %d (pagination not followed)", len(objects), want)
	}
}

// TestListSetsPageSize verifies the larger default page size is requested.
func TestListSetsPageSize(t *testing.T) {
	var gotLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results": [], "next": null}`)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if _, err := c.List("/api/dcim/sites/", nil); err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if gotLimit != fmt.Sprintf("%d", defaultPageSize) {
		t.Errorf("List() requested limit=%q, expected %d", gotLimit, defaultPageSize)
	}
}

// TestRetryOnTransientError verifies that 5xx responses are retried for GET.
func TestRetryOnTransientError(t *testing.T) {
	oldDelay := retryBaseDelay
	retryBaseDelay = time.Millisecond
	defer func() { retryBaseDelay = oldDelay }()

	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results": [{"id": 1}], "next": null}`)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	objects, err := c.List("/api/dcim/sites/", nil)
	if err != nil {
		t.Fatalf("List() error after retries = %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts (2 failures + 1 success), got %d", attempts)
	}
	if len(objects) != 1 {
		t.Errorf("List() returned %d objects, expected 1", len(objects))
	}
}

// TestNoRetryOnPOST5xx verifies POST is not retried on 5xx: the server may
// already have processed the creation, and retrying could create duplicates.
func TestNoRetryOnPOST5xx(t *testing.T) {
	oldDelay := retryBaseDelay
	retryBaseDelay = time.Millisecond
	defer func() { retryBaseDelay = oldDelay }()

	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if _, err := c.Request("POST", "/api/dcim/devices/", map[string]interface{}{"name": "x"}); err == nil {
		t.Fatal("Request() expected error on 500, got nil")
	}
	if attempts != 1 {
		t.Errorf("POST on 5xx must not be retried: expected 1 attempt, got %d", attempts)
	}
}

// TestNoRetryOn4xx verifies client errors fail immediately.
func TestNoRetryOn4xx(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"type": ["This field may not be blank."]}`)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if _, err := c.List("/api/dcim/sites/", nil); err == nil {
		t.Fatal("List() expected error on 400, got nil")
	}
	if attempts != 1 {
		t.Errorf("4xx must not be retried: expected 1 attempt, got %d", attempts)
	}
}
