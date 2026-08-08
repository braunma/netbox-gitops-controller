package client

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseNetBoxVersion(t *testing.T) {
	tests := []struct {
		raw           string
		major, minor  int
		wantParseFail bool
	}{
		{raw: "4.1.2", major: 4, minor: 1},
		{raw: "3.6.0", major: 3, minor: 6},
		{raw: "4.2", major: 4, minor: 2},
		{raw: "4.2.0-beta1", major: 4, minor: 2},
		{raw: "  4.3.1  ", major: 4, minor: 3},
		{raw: "10.0.0", major: 10, minor: 0},
		{raw: "", wantParseFail: true},
		{raw: "4", wantParseFail: true},
		{raw: "four.one", wantParseFail: true},
		{raw: "4.x", wantParseFail: true},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := ParseNetBoxVersion(tt.raw)
			if tt.wantParseFail {
				if err == nil {
					t.Fatalf("ParseNetBoxVersion(%q) = %v, want an error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseNetBoxVersion(%q) error = %v", tt.raw, err)
			}
			if got.Major != tt.major || got.Minor != tt.minor {
				t.Errorf("ParseNetBoxVersion(%q) = %d.%d, want %d.%d", tt.raw, got.Major, got.Minor, tt.major, tt.minor)
			}
		})
	}
}

func TestNetBoxVersionAtLeast(t *testing.T) {
	tests := []struct {
		have, want string
		atLeast    bool
	}{
		{have: "3.6.0", want: "3.6", atLeast: true},
		{have: "3.7.0", want: "3.6", atLeast: true},
		{have: "4.0.0", want: "3.6", atLeast: true},
		{have: "3.5.9", want: "3.6", atLeast: false},
		{have: "2.11.0", want: "3.6", atLeast: false},
		// A major bump must dominate the minor comparison.
		{have: "4.0.0", want: "3.11", atLeast: true},
		{have: "3.11.0", want: "4.0", atLeast: false},
	}

	for _, tt := range tests {
		t.Run(tt.have+"_vs_"+tt.want, func(t *testing.T) {
			have, err := ParseNetBoxVersion(tt.have)
			if err != nil {
				t.Fatalf("ParseNetBoxVersion(%q) error = %v", tt.have, err)
			}
			want, err := ParseNetBoxVersion(tt.want)
			if err != nil {
				t.Fatalf("ParseNetBoxVersion(%q) error = %v", tt.want, err)
			}
			if got := have.AtLeast(want); got != tt.atLeast {
				t.Errorf("%s.AtLeast(%s) = %v, want %v", tt.have, tt.want, got, tt.atLeast)
			}
		})
	}
}

// versionServer serves /api/status/ with the given body and status code.
func versionServer(t *testing.T, statusCode int, body string) *NetBoxClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(srv.URL, "token", true)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return c
}

func TestVersionReadsStatusEndpoint(t *testing.T) {
	c := versionServer(t, 200, `{"netbox-version": "4.1.2", "django-version": "5.0"}`)

	got, err := c.Version()
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if got.Major != 4 || got.Minor != 1 {
		t.Errorf("Version() = %s, want 4.1.x", got)
	}
	if got.String() != "4.1.2" {
		t.Errorf("String() = %q, want the raw server value", got.String())
	}
}

func TestCheckVersionAcceptsSupportedRelease(t *testing.T) {
	c := versionServer(t, 200, `{"netbox-version": "4.1.2"}`)

	if err := c.CheckVersion(); err != nil {
		t.Errorf("CheckVersion() error = %v, want a supported release accepted", err)
	}
}

func TestCheckVersionRejectsTooOldRelease(t *testing.T) {
	c := versionServer(t, 200, `{"netbox-version": "3.5.9"}`)

	err := c.CheckVersion()
	if err == nil {
		t.Fatal("CheckVersion() = nil, want an unsupported release rejected")
	}
	// The message has to name both versions, or it is no more useful than the
	// scattered 400s it exists to replace.
	if !strings.Contains(err.Error(), "3.5.9") || !strings.Contains(err.Error(), MinSupportedNetBoxVersion.String()) {
		t.Errorf("error = %q, want it to name the detected and required versions", err)
	}
}

func TestCheckVersionToleratesUnavailableStatus(t *testing.T) {
	// A server that cannot answer must not block the run: the check exists to
	// explain failures, not to become a new way to fail.
	for _, tc := range []struct {
		name string
		code int
		body string
	}{
		{name: "not found", code: 404, body: `{"detail": "Not found."}`},
		{name: "unparseable body", code: 200, body: `not json`},
		{name: "no version field", code: 200, body: `{"django-version": "5.0"}`},
		{name: "malformed version", code: 200, body: `{"netbox-version": "unknown"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := versionServer(t, tc.code, tc.body)
			if err := c.CheckVersion(); err != nil {
				t.Errorf("CheckVersion() error = %v, want an undeterminable version to be tolerated", err)
			}
		})
	}
}
