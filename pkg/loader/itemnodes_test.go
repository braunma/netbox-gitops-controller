// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// parseRoot unmarshals a YAML document and returns its root content node.
func parseRoot(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return doc.Content[0]
}

// The grouped form accepts `items:` as an exact synonym of `devices:`, so a
// reverse import can write definition kinds (sites, roles) in the readable
// grouped shape.
func TestItemNodesItemsSynonym(t *testing.T) {
	root := parseRoot(t, `defaults:
  status: "active"
items:
  - name: "berlin"
    slug: "bln"
  - name: "munich"
    slug: "muc"
`)
	nodes, defaults, err := ItemNodes("sites.yaml", root)
	if err != nil {
		t.Fatalf("ItemNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("got %d item nodes, want 2", len(nodes))
	}
	if defaults == nil {
		t.Fatal("defaults block was not returned")
	}
}

func TestItemNodesRejectsBothListKeys(t *testing.T) {
	root := parseRoot(t, `items:
  - name: "a"
devices:
  - name: "b"
`)
	if _, _, err := ItemNodes("f.yaml", root); err == nil ||
		!strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected a both-keys error, got %v", err)
	}
}

func TestItemNodesStrayKeyBesideItems(t *testing.T) {
	root := parseRoot(t, `items:
  - name: "a"
stray: 1
`)
	if _, _, err := ItemNodes("f.yaml", root); err == nil ||
		!strings.Contains(err.Error(), `"items"`) || !strings.Contains(err.Error(), `"stray"`) {
		t.Fatalf("expected a stray-key error naming items and stray, got %v", err)
	}
}

func TestItemNodesItemsMustBeList(t *testing.T) {
	root := parseRoot(t, `items:
  name: "a"
`)
	if _, _, err := ItemNodes("f.yaml", root); err == nil ||
		!strings.Contains(err.Error(), `"items" must be a list`) {
		t.Fatalf("expected an items-not-a-list error, got %v", err)
	}
}
