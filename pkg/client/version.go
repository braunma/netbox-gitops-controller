package client

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// PortMappingsVersion is the release where a front port's singular
// rear_port/rear_port_position fields were replaced by a rear_ports list of
// {position, rear_port, rear_port_position} mappings. NetBox accepts the old
// fields silently on newer releases instead of rejecting them, so the shape
// must be chosen by version rather than discovered from an error.
var PortMappingsVersion = NetBoxVersion{Major: 4, Minor: 6}

// MinSupportedNetBoxVersion is the oldest NetBox release this controller is
// known to work against. The reconcilers write `role` on devices rather than
// the pre-3.6 `device_role`, so anything older fails with confusing field
// errors rather than a clear one.
var MinSupportedNetBoxVersion = NetBoxVersion{Major: 3, Minor: 6}

// NetBoxVersion is a parsed NetBox release number. Only major and minor are
// compared; patch releases never change the API surface this project uses.
type NetBoxVersion struct {
	Major int
	Minor int
	Raw   string
}

// String returns the version as reported by the server.
func (v NetBoxVersion) String() string {
	if v.Raw != "" {
		return v.Raw
	}
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// AtLeast reports whether v is the same as, or newer than, other.
func (v NetBoxVersion) AtLeast(other NetBoxVersion) bool {
	if v.Major != other.Major {
		return v.Major > other.Major
	}
	return v.Minor >= other.Minor
}

// ParseNetBoxVersion reads a NetBox version string such as "4.1.2" or
// "4.2.0-beta1". Anything after the minor component is ignored.
func ParseNetBoxVersion(raw string) (NetBoxVersion, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return NetBoxVersion{}, fmt.Errorf("empty version string")
	}

	// Drop any pre-release or build suffix ("4.2.0-beta1" -> "4.2.0").
	numeric := trimmed
	if i := strings.IndexAny(numeric, "-+ "); i >= 0 {
		numeric = numeric[:i]
	}

	parts := strings.Split(numeric, ".")
	if len(parts) < 2 {
		return NetBoxVersion{}, fmt.Errorf("malformed version %q: expected at least major.minor", raw)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return NetBoxVersion{}, fmt.Errorf("malformed version %q: major component: %w", raw, err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return NetBoxVersion{}, fmt.Errorf("malformed version %q: minor component: %w", raw, err)
	}

	return NetBoxVersion{Major: major, Minor: minor, Raw: trimmed}, nil
}

// Version queries /api/status/ and returns the NetBox release the server
// reports.
func (c *NetBoxClient) Version() (NetBoxVersion, error) {
	statusURL := fmt.Sprintf("%s/api/status/", strings.TrimRight(c.baseURL, "/"))

	statusCode, body, err := c.doWithRetry("GET", statusURL, nil)
	if err != nil {
		return NetBoxVersion{}, fmt.Errorf("failed to query NetBox status: %w", err)
	}
	if statusCode != 200 {
		return NetBoxVersion{}, fmt.Errorf("NetBox status endpoint returned %d: %s", statusCode, string(body))
	}

	var status struct {
		NetBoxVersion string `json:"netbox-version"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return NetBoxVersion{}, fmt.Errorf("failed to decode NetBox status response: %w", err)
	}
	if status.NetBoxVersion == "" {
		return NetBoxVersion{}, fmt.Errorf("NetBox status response contained no version")
	}

	return ParseNetBoxVersion(status.NetBoxVersion)
}

// CheckVersion queries the server's NetBox release and rejects one older than
// MinSupportedNetBoxVersion. Running against an unsupported release otherwise
// surfaces as a scatter of 400s on renamed fields rather than as one clear
// message.
//
// A server that cannot be asked (an old release with no status endpoint, a
// proxy that rewrites it) is reported as a warning and allowed through: the
// check exists to explain failures, not to become a new way to fail.
func (c *NetBoxClient) CheckVersion() error {
	version, err := c.Version()
	if err != nil {
		c.logger.Warning("Could not determine the NetBox version (%v); continuing without the compatibility check", err)
		return nil
	}

	if !version.AtLeast(MinSupportedNetBoxVersion) {
		return fmt.Errorf("NetBox %s is not supported: this controller requires %s or newer",
			version, MinSupportedNetBoxVersion)
	}

	c.version = version
	c.logger.Info("Connected to NetBox %s", version)
	return nil
}

// UsesPortMappings reports whether the connected NetBox expects front port
// terminations as a rear_ports list. Unknown versions keep the pre-4.6 shape,
// which is what every release before the check existed understood.
func (c *NetBoxClient) UsesPortMappings() bool {
	if c.version.Major == 0 {
		return false
	}
	return c.version.AtLeast(PortMappingsVersion)
}

// SetVersionForTesting pins the detected version without contacting a server.
func (c *NetBoxClient) SetVersionForTesting(v NetBoxVersion) { c.version = v }
