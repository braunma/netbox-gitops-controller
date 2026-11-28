// SPDX-License-Identifier: Apache-2.0

package export

import (
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
)

// fakeLister serves fixed objects per endpoint.
type fakeLister map[string][]client.Object

func (f fakeLister) List(path string, _ map[string]interface{}) ([]client.Object, error) {
	return f[strings.TrimSuffix(strings.TrimPrefix(path, "/api/"), "/")], nil
}

func TestNaturalKeyOrdersPortNumbers(t *testing.T) {
	names := []string{"Eth1/10", "Eth1/2", "Eth1/1", "eth0"}
	sortBy(names, naturalKey)
	if got := strings.Join(names, ","); got != "Eth1/1,Eth1/2,Eth1/10,eth0" {
		t.Errorf("order = %s", got)
	}
}

// NetBox 4.6 moved a front port's rear port into a list of mappings.
func TestFrontPortRearReadsBothShapes(t *testing.T) {
	rearName := map[int]string{7: "R1"}
	old := map[string]interface{}{"rear_port": map[string]interface{}{"id": 7.0, "name": "R1"}, "rear_port_position": 3.0}
	if name, pos := frontPortRear(old, rearName); name != "R1" || pos != 3 {
		t.Errorf("pre-4.6: %q %d", name, pos)
	}
	v46 := map[string]interface{}{"rear_ports": []interface{}{
		map[string]interface{}{"position": 1.0, "rear_port": 7.0, "rear_port_position": 1.0},
	}}
	if name, pos := frontPortRear(v46, rearName); name != "R1" || pos != 0 {
		t.Errorf("4.6: %q %d (position 1 is the default and is left out)", name, pos)
	}
}

func site(id int, slug string) client.Object {
	return client.Object{"id": float64(id), "name": slug, "slug": slug}
}

func TestSiteFilterAndDeclaredObjectsAreSkipped(t *testing.T) {
	a, b := site(1, "a"), site(2, "b")
	role := client.Object{"id": 3.0, "slug": "server", "name": "Server", "color": "ffffff"}
	dt := client.Object{"id": 4.0, "slug": "box", "model": "Box", "manufacturer": map[string]interface{}{"name": "Acme"}}
	dev := func(id int, name string, s client.Object) client.Object {
		return client.Object{"id": float64(id), "name": name, "site": s, "role": role, "device_type": dt,
			"status": map[string]interface{}{"value": "active"}}
	}
	res, err := Export(fakeLister{
		"dcim/sites":        {a, b},
		"dcim/device-roles": {role},
		"dcim/device-types": {dt},
		"dcim/devices":      {dev(10, "in-a", a), dev(11, "in-b", b)},
	}, Options{Sites: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range res.Files {
		paths = append(paths, f.Path)
		if strings.Contains(string(f.Data), "in-b") {
			t.Errorf("%s holds a device of a site outside the filter", f.Path)
		}
	}
	if res.Exported[kindDevice] != 1 || res.Exported[kindSite] != 1 {
		t.Errorf("exported %v, want 1 device and 1 site; files %v", res.Exported, paths)
	}
}

// Two patch panels cabled front to front: the controller only wires panel to
// panel rear to rear, so the cable cannot be declared and must be reported.
func TestUndeclarableCableIsReported(t *testing.T) {
	s := site(1, "a")
	pp := client.Object{"id": 3.0, "slug": "patch-panel", "name": "Patch Panel"}
	dt := client.Object{"id": 4.0, "slug": "pp", "model": "PP", "manufacturer": map[string]interface{}{"name": "Acme"}}
	dev := func(id int, name string) client.Object {
		return client.Object{"id": float64(id), "name": name, "site": s, "role": pp, "device_type": dt}
	}
	d1, d2 := dev(10, "pp-1"), dev(11, "pp-2")
	port := func(id int, d client.Object) client.Object {
		return client.Object{"id": float64(id), "name": "1", "device": map[string]interface{}{"id": d["id"], "name": d["name"]},
			"type": map[string]interface{}{"value": "lc"}}
	}
	p1, p2 := port(20, d1), port(21, d2)
	term := func(p client.Object) []interface{} {
		return []interface{}{map[string]interface{}{"object_type": "dcim.frontport", "object_id": p["id"], "object": p}}
	}
	res, err := Export(fakeLister{
		"dcim/sites": {s}, "dcim/device-roles": {pp}, "dcim/device-types": {dt},
		"dcim/devices": {d1, d2}, "dcim/front-ports": {p1, p2},
		"dcim/cables": {{"id": 30.0, "a_terminations": term(p1), "b_terminations": term(p2)}},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "pp-1:1 - pp-2:1") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning for the undeclarable cable: %v", res.Warnings)
	}
	for _, f := range res.Files {
		if strings.Contains(string(f.Data), "peer_device") {
			t.Errorf("%s declares the cable anyway:\n%s", f.Path, f.Data)
		}
	}
}
