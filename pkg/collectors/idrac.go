// SPDX-License-Identifier: Apache-2.0

package collectors

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braunma/netbox-gitops-controller/pkg/discovery"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// defaultIDRACTimeout bounds a single Redfish request. A whole host takes
// longer: one request is made per processor, per DIMM slot and per drive.
const defaultIDRACTimeout = 30 * time.Second

// defaultIDRACPort is the HTTPS port an iDRAC listens on.
const defaultIDRACPort = 443

// Target is one host a scan source reads: a device the inventory declares,
// and the address it declares on the source's interface.
type Target struct {
	// Device is the declared device name. It is what the result is written
	// to, so a scan never has to guess which machine it reached.
	Device string
	// Address is the host address, without a prefix length.
	Address string
}

// ScanResult is what one host answered.
type ScanResult struct {
	Target Target
	// Device holds what the BMC reported. It is meaningful only when Err is
	// nil.
	Device discovery.Device
	// Incomplete names the parts of the inventory the BMC did not answer for
	// (processors, memory, storage, power). The facts of those parts are left
	// unreported, so the values NetBox already holds stay in place.
	Incomplete []string
	// Err is why the host could not be scanned at all.
	Err error
}

// IDRACScanner reads Dell iDRACs over Redfish.
//
// Every request it makes is a GET against the BMC, so a read-only iDRAC user
// is enough. It holds no NetBox client: what is done with the results is the
// caller's decision.
type IDRACScanner struct {
	source   Source
	logger   *utils.Logger
	client   *http.Client
	username string
	password string
	port     int
	// scheme is "https" outside tests.
	scheme string
	// now is the clock, replaceable in tests.
	now func() time.Time
}

// NewIDRACScanner builds the scanner for one source of type "idrac".
func NewIDRACScanner(src Source, logger *utils.Logger) (*IDRACScanner, error) {
	if src.Type != TypeIDRAC {
		return nil, fmt.Errorf("source %q: type %q is not a scan source", src.Name, src.Type)
	}
	username, password, err := src.Credentials()
	if err != nil {
		return nil, err
	}

	verify := src.ShouldVerifyTLS()
	if !verify {
		logger.Warning("Source %s has verify_tls: false — the TLS certificates of its BMCs will not be verified", src.Name)
	}

	timeout := defaultIDRACTimeout
	if src.TimeoutSeconds > 0 {
		timeout = time.Duration(src.TimeoutSeconds) * time.Second
	}
	port := defaultIDRACPort
	if src.Port > 0 {
		port = src.Port
	}

	return &IDRACScanner{
		source: src,
		logger: logger,
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig:     &tls.Config{InsecureSkipVerify: !verify}, // #nosec G402 -- per-source opt-in, warned about above
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     30 * time.Second,
			},
			// A BMC that redirects is sending the credentials somewhere the
			// inventory did not name.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		username: username,
		password: password,
		port:     port,
		scheme:   "https",
		now:      time.Now,
	}, nil
}

// Name returns the configured source name.
func (s *IDRACScanner) Name() string { return s.source.Name }

// Scan reads every target and returns one result per target, in the order the
// targets were given. A host that fails does not stop the others.
func (s *IDRACScanner) Scan(ctx context.Context, targets []Target) []ScanResult {
	results := make([]ScanResult, len(targets))

	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := s.source.ScanConcurrency()
	if workers > len(targets) {
		workers = len(targets)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = s.scanHost(ctx, targets[i])
			}
		}()
	}
	for i := range targets {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return results
}

// scanHost reads one BMC. The system resource is required: a host that does
// not answer for it was not scanned. Everything after it is best effort, and
// a part that fails is named in Incomplete rather than failing the host.
func (s *IDRACScanner) scanHost(ctx context.Context, target Target) ScanResult {
	result := ScanResult{Target: target}
	client := &redfishClient{
		baseURL:  s.scheme + "://" + net.JoinHostPort(target.Address, strconv.Itoa(s.port)),
		username: s.username,
		password: s.password,
		http:     s.client,
	}

	var system redfishSystem
	if err := client.get(ctx, redfishSystemPath, &system); err != nil {
		result.Err = err
		return result
	}

	dev := discovery.Device{
		Name:         system.HostName,
		Serial:       strings.TrimSpace(system.SerialNumber),
		ServiceTag:   strings.TrimSpace(system.SKU),
		Manufacturer: system.Manufacturer,
		Model:        system.Model,
		MgmtIP:       target.Address,
		HW: discovery.HardwareFacts{
			CPUCount:      system.ProcessorSummary.Count,
			CPUModel:      system.ProcessorSummary.Model,
			RAMTotalGB:    int(system.MemorySummary.TotalSystemMemoryGiB),
			BIOSVersion:   system.BiosVersion,
			PowerState:    system.PowerState,
			LastInventory: s.now().UTC().Truncate(time.Second),
		},
	}
	if system.Oem.Dell != nil && system.Oem.Dell.DellSystem != nil {
		dev.HW.RAMSlotsTotal = system.Oem.Dell.DellSystem.MaxDIMMSlots
	}

	parts := []struct {
		name string
		read func(context.Context, *redfishClient, *discovery.HardwareFacts) error
	}{
		{"processors", s.readProcessors},
		{"memory", s.readMemory},
		{"storage", s.readStorage},
		{"power", s.readPower},
	}
	for _, part := range parts {
		if err := part.read(ctx, client, &dev.HW); err != nil {
			if errors.Is(err, errRedfishAuth) {
				// The system resource answered, so this user may read some
				// resources and not others. Say so instead of "incomplete".
				result.Err = fmt.Errorf("%s: %w", part.name, err)
				return result
			}
			s.logger.Debug("%s (%s): %s not read: %v", target.Device, target.Address, part.name, err)
			result.Incomplete = append(result.Incomplete, part.name)
		}
	}

	result.Device = dev
	return result
}

// readProcessors counts the installed CPUs and takes the core count of the
// first socket. GPUs and other accelerators sit in the same collection; they
// are counted apart and summarised by model.
func (s *IDRACScanner) readProcessors(ctx context.Context, client *redfishClient, hw *discovery.HardwareFacts) error {
	var collection redfishCollection
	if err := client.get(ctx, redfishProcessorsPath, &collection); err != nil {
		return err
	}

	var cpus []redfishProcessor
	var gpus []discovery.GPU
	gpuMemoryMiB := 0
	var failed error
	for _, member := range collection.Members {
		var p redfishProcessor
		if err := client.get(ctx, member.OdataID, &p); err != nil {
			failed = err
			continue
		}
		if p.Status.State != redfishStateEnabled {
			continue
		}
		if p.isAccelerator() {
			memoryMiB := 0
			for _, m := range p.ProcessorMemory {
				memoryMiB += m.CapacityMiB
			}
			gpuMemoryMiB += memoryMiB
			gpus = append(gpus, discovery.GPU{Model: p.Model, MemoryGB: memoryMiB / 1024})
			continue
		}
		cpus = append(cpus, p)
	}
	if failed != nil {
		// A count from a partial list would be a wrong count.
		return failed
	}

	// The whole collection was read, so a machine without a GPU is known to
	// have none.
	hw.GPUsRead = true
	hw.GPUCount = len(gpus)
	hw.GPUModel = discovery.GPUSummary(gpus)
	hw.GPUMemoryGB = gpuMemoryMiB / 1024

	if len(cpus) > 0 {
		hw.CPUCount = len(cpus)
		hw.CPUCores = cpus[0].TotalCores
		if hw.CPUModel == "" {
			hw.CPUModel = cpus[0].Model
		}
	}
	return nil
}

// readMemory counts the DIMM slots and takes the memory type and speed of the
// first populated one.
func (s *IDRACScanner) readMemory(ctx context.Context, client *redfishClient, hw *discovery.HardwareFacts) error {
	var collection redfishCollection
	if err := client.get(ctx, redfishMemoryPath, &collection); err != nil {
		return err
	}

	slots, used, totalMiB := 0, 0, 0
	var failed error
	for _, member := range collection.Members {
		var m redfishMemory
		if err := client.get(ctx, member.OdataID, &m); err != nil {
			failed = err
			continue
		}
		slots++
		if m.Status.State != redfishStateEnabled {
			continue
		}
		used++
		totalMiB += m.CapacityMiB
		if hw.MemoryType == "" {
			hw.MemoryType = m.MemoryDeviceType
			hw.MemorySpeedMHz = m.OperatingSpeedMhz
		}
	}
	if failed != nil {
		hw.MemoryType, hw.MemorySpeedMHz = "", 0
		return failed
	}

	// The Dell OEM block knows the slot count of the board; the collection
	// only lists the slots the BMC chose to report.
	if hw.RAMSlotsTotal == 0 {
		hw.RAMSlotsTotal = slots
	}
	hw.RAMSlotsUsed = used
	hw.RAMSlotsAvailable = hw.RAMSlotsTotal - used
	if hw.RAMSlotsAvailable < 0 {
		hw.RAMSlotsAvailable = 0
	}
	if fromDIMMs := totalMiB / 1024; fromDIMMs > hw.RAMTotalGB {
		hw.RAMTotalGB = fromDIMMs
	}
	return nil
}

// readStorage counts the drives behind every storage controller and
// summarises them by capacity.
func (s *IDRACScanner) readStorage(ctx context.Context, client *redfishClient, hw *discovery.HardwareFacts) error {
	var collection redfishCollection
	if err := client.get(ctx, redfishStoragePath, &collection); err != nil {
		return err
	}

	var capacitiesGB []int
	var totalBytes int64
	for _, member := range collection.Members {
		var controller redfishStorage
		if err := client.get(ctx, member.OdataID, &controller); err != nil {
			return err
		}
		for _, link := range controller.Drives {
			var d redfishDrive
			if err := client.get(ctx, link.OdataID, &d); err != nil {
				return err
			}
			capacitiesGB = append(capacitiesGB, int(float64(d.CapacityBytes)/(1<<30)))
			totalBytes += d.CapacityBytes
		}
	}

	hw.DiskCount = len(capacitiesGB)
	hw.StorageSummary = discovery.StorageSummary(capacitiesGB)
	hw.StorageTotalTB = float64(totalBytes) / (1 << 40)
	return nil
}

// readPower takes the current and the peak draw of the chassis.
func (s *IDRACScanner) readPower(ctx context.Context, client *redfishClient, hw *discovery.HardwareFacts) error {
	var power redfishPower
	if err := client.get(ctx, redfishPowerPath, &power); err != nil {
		return err
	}
	if len(power.PowerControl) > 0 {
		hw.PowerConsumedWatts = int(power.PowerControl[0].PowerConsumedWatts)
		hw.PowerPeakWatts = int(power.PowerControl[0].PowerMetrics.MaxConsumedWatts)
	}
	return nil
}
