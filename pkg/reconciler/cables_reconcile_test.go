package reconciler

import (
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
)

func ifaceEndpoint(device, port string, id int) *CableEndpoint {
	return &CableEndpoint{
		DeviceName: device,
		PortName:   port,
		ObjectType: "dcim.interface",
		ObjectID:   id,
	}
}

func TestReconcileCableCreatesNewCable(t *testing.T) {
	f, c := newFakeNetBox(t)
	f.seed("dcim", "interfaces", client.Object{"id": 10, "name": "eth0", "device": "sw-01"})
	f.seed("dcim", "interfaces", client.Object{"id": 20, "name": "eth0", "device": "sw-02"})

	cr := NewCableReconciler(c)
	aEnd := ifaceEndpoint("sw-01", "eth0", 10)
	bEnd := ifaceEndpoint("sw-02", "eth0", 20)
	link := &models.LinkConfig{CableType: "cat6", Color: "purple", Length: 3, LengthUnit: "m"}

	if err := cr.ReconcileCable(aEnd, bEnd, link); err != nil {
		t.Fatalf("ReconcileCable() error = %v", err)
	}

	muts := f.requireMutationCount(t, 1)
	if muts[0].method != "POST" || muts[0].path != "/api/dcim/cables/" {
		t.Fatalf("expected POST /api/dcim/cables/, got %s %s", muts[0].method, muts[0].path)
	}
	body := muts[0].body
	if body["type"] != "cat6" {
		t.Errorf("cable type = %v, expected \"cat6\"", body["type"])
	}
	if body["color"] != "800080" {
		t.Errorf("cable color = %v, expected color name \"purple\" normalized to \"800080\"", body["color"])
	}
	if body["status"] != "connected" {
		t.Errorf("cable status = %v, expected \"connected\"", body["status"])
	}
	aTerms, ok := body["a_terminations"].([]interface{})
	if !ok || len(aTerms) != 1 {
		t.Fatalf("a_terminations = %v, expected exactly one termination", body["a_terminations"])
	}
	aTerm := aTerms[0].(map[string]interface{})
	if aTerm["object_type"] != "dcim.interface" || aTerm["object_id"] != float64(10) {
		t.Errorf("a_termination = %v, expected dcim.interface/10", aTerm)
	}

	// Reconciling the same pair again within the run (even reversed) is
	// suppressed by the processed-pairs set: still only one POST.
	if err := cr.ReconcileCable(bEnd, aEnd, link); err != nil {
		t.Fatalf("ReconcileCable() repeat error = %v", err)
	}
	f.requireMutationCount(t, 1)
}

func TestReconcileCableIdempotentWhenCableExists(t *testing.T) {
	f, c := newFakeNetBox(t)
	f.seed("dcim", "cables", client.Object{
		"id":                 5,
		"termination_a_type": "dcim.interface",
		"termination_a_id":   10,
		"termination_b_type": "dcim.interface",
		"termination_b_id":   20,
	})

	aEnd := ifaceEndpoint("sw-01", "eth0", 10)
	bEnd := ifaceEndpoint("sw-02", "eth0", 20)

	if err := NewCableReconciler(c).ReconcileCable(aEnd, bEnd, nil); err != nil {
		t.Fatalf("ReconcileCable() error = %v", err)
	}
	f.requireMutationCount(t, 0)

	// A fresh reconciler approaching the cable from the other side must
	// find it via the reversed lookup and also change nothing.
	if err := NewCableReconciler(c).ReconcileCable(bEnd, aEnd, nil); err != nil {
		t.Fatalf("ReconcileCable() reversed error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileCableUpdatesMismatchedConfig(t *testing.T) {
	f, c := newFakeNetBox(t)
	f.seed("dcim", "cables", client.Object{
		"id":                 5,
		"termination_a_type": "dcim.interface",
		"termination_a_id":   10,
		"termination_b_type": "dcim.interface",
		"termination_b_id":   20,
		"type":               "cat5e",
		"color":              "ff0000",
	})

	cr := NewCableReconciler(c)
	link := &models.LinkConfig{CableType: "cat6a", Color: "blue"}
	if err := cr.ReconcileCable(ifaceEndpoint("sw-01", "eth0", 10), ifaceEndpoint("sw-02", "eth0", 20), link); err != nil {
		t.Fatalf("ReconcileCable() error = %v", err)
	}

	muts := f.requireMutationCount(t, 1)
	if muts[0].method != "PATCH" || muts[0].path != "/api/dcim/cables/5/" {
		t.Fatalf("expected PATCH /api/dcim/cables/5/, got %s %s", muts[0].method, muts[0].path)
	}
	if muts[0].body["type"] != "cat6a" || muts[0].body["color"] != "0000ff" {
		t.Errorf("PATCH body = %v, expected type \"cat6a\" and color \"0000ff\"", muts[0].body)
	}

	cable := f.objects("dcim", "cables")[0]
	if cable["type"] != "cat6a" {
		t.Errorf("stored cable type = %v, expected \"cat6a\" after update", cable["type"])
	}
}

func TestReconcileCableDeletesWrongCableOnLocalPort(t *testing.T) {
	f, c := newFakeNetBox(t)
	// Port 10 is cabled to port 30, but the definition wants 10 <-> 20.
	f.seed("dcim", "interfaces", client.Object{"id": 10, "name": "eth0", "cable": map[string]interface{}{"id": 5}})
	f.seed("dcim", "interfaces", client.Object{"id": 20, "name": "eth0"})
	f.seed("dcim", "interfaces", client.Object{"id": 30, "name": "eth0"})
	f.seed("dcim", "cables", client.Object{
		"id":             5,
		"a_terminations": []interface{}{map[string]interface{}{"object_type": "dcim.interface", "object_id": 10}},
		"b_terminations": []interface{}{map[string]interface{}{"object_type": "dcim.interface", "object_id": 30}},
	})

	cr := NewCableReconciler(c)
	if err := cr.ReconcileCable(ifaceEndpoint("sw-01", "eth0", 10), ifaceEndpoint("sw-02", "eth0", 20), nil); err != nil {
		t.Fatalf("ReconcileCable() error = %v", err)
	}

	muts := f.requireMutationCount(t, 2)
	if muts[0].method != "DELETE" || muts[0].path != "/api/dcim/cables/5/" {
		t.Errorf("first mutation = %s %s, expected DELETE /api/dcim/cables/5/ (the blocking cable)", muts[0].method, muts[0].path)
	}
	if muts[1].method != "POST" || muts[1].path != "/api/dcim/cables/" {
		t.Errorf("second mutation = %s %s, expected POST /api/dcim/cables/ (the desired cable)", muts[1].method, muts[1].path)
	}

	cables := f.objects("dcim", "cables")
	if len(cables) != 1 {
		t.Fatalf("expected exactly 1 cable in store after cleanup, got %d", len(cables))
	}
	bTerms := cables[0]["b_terminations"].([]interface{})
	if bTerms[0].(map[string]interface{})["object_id"] != float64(20) {
		t.Errorf("remaining cable b_termination = %v, expected object_id 20", bTerms[0])
	}
}

func TestReconcileCableSkipsWhenLocalPortHasCorrectCable(t *testing.T) {
	f, c := newFakeNetBox(t)
	// Port 10 already carries cable 5 to port 20 — exactly what the
	// definition wants — but the cable lacks the legacy termination keys,
	// so findExistingCable does not see it and the local-port check must
	// recognize it instead.
	f.seed("dcim", "interfaces", client.Object{"id": 10, "name": "eth0", "cable": map[string]interface{}{"id": 5}})
	f.seed("dcim", "interfaces", client.Object{"id": 20, "name": "eth0"})
	f.seed("dcim", "cables", client.Object{
		"id":             5,
		"a_terminations": []interface{}{map[string]interface{}{"object_type": "dcim.interface", "object_id": 10}},
		"b_terminations": []interface{}{map[string]interface{}{"object_type": "dcim.interface", "object_id": 20}},
	})

	cr := NewCableReconciler(c)
	if err := cr.ReconcileCable(ifaceEndpoint("sw-01", "eth0", 10), ifaceEndpoint("sw-02", "eth0", 20), nil); err != nil {
		t.Fatalf("ReconcileCable() error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileCableSkipsWhenPeerPortHasCorrectCable(t *testing.T) {
	f, c := newFakeNetBox(t)
	// Only the peer port reports the (correct) cable.
	f.seed("dcim", "interfaces", client.Object{"id": 10, "name": "eth0"})
	f.seed("dcim", "interfaces", client.Object{"id": 20, "name": "eth0", "cable": map[string]interface{}{"id": 6}})
	f.seed("dcim", "cables", client.Object{
		"id":             6,
		"a_terminations": []interface{}{map[string]interface{}{"object_type": "dcim.interface", "object_id": 20}},
		"b_terminations": []interface{}{map[string]interface{}{"object_type": "dcim.interface", "object_id": 10}},
	})

	cr := NewCableReconciler(c)
	if err := cr.ReconcileCable(ifaceEndpoint("sw-01", "eth0", 10), ifaceEndpoint("sw-02", "eth0", 20), nil); err != nil {
		t.Fatalf("ReconcileCable() error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileCableDeletesBlockingCableOnPeerPort(t *testing.T) {
	f, c := newFakeNetBox(t)
	// The peer port 20 is cabled to an unrelated port 30: that blocking
	// cable must be deleted before the desired cable is created.
	f.seed("dcim", "interfaces", client.Object{"id": 10, "name": "eth0"})
	f.seed("dcim", "interfaces", client.Object{"id": 20, "name": "eth0", "cable": map[string]interface{}{"id": 7}})
	f.seed("dcim", "cables", client.Object{
		"id":             7,
		"a_terminations": []interface{}{map[string]interface{}{"object_type": "dcim.interface", "object_id": 20}},
		"b_terminations": []interface{}{map[string]interface{}{"object_type": "dcim.interface", "object_id": 30}},
	})

	cr := NewCableReconciler(c)
	if err := cr.ReconcileCable(ifaceEndpoint("sw-01", "eth0", 10), ifaceEndpoint("sw-02", "eth0", 20), nil); err != nil {
		t.Fatalf("ReconcileCable() error = %v", err)
	}

	muts := f.requireMutationCount(t, 2)
	if muts[0].method != "DELETE" || muts[0].path != "/api/dcim/cables/7/" {
		t.Errorf("first mutation = %s %s, expected DELETE /api/dcim/cables/7/", muts[0].method, muts[0].path)
	}
	if muts[1].method != "POST" || muts[1].path != "/api/dcim/cables/" {
		t.Errorf("second mutation = %s %s, expected POST /api/dcim/cables/", muts[1].method, muts[1].path)
	}
}

func TestReconcileCableDryRunNeverMutates(t *testing.T) {
	f, c := newFakeNetBox(t)
	// Worst case for dry-run: a blocking cable that would normally be
	// deleted, plus a missing cable that would normally be created.
	f.seed("dcim", "interfaces", client.Object{"id": 10, "name": "eth0", "cable": map[string]interface{}{"id": 5}})
	f.seed("dcim", "interfaces", client.Object{"id": 20, "name": "eth0"})
	f.seed("dcim", "cables", client.Object{
		"id":             5,
		"a_terminations": []interface{}{map[string]interface{}{"object_type": "dcim.interface", "object_id": 10}},
		"b_terminations": []interface{}{map[string]interface{}{"object_type": "dcim.interface", "object_id": 30}},
	})

	c.SetDryRun(true)
	cr := NewCableReconciler(c)
	if err := cr.ReconcileCable(ifaceEndpoint("sw-01", "eth0", 10), ifaceEndpoint("sw-02", "eth0", 20), nil); err != nil {
		t.Fatalf("ReconcileCable() dry-run error = %v", err)
	}

	f.requireMutationCount(t, 0)
	if got := len(f.objects("dcim", "cables")); got != 1 {
		t.Errorf("dry-run changed the store: %d cable(s), expected the original 1", got)
	}
}

func TestReconcileCableNilEndpoints(t *testing.T) {
	_, c := newFakeNetBox(t)
	cr := NewCableReconciler(c)
	if err := cr.ReconcileCable(nil, ifaceEndpoint("sw-02", "eth0", 20), nil); err == nil {
		t.Error("ReconcileCable(nil, b) expected error, got nil")
	}
	if err := cr.ReconcileCable(ifaceEndpoint("sw-01", "eth0", 10), nil, nil); err == nil {
		t.Error("ReconcileCable(a, nil) expected error, got nil")
	}
}
