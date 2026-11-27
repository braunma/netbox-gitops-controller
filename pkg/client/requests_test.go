// SPDX-License-Identifier: Apache-2.0

package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Device and VM lists ask NetBox not to render config_context; every other
// list, and an explicit exclude, is left as it was.
func TestListExcludesConfigContext(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got[r.URL.Path] = r.URL.Query().Get("exclude")
		mu.Unlock()
		fmt.Fprint(w, `{"results": [], "next": null}`)
	}))
	defer srv.Close()
	c := newTestClient(srv)

	for _, p := range []string{"/api/dcim/devices/", "/api/virtualization/virtual-machines/", "/api/dcim/interfaces/"} {
		if _, err := c.List(p, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got["/api/dcim/devices/"] != "config_context" || got["/api/virtualization/virtual-machines/"] != "config_context" {
		t.Errorf("device and VM lists must exclude config_context, got %v", got)
	}
	if got["/api/dcim/interfaces/"] != "" {
		t.Errorf("other lists must not send exclude, got %q", got["/api/dcim/interfaces/"])
	}

	if _, err := c.List("/api/dcim/devices/", map[string]interface{}{"exclude": "custom_fields"}); err != nil {
		t.Fatal(err)
	}
	if got["/api/dcim/devices/"] != "custom_fields" {
		t.Errorf("an explicit exclude must win, got %q", got["/api/dcim/devices/"])
	}
}

func TestPageSizeFromEnv(t *testing.T) {
	for _, tc := range []struct {
		value   string
		want    int
		wantErr bool
	}{
		{"", defaultPageSize, false},
		{" 200 ", 200, false},
		{"0", 0, true},
		{"-5", 0, true},
		{"lots", 0, true},
	} {
		t.Setenv(pageSizeEnv, tc.value)
		got, err := pageSizeFromEnv()
		if (err != nil) != tc.wantErr || (!tc.wantErr && got != tc.want) {
			t.Errorf("%s=%q: got %d, %v; want %d, error %v", pageSizeEnv, tc.value, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestListUsesConfiguredPageSize(t *testing.T) {
	var limit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit = r.URL.Query().Get("limit")
		fmt.Fprint(w, `{"results": [], "next": null}`)
	}))
	defer srv.Close()
	c := newTestClient(srv)
	c.pageSize = 200
	if _, err := c.List("/api/dcim/sites/", nil); err != nil {
		t.Fatal(err)
	}
	if limit != "200" {
		t.Errorf("limit = %q, want 200", limit)
	}
}

// The transport keeps Go's default timeouts and keep-alive, applies the TLS
// choice, and still ignores proxy environment variables, as the client always
// has.
func TestNewTransport(t *testing.T) {
	tr := newTransport(true)
	if tr.Proxy != nil {
		t.Error("the transport must not pick up HTTP(S)_PROXY")
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("IGNORE_SSL_ERRORS must still turn verification off")
	}
	if newTransport(false).TLSClientConfig.InsecureSkipVerify {
		t.Error("verification must be on by default")
	}
	if tr.TLSHandshakeTimeout == 0 || tr.IdleConnTimeout == 0 {
		t.Error("expected the default transport's timeouts")
	}
	if tr.MaxIdleConnsPerHost < cacheLoadConcurrency {
		t.Errorf("MaxIdleConnsPerHost = %d, below the cache load concurrency", tr.MaxIdleConnsPerHost)
	}
}

func TestRequestCountIncludesRetries(t *testing.T) {
	retryBaseDelay = time.Millisecond
	defer func() { retryBaseDelay = time.Second }()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"results": [], "next": null}`)
	}))
	defer srv.Close()
	c := newTestClient(srv)
	if _, err := c.List("/api/dcim/sites/", nil); err != nil {
		t.Fatal(err)
	}
	if c.RequestCount() != 2 {
		t.Errorf("RequestCount() = %d, want 2", c.RequestCount())
	}
}

// The global caches load side by side but fill exactly as a sequential load
// did, and a failure is reported deterministically.
func TestLoadGlobalConcurrently(t *testing.T) {
	var inFlight, peak int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		endpoint := strings.Split(strings.Trim(r.URL.Path, "/"), "/")[2]
		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []Object{{"id": float64(1), "slug": endpoint + "-a", "name": endpoint + " A"}},
			"next":    nil,
		})
	}))
	defer srv.Close()
	c := newTestClient(srv)
	cm := NewCacheManager(c)

	if err := cm.LoadGlobal(); err != nil {
		t.Fatal(err)
	}
	if peak < 2 || peak > cacheLoadConcurrency {
		t.Errorf("peak concurrency = %d, want between 2 and %d", peak, cacheLoadConcurrency)
	}
	for resource, endpoint := range map[string]string{"sites": "sites", "tags": "tags", "clusters": "clusters", "roles": "device-roles"} {
		if id, ok := cm.GetGlobalID(resource, endpoint+"-a"); !ok || id != 1 {
			t.Errorf("%s not cached by slug", resource)
		}
		if _, ok := cm.GetGlobalID(resource, endpoint+" A"); !ok {
			t.Errorf("%s not cached by name", resource)
		}
	}
}

func TestLoadGlobalReportsFirstFailureInOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/tags/") || strings.Contains(r.URL.Path, "/clusters/") {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"detail":"denied"}`)
			return
		}
		fmt.Fprint(w, `{"results": [], "next": null}`)
	}))
	defer srv.Close()
	cm := NewCacheManager(newTestClient(srv))
	for i := 0; i < 5; i++ {
		err := cm.LoadGlobal()
		if err == nil || !strings.Contains(err.Error(), "failed to load clusters") {
			t.Fatalf("run %d: error = %v, want the clusters failure (first in order)", i, err)
		}
	}
}

// The label of an object found by neither name nor slug must not depend on
// map iteration order, or two plans of the same state differ.
func TestFormatLookupIsDeterministic(t *testing.T) {
	c := &NetBoxClient{}
	for i := 0; i < 50; i++ {
		got := c.formatLookup(map[string]interface{}{"vrf_id": 43, "prefix": "10.10.40.0/24", "site_id": 2})
		if got != "prefix=10.10.40.0/24" {
			t.Fatalf("formatLookup = %q, want prefix=10.10.40.0/24", got)
		}
	}
	if got := c.formatLookup(nil); got != "{}" {
		t.Errorf("formatLookup(nil) = %q", got)
	}
}
