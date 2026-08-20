// SPDX-License-Identifier: Apache-2.0

package client

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

// transportOf returns the TLS config the client was built with.
func transportOf(t *testing.T, c *NetBoxClient) *tls.Config {
	t.Helper()
	transport, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", c.httpClient.Transport)
	}
	return transport.TLSClientConfig
}

func TestCertificatesAreVerifiedByDefault(t *testing.T) {
	c, err := NewClient("https://netbox.example.com", "token", true)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if transportOf(t, c).InsecureSkipVerify {
		t.Error("TLS verification must be on unless IGNORE_SSL_ERRORS says otherwise")
	}
}

func TestIgnoreSSLErrorsOptsOutOfVerification(t *testing.T) {
	for _, value := range []string{"1", "true", "True", "TRUE", "yes", "on"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("IGNORE_SSL_ERRORS", value)
			c, err := NewClient("https://netbox.example.com", "token", true)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			if !transportOf(t, c).InsecureSkipVerify {
				t.Errorf("IGNORE_SSL_ERRORS=%q should disable verification", value)
			}
		})
	}
}

func TestFalseAndNonsenseKeepVerificationOn(t *testing.T) {
	for _, value := range []string{"", "0", "false", "False", "no", "off", "maybe"} {
		t.Run("value="+value, func(t *testing.T) {
			t.Setenv("IGNORE_SSL_ERRORS", value)
			c, err := NewClient("https://netbox.example.com", "token", true)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			if transportOf(t, c).InsecureSkipVerify {
				t.Errorf("IGNORE_SSL_ERRORS=%q must not disable verification", value)
			}
		})
	}
}

// The end-to-end proof: a server with a certificate no CA vouches for is
// refused by default and reachable once the opt-out is set.
func TestSelfSignedServerIsRefusedUnlessOptedOut(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"netbox-version": "4.6.7"}`))
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL, "token", true)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.httpClient.Get(srv.URL); err == nil {
		t.Error("a self-signed certificate must be refused by default")
	}

	t.Setenv("IGNORE_SSL_ERRORS", "true")
	insecure, err := NewClient(srv.URL, "token", true)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resp, err := insecure.httpClient.Get(srv.URL)
	if err != nil {
		t.Fatalf("with IGNORE_SSL_ERRORS set the request should succeed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want 200", resp.StatusCode)
	}
}
