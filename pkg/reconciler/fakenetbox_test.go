// SPDX-License-Identifier: Apache-2.0

package reconciler

// The in-memory NetBox fake now lives in internal/nbtest so the importer can
// share it. This file is a thin shim that re-exposes it under the lowercase
// names the reconciler's own tests already use, so those tests are unchanged.

import (
	"net/url"
	"testing"

	"github.com/braunma/netbox-gitops-controller/internal/nbtest"
	"github.com/braunma/netbox-gitops-controller/pkg/client"
)

// fakeNetBox wraps the shared fake, promoting its exported methods and adding
// lowercase aliases for the reconciler tests.
type fakeNetBox struct {
	*nbtest.FakeNetBox
}

// recordedRequest mirrors nbtest.RecordedRequest with the field names the
// reconciler tests read.
type recordedRequest struct {
	method string
	path   string
	body   map[string]interface{}
}

func newFakeNetBox(t *testing.T) (*fakeNetBox, *client.NetBoxClient) {
	f, c := nbtest.New(t)
	return &fakeNetBox{f}, c
}

func (f *fakeNetBox) seed(app, endpoint string, obj client.Object) client.Object {
	return f.Seed(app, endpoint, obj)
}

func (f *fakeNetBox) objects(app, endpoint string) []client.Object {
	return f.Objects(app, endpoint)
}

func (f *fakeNetBox) resetMutations() { f.ResetMutations() }

func (f *fakeNetBox) mutationLog() []recordedRequest {
	return convertRequests(f.MutationLog())
}

func (f *fakeNetBox) requireMutationCount(t *testing.T, n int) []recordedRequest {
	t.Helper()
	return convertRequests(f.RequireMutationCount(t, n))
}

func convertRequests(in []nbtest.RecordedRequest) []recordedRequest {
	out := make([]recordedRequest, len(in))
	for i, r := range in {
		out[i] = recordedRequest{method: r.Method, path: r.Path, body: r.Body}
	}
	return out
}

func matchesFilters(obj client.Object, query url.Values) bool {
	return nbtest.MatchesFilters(obj, query)
}

func mergeCustomFields(existing, patch interface{}) interface{} {
	return nbtest.MergeCustomFields(existing, patch)
}
