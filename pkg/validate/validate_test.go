// SPDX-License-Identifier: Apache-2.0

package validate

import (
	"fmt"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/lint"
	"github.com/braunma/netbox-gitops-controller/pkg/loader"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

// fakeSchemas answers for the endpoints a test cares about and reports the
// rest as unreadable, the way a restricted token would.
type fakeSchemas struct {
	schemas map[string]client.EndpointSchema
	calls   map[string]int
}

func (f *fakeSchemas) Schema(app, endpoint string) (client.EndpointSchema, error) {
	key := app + "/" + endpoint
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[key]++
	schema, ok := f.schemas[key]
	if !ok {
		return client.EndpointSchema{}, fmt.Errorf("no schema for %s", key)
	}
	return schema, nil
}

// fakeRefs answers existence queries from a fixed set and records what it was
// asked, so a test can assert that lookups are deduplicated.
type fakeRefs struct {
	existing map[string]bool // "app/endpoint field=value"
	asked    []string
	err      error
}

func (f *fakeRefs) Filter(app, endpoint string, filters map[string]interface{}) ([]client.Object, error) {
	var key string
	for field, value := range filters {
		key = fmt.Sprintf("%s/%s %s=%v", app, endpoint, field, value)
	}
	f.asked = append(f.asked, key)
	if f.err != nil {
		return nil, f.err
	}
	if f.existing[key] {
		return []client.Object{{"id": 1}}, nil
	}
	return nil, nil
}

func interfaceSchema() client.EndpointSchema {
	return client.EndpointSchema{
		Choices: map[string][]string{
			"type": {"virtual", "lag", "1000base-t", "25gbase-x-sfp28", "100gbase-x-qsfp28"},
			"mode": {"access", "tagged", "tagged-all"},
		},
		MaxLength: map[string]int{"name": 64, "label": 64},
	}
}

func deviceSchema() client.EndpointSchema {
	return client.EndpointSchema{
		Choices: map[string][]string{
			"status": {"active", "offline", "planned"},
			"face":   {"front", "rear"},
		},
		MaxLength: map[string]int{"name": 64, "serial": 50, "asset_tag": 50},
	}
}

func allSchemas() *fakeSchemas {
	return &fakeSchemas{schemas: map[string]client.EndpointSchema{
		"dcim/interfaces":                 interfaceSchema(),
		"dcim/interface-templates":        interfaceSchema(),
		"dcim/devices":                    deviceSchema(),
		"dcim/device-types":               {Choices: map[string][]string{"subdevice_role": {"parent", "child"}}},
		"dcim/sites":                      {Choices: map[string][]string{"status": {"active", "planned"}}},
		"dcim/racks":                      {Choices: map[string][]string{"status": {"active", "reserved"}}},
		"dcim/cables":                     {Choices: map[string][]string{"type": {"cat6", "dac-active", "smf-os2"}, "length_unit": {"m", "cm"}}},
		"ipam/ip-addresses":               {Choices: map[string][]string{"status": {"active", "reserved"}}},
		"ipam/vlans":                      {Choices: map[string][]string{"status": {"active"}}},
		"ipam/prefixes":                   {Choices: map[string][]string{"status": {"active", "container"}}},
		"dcim/modules":                    {Choices: map[string][]string{"status": {"active", "offline"}}},
		"virtualization/virtual-machines": {Choices: map[string][]string{"status": {"active", "offline"}}},
		"virtualization/interfaces":       {Choices: map[string][]string{"mode": {"access", "tagged"}}},
	}}
}

func findingsFor(findings []lint.Finding, check string) []lint.Finding {
	var out []lint.Finding
	for _, f := range findings {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

func render(findings []lint.Finding) string {
	var b strings.Builder
	for _, f := range findings {
		b.WriteString("  " + f.String() + "\n")
	}
	if b.Len() == 0 {
		return "  (none)\n"
	}
	return b.String()
}

func assertCheck(t *testing.T, findings []lint.Finding, check string, want int) []lint.Finding {
	t.Helper()
	got := findingsFor(findings, check)
	if len(got) != want {
		t.Errorf("check %q: got %d findings, want %d\nall findings:\n%s", check, len(got), want, render(findings))
	}
	return got
}

func device(name string) *models.DeviceConfig {
	return &models.DeviceConfig{
		Name: name, SiteSlug: "berlin", RoleSlug: "server",
		DeviceTypeSlug: "r740", Status: "active", Face: "front",
	}
}

// declaredDataset holds every reference it makes, so nothing needs looking up.
func declaredDataset() loader.Dataset {
	return loader.Dataset{
		Sites:       []*models.Site{{Name: "Berlin", Slug: "berlin", Status: "active"}},
		Roles:       []*models.Role{{Name: "Server", Slug: "server", Color: "4caf50"}},
		DeviceTypes: []*models.DeviceType{{Model: "R740", Slug: "r740", Manufacturer: "Dell"}},
	}
}

func TestValidDatasetProducesNoFindings(t *testing.T) {
	ds := declaredDataset()
	srv := device("srv-01")
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", Type: "25gbase-x-sfp28", Mode: "access",
			IP:   &models.IPConfig{Address: "10.0.0.1/24", Status: "active"},
			Link: &models.LinkConfig{PeerDevice: "srv-02", PeerPort: "eth0", CableType: "dac-active"}},
	}
	ds.Devices = []*models.DeviceConfig{srv, device("srv-02")}

	findings, err := Check(ds, allSchemas(), &fakeRefs{}, Options{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings, got:\n%s", render(findings))
	}
}

func TestInvalidChoiceIsReportedWithASuggestion(t *testing.T) {
	ds := declaredDataset()
	srv := device("srv-01")
	srv.Interfaces = []models.InterfaceConfig{{Name: "eth0", Type: "25gbase-x-sfp29"}}
	ds.Devices = []*models.DeviceConfig{srv}

	findings, err := Check(ds, allSchemas(), nil, Options{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	got := assertCheck(t, findings, "invalid-choice", 1)
	if len(got) == 1 {
		if got[0].Severity != lint.Error {
			t.Errorf("got severity %v, want error", got[0].Severity)
		}
		if !strings.Contains(got[0].Message, `did you mean "25gbase-x-sfp28"`) {
			t.Errorf("expected the near miss to be suggested, got %q", got[0].Message)
		}
	}
}

// A value that resembles nothing gets the size of the choice set and a sample,
// because suggesting the "nearest" of 216 would be a guess.
func TestInvalidChoiceWithoutANearMissListsTheAlternatives(t *testing.T) {
	ds := declaredDataset()
	srv := device("srv-01")
	srv.Interfaces = []models.InterfaceConfig{{Name: "eth0", Type: "fibre-channel-over-carrier-pigeon"}}
	ds.Devices = []*models.DeviceConfig{srv}

	findings, _ := Check(ds, allSchemas(), nil, Options{})
	got := assertCheck(t, findings, "invalid-choice", 1)
	if len(got) == 1 {
		if strings.Contains(got[0].Message, "did you mean") {
			t.Errorf("a distant value must not be guessed at, got %q", got[0].Message)
		}
		if !strings.Contains(got[0].Message, "it offers 5") {
			t.Errorf("expected the number of choices, got %q", got[0].Message)
		}
	}
}

func TestChoicesAreCheckedAcrossObjectKinds(t *testing.T) {
	ds := declaredDataset()
	ds.Sites[0].Status = "activ"
	ds.Racks = []*models.Rack{{Name: "A01", Slug: "a01", SiteSlug: "berlin", Status: "reservd"}}
	ds.VLANs = []*models.VLAN{{Name: "V", VID: 10, SiteSlug: "berlin", Status: "actve"}}
	ds.Prefixes = []*models.Prefix{{Prefix: "10.0.0.0/24", Status: "containr"}}
	ds.DeviceTypes[0].SubdeviceRole = "parnt"

	srv := device("srv-01")
	srv.Face = "fromt"
	srv.Modules = []models.ModuleConfig{{Name: "OCP-1", ModuleTypeSlug: "x", Status: "aktive"}}
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", Type: "1000base-t", Mode: "acess",
			IP:   &models.IPConfig{Address: "10.0.0.1/24", Status: "actve"},
			Link: &models.LinkConfig{PeerDevice: "srv-01", PeerPort: "eth1", CableType: "cat6x", LengthUnit: "metres"}},
	}
	ds.Devices = []*models.DeviceConfig{srv}
	ds.VMs = []*models.VMConfig{{Name: "vm-01", Status: "offlin",
		Interfaces: []models.VMInterfaceConfig{{Name: "eth0", Mode: "tagged-all"}}}}

	findings, err := Check(ds, allSchemas(), nil, Options{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// site, rack, vlan, prefix, subdevice_role, face, module status,
	// interface mode, ip status, cable type, cable length_unit, vm status,
	// vm interface mode
	assertCheck(t, findings, "invalid-choice", 13)
}

// NetBox calls both an interface's type and a cable's type "type". A rejected
// cable type must not read as a rejected interface type.
func TestCableFindingsNameTheCable(t *testing.T) {
	ds := declaredDataset()
	srv := device("srv-01")
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", Type: "1000base-t",
			Link: &models.LinkConfig{PeerDevice: "srv-01", PeerPort: "eth1", CableType: "dac-activ"}},
	}
	ds.Devices = []*models.DeviceConfig{srv}

	findings, _ := Check(ds, allSchemas(), nil, Options{})
	got := assertCheck(t, findings, "invalid-choice", 1)
	if len(got) == 1 && !strings.HasPrefix(got[0].Object, "cable declared on") {
		t.Errorf("got object %q, want it to name the cable", got[0].Object)
	}
}

func TestValueTooLong(t *testing.T) {
	ds := declaredDataset()
	srv := device("srv-01")
	srv.Serial = strings.Repeat("x", 51)
	ds.Devices = []*models.DeviceConfig{srv}

	findings, _ := Check(ds, allSchemas(), nil, Options{})
	got := assertCheck(t, findings, "value-too-long", 1)
	if len(got) == 1 && !strings.Contains(got[0].Message, "at most 50") {
		t.Errorf("expected the limit in the message, got %q", got[0].Message)
	}
}

// Length is counted in runes: NetBox counts characters, so a name of accented
// or non-Latin characters must not be rejected early.
func TestValueLengthCountsRunesNotBytes(t *testing.T) {
	ds := declaredDataset()
	srv := device("srv-01")
	srv.Serial = strings.Repeat("ü", 50)
	ds.Devices = []*models.DeviceConfig{srv}

	findings, _ := Check(ds, allSchemas(), nil, Options{})
	assertCheck(t, findings, "value-too-long", 0)
}

func TestUnreadableEndpointIsAWarning(t *testing.T) {
	ds := declaredDataset()
	srv := device("srv-01")
	srv.Interfaces = []models.InterfaceConfig{{Name: "eth0", Type: "25gbase-x-sfp28"}}
	ds.Devices = []*models.DeviceConfig{srv}

	// Every endpoint answers except the interfaces one.
	schemas := allSchemas()
	delete(schemas.schemas, "dcim/interfaces")

	findings, err := Check(ds, schemas, nil, Options{})
	if err != nil {
		t.Fatalf("one unreadable endpoint must not fail the run: %v", err)
	}
	got := assertCheck(t, findings, "schema-unavailable", 1)
	if len(got) == 1 && got[0].Severity != lint.Warning {
		t.Errorf("got severity %v, want warning", got[0].Severity)
	}
}

// If nothing at all can be read, the run failed rather than passed.
func TestEveryEndpointUnreadableIsAnError(t *testing.T) {
	ds := declaredDataset()
	ds.Devices = []*models.DeviceConfig{device("srv-01")}

	if _, err := Check(ds, &fakeSchemas{}, nil, Options{}); err == nil {
		t.Error("expected an error when no schema could be read")
	}
}

func TestSchemaIsRequestedOncePerEndpoint(t *testing.T) {
	ds := declaredDataset()
	var devices []*models.DeviceConfig
	for i := 0; i < 20; i++ {
		d := device(fmt.Sprintf("srv-%02d", i))
		d.Interfaces = []models.InterfaceConfig{{Name: "eth0", Type: "1000base-t"}}
		devices = append(devices, d)
	}
	ds.Devices = devices

	schemas := allSchemas()
	if _, err := Check(ds, schemas, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	for endpoint, calls := range schemas.calls {
		if calls != 1 {
			t.Errorf("%s was asked %d times, want 1", endpoint, calls)
		}
	}
}

func TestReferencesDeclaredHereAreNotLookedUp(t *testing.T) {
	ds := declaredDataset()
	ds.Devices = []*models.DeviceConfig{device("srv-01")}
	refs := &fakeRefs{}

	if _, err := Check(ds, allSchemas(), refs, Options{}); err != nil {
		t.Fatal(err)
	}
	if len(refs.asked) != 0 {
		t.Errorf("looked up references the repository declares: %v", refs.asked)
	}
}

func TestUndeclaredReferenceFoundInNetBoxIsAccepted(t *testing.T) {
	ds := declaredDataset()
	ds.Sites = nil // the site is managed outside this repository
	srv := device("srv-01")
	ds.Devices = []*models.DeviceConfig{srv}

	refs := &fakeRefs{existing: map[string]bool{"dcim/sites slug=berlin": true}}
	findings, err := Check(ds, allSchemas(), refs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	assertCheck(t, findings, "missing-reference", 0)
	if len(refs.asked) != 1 {
		t.Errorf("got lookups %v, want exactly one for the site", refs.asked)
	}
}

func TestUndeclaredReferenceMissingEverywhereIsAnError(t *testing.T) {
	ds := declaredDataset()
	ds.Sites = nil
	ds.Devices = []*models.DeviceConfig{device("srv-01")}

	findings, err := Check(ds, allSchemas(), &fakeRefs{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := assertCheck(t, findings, "missing-reference", 1)
	if len(got) == 1 && !strings.Contains(got[0].Object, "berlin") {
		t.Errorf("got %q, want the site named", got[0].Object)
	}
}

// A hundred devices at one site ask about that site once.
func TestReferenceLookupsAreDeduplicated(t *testing.T) {
	ds := declaredDataset()
	ds.Sites = nil
	var devices []*models.DeviceConfig
	for i := 0; i < 50; i++ {
		devices = append(devices, device(fmt.Sprintf("srv-%02d", i)))
	}
	ds.Devices = devices

	refs := &fakeRefs{existing: map[string]bool{"dcim/sites slug=berlin": true}}
	if _, err := Check(ds, allSchemas(), refs, Options{}); err != nil {
		t.Fatal(err)
	}
	if len(refs.asked) != 1 {
		t.Errorf("got %d lookups, want 1: %v", len(refs.asked), refs.asked)
	}
}

func TestSkipReferencesMakesNoQueries(t *testing.T) {
	ds := declaredDataset()
	ds.Sites = nil
	ds.Devices = []*models.DeviceConfig{device("srv-01")}

	refs := &fakeRefs{}
	findings, err := Check(ds, allSchemas(), refs, Options{SkipReferences: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs.asked) != 0 {
		t.Errorf("--skip-references still queried: %v", refs.asked)
	}
	assertCheck(t, findings, "missing-reference", 0)
}

func TestAFailedLookupIsAWarningNotAVerdict(t *testing.T) {
	ds := declaredDataset()
	ds.Sites = nil
	ds.Devices = []*models.DeviceConfig{device("srv-01")}

	refs := &fakeRefs{err: fmt.Errorf("connection reset")}
	findings, err := Check(ds, allSchemas(), refs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := assertCheck(t, findings, "reference-unchecked", 1)
	if len(got) == 1 && got[0].Severity != lint.Warning {
		t.Errorf("got severity %v, want warning", got[0].Severity)
	}
	assertCheck(t, findings, "missing-reference", 0)
}

func TestPeerDeviceAndParentAreLookedUp(t *testing.T) {
	ds := declaredDataset()
	srv := device("srv-01")
	srv.ParentDevice = "chassis-01"
	srv.DeviceBay = "Bay 1"
	srv.Interfaces = []models.InterfaceConfig{
		{Name: "eth0", Type: "1000base-t",
			Link: &models.LinkConfig{PeerDevice: "core-01", PeerPort: "Eth1/1"}},
	}
	ds.Devices = []*models.DeviceConfig{srv}

	refs := &fakeRefs{existing: map[string]bool{"dcim/devices name=chassis-01": true}}
	findings, err := Check(ds, allSchemas(), refs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := assertCheck(t, findings, "missing-reference", 1)
	if len(got) == 1 && !strings.Contains(got[0].Object, "core-01") {
		t.Errorf("got %q, want the unresolvable peer named", got[0].Object)
	}
}

func TestFindingsSortErrorsFirst(t *testing.T) {
	ds := declaredDataset()
	ds.Sites = nil
	srv := device("srv-01")
	srv.Interfaces = []models.InterfaceConfig{{Name: "eth0", Type: "nonsense-type"}}
	ds.Devices = []*models.DeviceConfig{srv}

	schemas := &fakeSchemas{schemas: map[string]client.EndpointSchema{
		"dcim/devices":    deviceSchema(),
		"dcim/interfaces": interfaceSchema(),
	}}
	refs := &fakeRefs{existing: map[string]bool{"dcim/sites slug=berlin": true}}

	findings, err := Check(ds, schemas, refs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) < 2 {
		t.Fatalf("expected an error and a warning, got:\n%s", render(findings))
	}
	if findings[0].Severity != lint.Error {
		t.Errorf("errors must sort first, got:\n%s", render(findings))
	}
}

func TestNearest(t *testing.T) {
	choices := []string{"1000base-t", "25gbase-x-sfp28", "100gbase-x-qsfp28", "virtual"}
	cases := map[string]string{
		"25gbase-x-sfp29":   "25gbase-x-sfp28", // one character
		"1000base-tx":       "1000base-t",      // one extra
		"virtal":            "virtual",         // one missing
		"totally-different": "",                // too far to guess
		"":                  "",
	}
	for value, want := range cases {
		if got := nearest(value, choices); got != want {
			t.Errorf("nearest(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestEditDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "ab", 1},
		{"ab", "abc", 1},
		{"kitten", "sitting", 3},
		{"", "abc", 3},
	}
	for _, tc := range cases {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestPreviewTruncatesLongChoiceSets(t *testing.T) {
	many := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		many = append(many, fmt.Sprintf("choice-%02d", i))
	}
	got := preview(many)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a long set must be elided, got %q", got)
	}
	if strings.Count(got, ",") != 5 {
		t.Errorf("expected five listed choices, got %q", got)
	}

	short := preview([]string{"b", "a"})
	if short != "a, b" {
		t.Errorf("a short set is listed in full and sorted, got %q", short)
	}
}
