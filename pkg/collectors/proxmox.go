// SPDX-License-Identifier: Apache-2.0

package collectors

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

func init() { Register("proxmox", newProxmoxCollector) }

// defaultProxmoxTimeout bounds a single API request. A whole scan may take
// longer: one request per guest is made to read its NIC configuration.
const defaultProxmoxTimeout = 30 * time.Second

// ProxmoxCollector reads guests from a Proxmox VE cluster through its
// /api2/json API.
//
// Every request it makes is a GET. The token it authenticates with only needs
// read permission (PVEAuditor on /), and giving it more buys nothing: nothing
// in this package issues a write.
type ProxmoxCollector struct {
	source Source
	logger *utils.Logger
	client *http.Client
	// authHeader is the value sent as Authorization on every request.
	authHeader string
	baseURL    string
}

func newProxmoxCollector(src Source, logger *utils.Logger) (Collector, error) {
	token, err := src.Token()
	if err != nil {
		return nil, err
	}

	base, err := url.Parse(strings.TrimRight(src.URL, "/"))
	if err != nil {
		return nil, fmt.Errorf("source %q: url %q is not a URL: %w", src.Name, src.URL, err)
	}
	if base.Scheme != "https" && base.Scheme != "http" {
		return nil, fmt.Errorf("source %q: url %q must be http(s)", src.Name, src.URL)
	}

	verify := src.ShouldVerifyTLS()
	if !verify {
		logger.Warning("Source %s has verify_tls: false — its TLS certificate will not be verified", src.Name)
	}

	timeout := defaultProxmoxTimeout
	if src.TimeoutSeconds > 0 {
		timeout = time.Duration(src.TimeoutSeconds) * time.Second
	}

	return &ProxmoxCollector{
		source: src,
		logger: logger,
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: !verify}, // #nosec G402 -- per-source opt-in, warned about above
			},
		},
		// Proxmox API tokens are sent whole: "user@realm!tokenid=secret".
		authHeader: "PVEAPIToken=" + token,
		baseURL:    base.String(),
	}, nil
}

// Name returns the configured source name.
func (p *ProxmoxCollector) Name() string { return p.source.Name }

// Collect enumerates the cluster's guests and reads each one's configuration
// for its NICs.
func (p *ProxmoxCollector) Collect(ctx context.Context) (*discovery.Snapshot, error) {
	snap := &discovery.Snapshot{
		Source:      p.source.Name,
		CollectedAt: time.Now().UTC(),
	}

	if version, err := p.version(ctx); err != nil {
		// The version is provenance, not data. A source that will not report it
		// can still be scanned.
		p.logger.Debug("Source %s did not report its version: %v", p.source.Name, err)
	} else {
		snap.SourceVersion = version
	}

	resources, err := p.resources(ctx)
	if err != nil {
		return nil, fmt.Errorf("source %q: %w", p.source.Name, err)
	}

	for _, res := range resources {
		if res.Template != 0 {
			// A template is not a machine anyone runs; documenting it as a VM
			// would put a phantom into NetBox.
			continue
		}
		if res.Type != "qemu" && res.Type != "lxc" {
			continue
		}

		vm := discovery.VM{
			Name:     res.Name,
			Cluster:  p.source.Cluster,
			Node:     res.Node,
			Status:   proxmoxStatus(res.Status),
			VMID:     res.VMID,
			VCPUs:    res.MaxCPU,
			MemoryMB: bytesToMB(res.MaxMem),
			DiskGB:   bytesToGB(res.MaxDisk),
		}
		if vm.Name == "" {
			// Proxmox allows an unnamed guest; without a name there is nothing
			// to match on and nothing to call the object in NetBox.
			p.logger.Warning("Source %s: guest %d on node %s has no name; skipping", p.source.Name, res.VMID, res.Node)
			continue
		}

		cfg, err := p.guestConfig(ctx, res.Node, res.Type, res.VMID)
		if err != nil {
			// One unreadable guest must not cost the whole scan, but it must be
			// visible: its NICs would otherwise look like they were removed.
			p.logger.Warning("Source %s: could not read the configuration of %s (%s/%d): %v",
				p.source.Name, vm.Name, res.Node, res.VMID, err)
		} else {
			vm.Interfaces = parseGuestNICs(res.Type, cfg)
		}

		snap.VMs = append(snap.VMs, vm)
	}

	// Configuration order from the API is not guaranteed; sort so a run's
	// output does not depend on it.
	sort.Slice(snap.VMs, func(i, j int) bool {
		if snap.VMs[i].Name != snap.VMs[j].Name {
			return snap.VMs[i].Name < snap.VMs[j].Name
		}
		return snap.VMs[i].VMID < snap.VMs[j].VMID
	})

	p.logger.Info("Source %s: found %d guest(s)", p.source.Name, len(snap.VMs))
	return snap, nil
}

// proxmoxResource is one entry of /cluster/resources?type=vm.
type proxmoxResource struct {
	VMID     int    `json:"vmid"`
	Name     string `json:"name"`
	Node     string `json:"node"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	MaxCPU   int    `json:"maxcpu"`
	MaxMem   int64  `json:"maxmem"`
	MaxDisk  int64  `json:"maxdisk"`
	Template int    `json:"template"`
}

// version reports the Proxmox release, for provenance.
func (p *ProxmoxCollector) version(ctx context.Context) (string, error) {
	var payload struct {
		Version string `json:"version"`
		Release string `json:"release"`
	}
	if err := p.get(ctx, "/api2/json/version", &payload); err != nil {
		return "", err
	}
	if payload.Version != "" {
		return payload.Version, nil
	}
	return payload.Release, nil
}

// resources lists every guest in the cluster in one request, rather than
// walking the nodes and asking each for its qemu and lxc lists.
func (p *ProxmoxCollector) resources(ctx context.Context) ([]proxmoxResource, error) {
	var payload []proxmoxResource
	if err := p.get(ctx, "/api2/json/cluster/resources?type=vm", &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// guestConfig reads one guest's configuration, which is where the NICs are.
// The cluster resource list does not carry them.
func (p *ProxmoxCollector) guestConfig(ctx context.Context, node, guestType string, vmid int) (map[string]string, error) {
	// The values are mostly strings but not all (cores, memory, ...), so decode
	// loosely and stringify.
	var payload map[string]interface{}
	path := fmt.Sprintf("/api2/json/nodes/%s/%s/%d/config", url.PathEscape(node), guestType, vmid)
	if err := p.get(ctx, path, &payload); err != nil {
		return nil, err
	}

	cfg := make(map[string]string, len(payload))
	for key, value := range payload {
		switch v := value.(type) {
		case string:
			cfg[key] = v
		case float64:
			cfg[key] = strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			cfg[key] = strconv.FormatBool(v)
		}
	}
	return cfg, nil
}

// get performs one authenticated GET and decodes the API's `data` envelope.
func (p *ProxmoxCollector) get(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", p.authHeader)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only request

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}

	// Every Proxmox response wraps its payload in {"data": ...}.
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("GET %s: response carried no data", path)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	return nil
}

// proxmoxStatus maps a Proxmox guest state onto a NetBox virtual-machine
// status. An unrecognised state yields "" so nothing is written rather than a
// value NetBox would reject.
func proxmoxStatus(status string) string {
	switch strings.ToLower(status) {
	case "running":
		return "active"
	case "stopped", "paused", "suspended":
		return "offline"
	default:
		return ""
	}
}

// bytesToMB converts to whole megabytes, NetBox's unit for VM memory.
func bytesToMB(b int64) int {
	if b <= 0 {
		return 0
	}
	return int(b / (1024 * 1024))
}

// bytesToGB converts to whole gigabytes, NetBox's unit for VM disk.
func bytesToGB(b int64) int {
	if b <= 0 {
		return 0
	}
	return int(b / (1024 * 1024 * 1024))
}

// parseGuestNICs extracts the NICs from a guest configuration. Proxmox encodes
// each one as a single comma-separated string under netN, and the two guest
// types spell it differently:
//
//	qemu: net0 = "virtio=BC:24:11:00:00:01,bridge=vmbr0,tag=100"
//	lxc:  net0 = "name=eth0,bridge=vmbr0,hwaddr=BC:24:11:00:00:02,ip=10.0.0.5/24"
//
// A qemu NIC has no name of its own, so the config key is used.
func parseGuestNICs(guestType string, cfg map[string]string) []discovery.NIC {
	var keys []string
	for key := range cfg {
		if strings.HasPrefix(key, "net") && isNumericSuffix(key[len("net"):]) {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		return netIndex(keys[i]) < netIndex(keys[j])
	})

	nics := make([]discovery.NIC, 0, len(keys))
	for _, key := range keys {
		nic := discovery.NIC{Name: key, Enabled: true}

		for _, part := range strings.Split(cfg[key], ",") {
			name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok {
				continue
			}
			switch strings.ToLower(name) {
			case "name":
				if guestType == "lxc" && value != "" {
					nic.Name = value
				}
			case "hwaddr", "macaddr":
				nic.MAC = strings.ToUpper(value)
			case "ip":
				// "dhcp" and "manual" are policies, not addresses.
				if strings.Contains(value, "/") {
					nic.IPs = append(nic.IPs, value)
				}
			case "link_down":
				if value == "1" {
					nic.Enabled = false
				}
			case "virtio", "e1000", "rtl8139", "vmxnet3", "e1000e":
				// A qemu NIC's model is the key and the MAC is its value.
				nic.MAC = strings.ToUpper(value)
			}
		}

		nics = append(nics, nic)
	}
	return nics
}

// isNumericSuffix reports whether s is a non-empty run of digits, so that
// "net0" is recognised as a NIC and "netmask" is not.
func isNumericSuffix(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// netIndex returns the number in a netN key, for ordering.
func netIndex(key string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(key, "net"))
	if err != nil {
		return 0
	}
	return n
}
