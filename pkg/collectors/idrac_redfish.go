// SPDX-License-Identifier: Apache-2.0

package collectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// The Redfish resources an iDRAC scan reads. The paths are Dell's: an iDRAC
// exposes exactly one system and one chassis under these fixed names.
const (
	redfishSystemPath     = "/redfish/v1/Systems/System.Embedded.1"
	redfishProcessorsPath = redfishSystemPath + "/Processors"
	redfishMemoryPath     = redfishSystemPath + "/Memory"
	redfishStoragePath    = redfishSystemPath + "/Storage"
	redfishPowerPath      = "/redfish/v1/Chassis/System.Embedded.1/Power"
)

// redfishStateEnabled is the Redfish state of a component that is present and
// working: an installed processor, a populated DIMM slot.
const redfishStateEnabled = "Enabled"

// maxRedfishBody bounds one response. The largest resource a scan reads is a
// few kilobytes; anything near this size is not a Redfish document.
const maxRedfishBody = 4 << 20

// errRedfishAuth is returned when the BMC rejects the credentials. It is told
// apart from every other failure because the fix is different: the password,
// not the network.
var errRedfishAuth = errors.New("the BMC rejected the credentials (401/403)")

// Only the fields a scan uses are declared; everything else a BMC reports is
// ignored by the decoder.

type redfishLink struct {
	OdataID string `json:"@odata.id"`
}

type redfishCollection struct {
	Members []redfishLink `json:"Members"`
}

type redfishStatus struct {
	State string `json:"State"`
}

type redfishSystem struct {
	Model        string `json:"Model"`
	Manufacturer string `json:"Manufacturer"`
	SerialNumber string `json:"SerialNumber"`
	// SKU is where Dell puts the service tag.
	SKU         string `json:"SKU"`
	BiosVersion string `json:"BiosVersion"`
	HostName    string `json:"HostName"`
	PowerState  string `json:"PowerState"`

	ProcessorSummary struct {
		Count int    `json:"Count"`
		Model string `json:"Model"`
	} `json:"ProcessorSummary"`
	MemorySummary struct {
		TotalSystemMemoryGiB float64 `json:"TotalSystemMemoryGiB"`
	} `json:"MemorySummary"`

	Oem struct {
		Dell *struct {
			DellSystem *struct {
				MaxDIMMSlots int `json:"MaxDIMMSlots"`
			} `json:"DellSystem"`
		} `json:"Dell"`
	} `json:"Oem"`
}

type redfishProcessor struct {
	Model         string        `json:"Model"`
	ProcessorType string        `json:"ProcessorType"`
	TotalCores    int           `json:"TotalCores"`
	Status        redfishStatus `json:"Status"`
	// ProcessorMemory is where a GPU lists its own memory, when it does.
	ProcessorMemory []struct {
		CapacityMiB int `json:"CapacityMiB"`
	} `json:"ProcessorMemory"`
}

// isAccelerator reports whether the entry is a GPU or another accelerator. An
// iDRAC lists those in the processor collection next to the CPUs, and they
// must not be counted as sockets; they are reported as GPUs instead.
func (p redfishProcessor) isAccelerator() bool {
	return p.ProcessorType == "Accelerator" || p.ProcessorType == "GPU"
}

type redfishMemory struct {
	MemoryDeviceType string `json:"MemoryDeviceType"`
	CapacityMiB      int    `json:"CapacityMiB"`
	// The spelling is the Redfish schema's, not a typo here.
	OperatingSpeedMhz int           `json:"OperatingSpeedMhz"`
	Status            redfishStatus `json:"Status"`
}

type redfishStorage struct {
	Drives []redfishLink `json:"Drives"`
}

type redfishDrive struct {
	CapacityBytes int64 `json:"CapacityBytes"`
}

type redfishPower struct {
	PowerControl []struct {
		PowerConsumedWatts float64 `json:"PowerConsumedWatts"`
		PowerMetrics       struct {
			MaxConsumedWatts float64 `json:"MaxConsumedWatts"`
		} `json:"PowerMetrics"`
	} `json:"PowerControl"`
}

// redfishClient reads one BMC. Every request it makes is a GET.
type redfishClient struct {
	baseURL  string
	username string
	password string
	http     *http.Client
}

// get performs one authenticated GET and decodes the JSON response.
func (c *redfishClient) get(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only request

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errRedfishAuth
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRedfishBody))
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	return nil
}
