package reconciler

import "testing"

func TestSetSiteScope(t *testing.T) {
	payload := map[string]interface{}{"name": "fabric"}
	setSiteScope(payload, 8)

	if payload["scope_type"] != "dcim.site" {
		t.Errorf("scope_type = %v, want \"dcim.site\"", payload["scope_type"])
	}
	if payload["scope_id"] != 8 {
		t.Errorf("scope_id = %v, want 8", payload["scope_id"])
	}
	if _, ok := payload["site"]; ok {
		t.Error("the legacy `site` field must not be set on a 4.2 scoped payload")
	}
}

func TestSiteIDFromScope(t *testing.T) {
	tests := []struct {
		name string
		obj  map[string]interface{}
		want int
	}{
		{
			name: "NetBox 4.2 scope_id",
			obj:  map[string]interface{}{"scope_type": "dcim.site", "scope_id": float64(8)},
			want: 8,
		},
		{
			name: "NetBox 4.2 nested scope object",
			obj: map[string]interface{}{
				"scope_type": "dcim.site",
				"scope":      map[string]interface{}{"id": float64(8)},
			},
			want: 8,
		},
		{
			name: "legacy site field fallback",
			obj:  map[string]interface{}{"site": map[string]interface{}{"id": float64(8)}},
			want: 8,
		},
		{
			name: "non-site scope is ignored",
			obj:  map[string]interface{}{"scope_type": "dcim.location", "scope_id": float64(3)},
			want: 0,
		},
		{
			name: "unscoped object",
			obj:  map[string]interface{}{"name": "global"},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := siteIDFromScope(tt.obj); got != tt.want {
				t.Errorf("siteIDFromScope(%v) = %d, want %d", tt.obj, got, tt.want)
			}
		})
	}
}
