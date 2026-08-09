package models

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// NetBox choice sets and field lengths, read from a NetBox 4.6 instance's
// OPTIONS responses. Validating against them turns a 400 that would otherwise
// land partway through a sync — after some objects have already been written —
// into a pre-flight error that `yamlcheck` and `--dry-run` both catch.
//
// A future NetBox release may add a choice. If one is rejected here that the
// server would accept, add it to the relevant set; the endpoint's OPTIONS
// response is the authority.
var (
	SiteStatuses       = []string{"planned", "staging", "active", "decommissioning", "retired"}
	RackStatuses       = []string{"reserved", "available", "planned", "active", "deprecated"}
	DeviceStatuses     = []string{"offline", "active", "planned", "staged", "failed", "inventory", "decommissioning"}
	VLANStatuses       = []string{"active", "reserved", "deprecated"}
	PrefixStatuses     = []string{"container", "active", "reserved", "deprecated"}
	VMStatuses         = []string{"offline", "active", "planned", "staged", "failed", "decommissioning"}
	DeviceFaces        = []string{"front", "rear"}
	SubdeviceRoles     = []string{"parent", "child"}
	WeightUnits        = []string{"kg", "g", "lb", "oz"}
	DeviceTypeAirflows = []string{
		"front-to-rear", "rear-to-front", "left-to-right", "right-to-left",
		"side-to-rear", "rear-to-side", "bottom-to-top", "top-to-bottom",
		"passive", "mixed",
	}
)

// NetBox field length limits, likewise from OPTIONS. Exceeding one is rejected
// by the API, so it is worth catching before anything is written.
const (
	MaxDeviceNameLength = 64
	MaxVLANNameLength   = 64
	MaxNameLength       = 100 // sites, racks, device type models, slugs
	MaxSerialLength     = 50
	MaxAssetTagLength   = 50
	MaxPartNumberLength = 50
)

// validateChoice reports an error when value is set but is not one of allowed.
// An empty value is accepted: these fields are optional, and the reconcilers
// apply NetBox's default for the ones that need one.
func validateChoice(field, value string, allowed []string) error {
	if value == "" {
		return nil
	}
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return fmt.Errorf("%s %q is not valid (expected one of: %s)", field, value, strings.Join(allowed, ", "))
}

// validateLength reports an error when value is longer than NetBox allows.
// Length is counted in runes, matching how NetBox counts characters, so a
// name of accented or non-Latin characters is not rejected early.
func validateLength(field, value string, max int) error {
	if n := utf8.RuneCountInString(value); n > max {
		return fmt.Errorf("%s is %d characters, exceeding NetBox's limit of %d", field, n, max)
	}
	return nil
}

// appendNonNil adds each non-nil error to errs.
func appendNonNil(errs []error, candidates ...error) []error {
	for _, err := range candidates {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
