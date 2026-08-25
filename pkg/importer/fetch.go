// SPDX-License-Identifier: Apache-2.0

package importer

import "github.com/braunma/netbox-gitops-controller/pkg/client"

// fetcher is the importer's read-only window onto NetBox. It exists so the
// mapping code reads as "list this endpoint" rather than threading the client
// and its filter shape through every call, and so a test can point the whole
// importer at a fake by handing it a client wired to one.
type fetcher struct {
	c *client.NetBoxClient
}

// list returns every object at an endpoint, following pagination. A nil filter
// lists the endpoint whole, which is the common case for a full import.
func (f fetcher) list(app, endpoint string, filter map[string]interface{}) ([]client.Object, error) {
	return f.c.Filter(app, endpoint, filter)
}

// byID indexes objects by their NetBox id, for joining child endpoints
// (interfaces to devices, IPs to interfaces) in memory rather than with a
// request per parent.
func byID(objs []client.Object) map[int]client.Object {
	m := make(map[int]client.Object, len(objs))
	for _, o := range objs {
		m[idOf(o)] = o
	}
	return m
}

// groupBy indexes objects by the id of a nested reference field, for joining a
// child list to its parents (e.g. interfaces grouped by device id).
func groupBy(objs []client.Object, refKey string) map[int][]client.Object {
	m := make(map[int][]client.Object)
	for _, o := range objs {
		ref := nested(o, refKey)
		if ref == nil {
			continue
		}
		id := idOf(client.Object(ref))
		m[id] = append(m[id], o)
	}
	return m
}
