// SPDX-License-Identifier: Apache-2.0

package collectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// fakeProxmox serves the three endpoints the collector reads, from recorded
// payloads shaped like a real Proxmox VE 8 answers them.
func fakeProxmox(t *testing.T, seen *[]string) *httptest.Server {
	t.Helper()

	responses := map[string]string{
		"/api2/json/version": `{"data":{"version":"8.2.2","release":"8.2","repoid":"9355359cd7afbae4"}}`,
		"/api2/json/cluster/resources": `{"data":[
			{"vmid":101,"name":"web-01","node":"pve-01","type":"qemu","status":"running","maxcpu":4,"maxmem":8589934592,"maxdisk":107374182400},
			{"vmid":102,"name":"db-01","node":"pve-02","type":"qemu","status":"stopped","maxcpu":8,"maxmem":17179869184,"maxdisk":536870912000},
			{"vmid":150,"name":"dns-01","node":"pve-01","type":"lxc","status":"running","maxcpu":1,"maxmem":536870912,"maxdisk":8589934592},
			{"vmid":800,"name":"ubuntu-2204-template","node":"pve-01","type":"qemu","status":"stopped","maxcpu":2,"maxmem":2147483648,"maxdisk":10737418240,"template":1},
			{"vmid":9,"name":"local-lvm","node":"pve-01","type":"storage","status":"available"}
		]}`,
		"/api2/json/nodes/pve-01/qemu/101/config": `{"data":{"cores":4,"sockets":1,"memory":8192,
			"net0":"virtio=BC:24:11:00:00:01,bridge=vmbr0,tag=100","net1":"virtio=BC:24:11:00:00:02,bridge=vmbr1,link_down=1",
			"ostype":"l26","scsi0":"local-lvm:vm-101-disk-0,size=100G"}}`,
		"/api2/json/nodes/pve-02/qemu/102/config": `{"data":{"cores":8,"memory":16384,
			"net0":"e1000=BC:24:11:00:00:03,bridge=vmbr0"}}`,
		"/api2/json/nodes/pve-01/lxc/150/config": `{"data":{"cores":1,"memory":512,
			"net0":"name=eth0,bridge=vmbr0,hwaddr=BC:24:11:00:00:04,ip=10.0.100.9/24,type=veth"}}`,
	}

	// A malformed recording would make the fake test nothing at all.
	for path, body := range responses {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &envelope); err != nil {
			t.Fatalf("recorded payload for %s is not valid JSON: %v", path, err)
		}
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = append(*seen, r.Method+" "+r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "PVEAPIToken=root@pam!gitops=s3cret" {
			t.Errorf("Authorization = %q, want the configured API token", got)
		}
		body, ok := responses[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func newTestCollector(t *testing.T, srv *httptest.Server, cluster string) Collector {
	t.Helper()
	t.Setenv("PROXMOX_TOKEN", "root@pam!gitops=s3cret")
	c, err := New(Source{
		Name: "pve-prod", Type: "proxmox", URL: srv.URL,
		TokenEnv: "PROXMOX_TOKEN", Cluster: cluster,
	}, utils.NewLogger(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestProxmoxCollect(t *testing.T) {
	var seen []string
	srv := fakeProxmox(t, &seen)
	defer srv.Close()

	snap, err := newTestCollector(t, srv, "berlin-prod-cluster").Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if snap.Source != "pve-prod" {
		t.Errorf("Source = %q, want the configured source name", snap.Source)
	}
	if snap.SourceVersion != "8.2.2" {
		t.Errorf("SourceVersion = %q, want the reported Proxmox version", snap.SourceVersion)
	}
	if snap.CollectedAt.IsZero() {
		t.Error("CollectedAt was not set")
	}
	if len(snap.Devices) != 0 {
		t.Errorf("a Proxmox scan reported %d devices; it knows only about guests", len(snap.Devices))
	}

	// The template and the storage entry are both filtered out, and the rest
	// come back sorted by name regardless of the API's order.
	var names []string
	for _, vm := range snap.VMs {
		names = append(names, vm.Name)
	}
	if want := "db-01,dns-01,web-01"; strings.Join(names, ",") != want {
		t.Fatalf("VM names = %v, want %s (template and non-guest resources filtered, sorted)", names, want)
	}

	web := snap.VMs[2]
	if web.VMID != 101 || web.Node != "pve-01" || web.Cluster != "berlin-prod-cluster" {
		t.Errorf("web-01 = %+v, want vmid 101 on pve-01 in the configured cluster", web)
	}
	if web.Status != "active" {
		t.Errorf("web-01 status = %q, want a running guest mapped to NetBox's \"active\"", web.Status)
	}
	if web.VCPUs != 4 || web.MemoryMB != 8192 || web.DiskGB != 100 {
		t.Errorf("web-01 sizing = %d vcpu / %d MB / %d GB, want 4 / 8192 / 100", web.VCPUs, web.MemoryMB, web.DiskGB)
	}
	if len(web.Interfaces) != 2 {
		t.Fatalf("web-01 has %d interfaces, want 2", len(web.Interfaces))
	}
	if web.Interfaces[0].Name != "net0" || web.Interfaces[0].MAC != "BC:24:11:00:00:01" || !web.Interfaces[0].Enabled {
		t.Errorf("web-01 net0 = %+v, want the qemu model's MAC and an enabled link", web.Interfaces[0])
	}
	if web.Interfaces[1].Enabled {
		t.Error("web-01 net1 declares link_down=1 and must not be reported as enabled")
	}

	if db := snap.VMs[0]; db.Status != "offline" {
		t.Errorf("db-01 status = %q, want a stopped guest mapped to \"offline\"", db.Status)
	}

	// An LXC container names its NIC itself and may carry a static address.
	dns := snap.VMs[1]
	if len(dns.Interfaces) != 1 {
		t.Fatalf("dns-01 has %d interfaces, want 1", len(dns.Interfaces))
	}
	if dns.Interfaces[0].Name != "eth0" || dns.Interfaces[0].MAC != "BC:24:11:00:00:04" {
		t.Errorf("dns-01 net0 = %+v, want the container's own interface name and hwaddr", dns.Interfaces[0])
	}
	if len(dns.Interfaces[0].IPs) != 1 || dns.Interfaces[0].IPs[0] != "10.0.100.9/24" {
		t.Errorf("dns-01 addresses = %v, want the configured CIDR", dns.Interfaces[0].IPs)
	}

	// Read-only: nothing but GETs, and no config request for the template.
	for _, call := range seen {
		if !strings.HasPrefix(call, "GET ") {
			t.Errorf("the collector made a non-GET request: %s", call)
		}
		if strings.Contains(call, "/800/") {
			t.Errorf("the collector read the configuration of a template: %s", call)
		}
	}
}

// A guest whose configuration cannot be read still reaches the snapshot, minus
// its NICs: dropping it would look like the VM had been deleted.
func TestProxmoxCollectKeepsGuestWithUnreadableConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api2/json/cluster/resources":
			_, _ = w.Write([]byte(`{"data":[{"vmid":101,"name":"web-01","node":"pve-01","type":"qemu","status":"running","maxcpu":2}]}`))
		default:
			http.Error(w, "forbidden", http.StatusForbidden)
		}
	}))
	defer srv.Close()

	snap, err := newTestCollector(t, srv, "").Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(snap.VMs) != 1 || snap.VMs[0].Name != "web-01" {
		t.Fatalf("VMs = %+v, want the guest kept despite its unreadable config", snap.VMs)
	}
	if len(snap.VMs[0].Interfaces) != 0 {
		t.Error("NICs were invented for a guest whose configuration could not be read")
	}
}

// An unreachable source fails the run; a partial scan would read as objects
// having disappeared.
func TestProxmoxCollectFailsOnUnreadableResourceList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	if _, err := newTestCollector(t, srv, "").Collect(context.Background()); err == nil {
		t.Fatal("expected an error when the guest list cannot be read")
	} else if !strings.Contains(err.Error(), "pve-prod") {
		t.Errorf("error = %q, want it to name the source", err)
	}
}

func TestProxmoxCollectorRequiresToken(t *testing.T) {
	if err := os.Unsetenv("PROXMOX_TOKEN"); err != nil {
		t.Fatal(err)
	}
	_, err := New(Source{Name: "pve-prod", Type: "proxmox", URL: "https://pve.example.com:8006", TokenEnv: "PROXMOX_TOKEN"},
		utils.NewLogger(false))
	if err == nil {
		t.Fatal("expected an error for an unset token variable")
	}
	if !strings.Contains(err.Error(), "PROXMOX_TOKEN") {
		t.Errorf("error = %q, want it to name the variable that is unset", err)
	}
}

func TestParseGuestNICs(t *testing.T) {
	tests := []struct {
		name      string
		guestType string
		cfg       map[string]string
		want      string // name|mac|ips|enabled, one NIC per line
	}{
		{
			"qemu nics are ordered numerically, not lexically",
			"qemu",
			map[string]string{
				"net10": "virtio=BC:24:11:00:00:0B,bridge=vmbr0",
				"net2":  "virtio=BC:24:11:00:00:03,bridge=vmbr0",
				"net0":  "virtio=BC:24:11:00:00:01,bridge=vmbr0",
			},
			"net0|BC:24:11:00:00:01||true\nnet2|BC:24:11:00:00:03||true\nnet10|BC:24:11:00:00:0B||true",
		},
		{
			"a dhcp container NIC carries no address",
			"lxc",
			map[string]string{"net0": "name=eth0,bridge=vmbr0,hwaddr=aa:bb:cc:dd:ee:ff,ip=dhcp"},
			"eth0|AA:BB:CC:DD:EE:FF||true",
		},
		{
			"keys that merely start with net are not NICs",
			"qemu",
			map[string]string{"netmask": "255.255.255.0", "net0": "virtio=BC:24:11:00:00:01"},
			"net0|BC:24:11:00:00:01||true",
		},
		{"no nics at all", "qemu", map[string]string{"cores": "4"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lines []string
			for _, nic := range parseGuestNICs(tt.guestType, tt.cfg) {
				lines = append(lines, strings.Join([]string{
					nic.Name, nic.MAC, strings.Join(nic.IPs, " "),
					map[bool]string{true: "true", false: "false"}[nic.Enabled],
				}, "|"))
			}
			if got := strings.Join(lines, "\n"); got != tt.want {
				t.Errorf("parseGuestNICs =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
