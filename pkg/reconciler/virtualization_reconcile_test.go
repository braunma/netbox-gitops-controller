package reconciler

import (
	"reflect"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

func TestReconcileClusterTypesCreateThenIdempotentThenUpdate(t *testing.T) {
	f, c := newFakeNetBox(t)
	vr := NewVirtualizationReconciler(c)

	types := []*models.ClusterType{{Name: "VMware vSphere", Slug: "vmware-vsphere", Description: "ESXi"}}

	if err := vr.ReconcileClusterTypes(types); err != nil {
		t.Fatalf("ReconcileClusterTypes() error = %v", err)
	}
	muts := f.requireMutationCount(t, 1)
	if muts[0].method != "POST" || muts[0].path != "/api/virtualization/cluster-types/" {
		t.Errorf("expected POST /api/virtualization/cluster-types/, got %s %s", muts[0].method, muts[0].path)
	}

	// Second run is a no-op.
	f.resetMutations()
	if err := vr.ReconcileClusterTypes(types); err != nil {
		t.Fatalf("ReconcileClusterTypes() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)

	// A changed field results in exactly one PATCH with only that field.
	types[0].Description = "vSphere 8"
	if err := vr.ReconcileClusterTypes(types); err != nil {
		t.Fatalf("ReconcileClusterTypes() third run error = %v", err)
	}
	muts = f.requireMutationCount(t, 1)
	want := map[string]interface{}{"description": "vSphere 8"}
	if muts[0].method != "PATCH" || !reflect.DeepEqual(muts[0].body, want) {
		t.Errorf("expected PATCH with %v, got %s %v", want, muts[0].method, muts[0].body)
	}
}

func TestReconcileClustersResolvesReferences(t *testing.T) {
	f, c := newFakeNetBox(t)
	site := f.seed("dcim", "sites", client.Object{"name": "Berlin DC", "slug": "berlin-dc"})
	tenant := f.seed("tenancy", "tenants", client.Object{"name": "Acme Corp", "slug": "acme-corp"})

	vr := NewVirtualizationReconciler(c)

	// Type and group are created in this same run, just before the cluster;
	// they must resolve via the live lookup, not the (pre-run) cache.
	if err := vr.ReconcileClusterTypes([]*models.ClusterType{{Name: "Proxmox", Slug: "proxmox"}}); err != nil {
		t.Fatalf("ReconcileClusterTypes() error = %v", err)
	}
	if err := vr.ReconcileClusterGroups([]*models.ClusterGroup{{Name: "Production", Slug: "production"}}); err != nil {
		t.Fatalf("ReconcileClusterGroups() error = %v", err)
	}

	clusters := []*models.Cluster{{
		Name:      "prod-cluster",
		TypeSlug:  "proxmox",
		GroupSlug: "production",
		SiteSlug:  "berlin-dc",
		Tenant:    "acme-corp",
		Status:    "active",
	}}
	if err := vr.ReconcileClusters(clusters); err != nil {
		t.Fatalf("ReconcileClusters() error = %v", err)
	}

	stored := f.objects("virtualization", "clusters")
	if len(stored) != 1 {
		t.Fatalf("expected 1 cluster in store, got %d", len(stored))
	}
	cl := stored[0]
	typeID := utils.GetIDFromObject(f.objects("virtualization", "cluster-types")[0])
	groupID := utils.GetIDFromObject(f.objects("virtualization", "cluster-groups")[0])
	if got := utils.GetIDFromObject(cl["type"]); got != typeID {
		t.Errorf("cluster type = %d, expected %d", got, typeID)
	}
	if got := utils.GetIDFromObject(cl["group"]); got != groupID {
		t.Errorf("cluster group = %d, expected %d", got, groupID)
	}
	if got := utils.GetIDFromObject(cl["site"]); got != utils.GetIDFromObject(site) {
		t.Errorf("cluster site = %d, expected %d", got, utils.GetIDFromObject(site))
	}
	if got := utils.GetIDFromObject(cl["tenant"]); got != utils.GetIDFromObject(tenant) {
		t.Errorf("cluster tenant = %d, expected %d", got, utils.GetIDFromObject(tenant))
	}

	// Second run is a no-op.
	f.resetMutations()
	if err := vr.ReconcileClusters(clusters); err != nil {
		t.Fatalf("ReconcileClusters() second run error = %v", err)
	}
	f.requireMutationCount(t, 0)
}

func TestReconcileClustersSkipsUnknownType(t *testing.T) {
	f, c := newFakeNetBox(t)
	vr := NewVirtualizationReconciler(c)

	clusters := []*models.Cluster{{Name: "orphan", TypeSlug: "does-not-exist"}}
	if err := vr.ReconcileClusters(clusters); err != nil {
		t.Fatalf("ReconcileClusters() error = %v, expected unknown type to be skipped", err)
	}
	f.requireMutationCount(t, 0)
	if got := len(f.objects("virtualization", "clusters")); got != 0 {
		t.Errorf("expected no clusters created when the type is unknown, got %d", got)
	}
}

func TestReconcileClustersToleratesMissingOptionalRefs(t *testing.T) {
	f, c := newFakeNetBox(t)
	vr := NewVirtualizationReconciler(c)

	if err := vr.ReconcileClusterTypes([]*models.ClusterType{{Name: "KVM", Slug: "kvm"}}); err != nil {
		t.Fatalf("ReconcileClusterTypes() error = %v", err)
	}

	// Group, site and tenant are all unknown; the cluster is still created
	// with only its required type.
	clusters := []*models.Cluster{{
		Name:      "minimal",
		TypeSlug:  "kvm",
		GroupSlug: "nope",
		SiteSlug:  "nope",
		Tenant:    "nope",
	}}
	if err := vr.ReconcileClusters(clusters); err != nil {
		t.Fatalf("ReconcileClusters() error = %v", err)
	}

	stored := f.objects("virtualization", "clusters")
	if len(stored) != 1 {
		t.Fatalf("expected 1 cluster in store, got %d", len(stored))
	}
	for _, field := range []string{"group", "site", "tenant"} {
		if _, set := stored[0][field]; set {
			t.Errorf("cluster %s should be unset when the reference is unknown, got %v", field, stored[0][field])
		}
	}
}
