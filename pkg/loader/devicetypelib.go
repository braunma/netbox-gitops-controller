package loader

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
	"gopkg.in/yaml.v3"
)

// The community device type library (github.com/netbox-community/devicetype-library)
// publishes one device type per file, laid out as <library>/<Manufacturer>/<model>.yaml.
// Its schema differs from this project's native device type format in three ways:
// component lists are hyphenated (`console-ports`, not `console_ports`), each file
// holds a single mapping rather than a list, and several fields default rather than
// being required. The types below mirror that schema so a library checkout can be
// consumed unchanged; toModel translates it into the native model.

// dtlDeviceType is one device type as published by the community library.
type dtlDeviceType struct {
	Manufacturer  string   `yaml:"manufacturer"`
	Model         string   `yaml:"model"`
	Slug          string   `yaml:"slug"`
	PartNumber    string   `yaml:"part_number"`
	UHeight       *float64 `yaml:"u_height"`
	IsFullDepth   *bool    `yaml:"is_full_depth"`
	SubdeviceRole string   `yaml:"subdevice_role"`
	Airflow       string   `yaml:"airflow"`
	Description   string   `yaml:"description"`
	Comments      string   `yaml:"comments"`
	Weight        float64  `yaml:"weight"`
	WeightUnit    string   `yaml:"weight_unit"`

	ConsolePorts       []dtlNamedType   `yaml:"console-ports"`
	ConsoleServerPorts []dtlNamedType   `yaml:"console-server-ports"`
	PowerPorts         []dtlPowerPort   `yaml:"power-ports"`
	PowerOutlets       []dtlPowerOutlet `yaml:"power-outlets"`
	Interfaces         []dtlInterface   `yaml:"interfaces"`
	FrontPorts         []dtlFrontPort   `yaml:"front-ports"`
	RearPorts          []dtlRearPort    `yaml:"rear-ports"`
	ModuleBays         []dtlBay         `yaml:"module-bays"`
	DeviceBays         []dtlBay         `yaml:"device-bays"`

	// Carried only so their presence can be reported; NetBox inventory item
	// templates are not reconciled by this project.
	InventoryItems []map[string]interface{} `yaml:"inventory-items"`
}

type dtlNamedType struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

type dtlPowerPort struct {
	Name          string `yaml:"name"`
	Type          string `yaml:"type"`
	MaximumDraw   int    `yaml:"maximum_draw"`
	AllocatedDraw int    `yaml:"allocated_draw"`
}

type dtlPowerOutlet struct {
	Name      string `yaml:"name"`
	Type      string `yaml:"type"`
	PowerPort string `yaml:"power_port"`
	FeedLeg   string `yaml:"feed_leg"`
}

type dtlInterface struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	MgmtOnly bool   `yaml:"mgmt_only"`
}

type dtlFrontPort struct {
	Name             string `yaml:"name"`
	Type             string `yaml:"type"`
	RearPort         string `yaml:"rear_port"`
	RearPortPosition int    `yaml:"rear_port_position"`
}

type dtlRearPort struct {
	Name      string `yaml:"name"`
	Type      string `yaml:"type"`
	Positions int    `yaml:"positions"`
}

type dtlBay struct {
	Name        string `yaml:"name"`
	Label       string `yaml:"label"`
	Description string `yaml:"description"`
	Position    string `yaml:"position"`
}

// LoadDeviceTypeLibrary reads a community-format device type library rooted at
// root and returns the device types in the native model. The layout is walked
// recursively, so pointing root at a library checkout picks up every vendor
// directory. A root that does not exist is not an error: the library is
// optional, and a missing one simply contributes nothing.
func (dl *DataLoader) LoadDeviceTypeLibrary(root string) ([]*models.DeviceType, error) {
	if root == "" {
		return nil, nil
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		dl.logger.Warning("Device type library %s not found, skipping", root)
		return nil, nil
	}

	files, err := dl.findYAMLFiles(root)
	if err != nil {
		return nil, fmt.Errorf("failed to find YAML files in %s: %w", root, err)
	}
	files = dl.filterIgnored(files)
	if len(files) == 0 {
		dl.logger.Warning("No device type files found in %s", root)
		return nil, nil
	}

	deviceTypes := make([]*models.DeviceType, 0, len(files))
	// A slug is the identity a device type is reconciled under, so two files
	// claiming one slug would make the resulting NetBox state depend on the
	// order the directory happened to be walked in.
	sourceBySlug := make(map[string]string, len(files))

	for _, path := range files {
		parsed, err := dl.loadDeviceTypeLibraryFile(path)
		if err != nil {
			return nil, err
		}
		for _, dt := range parsed {
			if previous, clash := sourceBySlug[dt.Slug]; clash {
				return nil, fmt.Errorf("duplicate device type slug %q in the library: declared in both %s and %s",
					dt.Slug, previous, path)
			}
			sourceBySlug[dt.Slug] = path
			deviceTypes = append(deviceTypes, dt)
		}
	}

	dl.logger.Info("Loaded %d device type(s) from library %s", len(deviceTypes), root)
	return deviceTypes, nil
}

// loadDeviceTypeLibraryFile parses one library file into the native model. The
// library publishes a single device type per file, but a multi-document file
// is read in full rather than having everything after the first document
// silently ignored.
func (dl *DataLoader) loadDeviceTypeLibraryFile(path string) ([]*models.DeviceType, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	// KnownFields catches a library file whose schema has drifted (or a native
	// device type file dropped into the library root by mistake) instead of
	// silently importing a device type with no components.
	dec := yaml.NewDecoder(bytes.NewReader(content))
	dec.KnownFields(true)

	var deviceTypes []*models.DeviceType
	for docIndex := 1; ; docIndex++ {
		var dtl dtlDeviceType
		err := dec.Decode(&dtl)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to parse device type library file %s (document %d): %w", path, docIndex, err)
		}

		dt, err := dtl.toModel()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		// Library device types go through the same model validation as native
		// ones, so a malformed component is caught here rather than as a 400
		// from NetBox partway through a run.
		if err := dt.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		// Nothing this project cannot express is dropped quietly.
		if len(dtl.InventoryItems) > 0 {
			dl.logger.Warning("%s: %d inventory item(s) are not imported (inventory item templates are not managed)",
				path, len(dtl.InventoryItems))
		}

		deviceTypes = append(deviceTypes, dt)
	}

	// An empty or comment-only file is a placeholder, not a failure.
	if len(deviceTypes) == 0 {
		dl.logger.Warning("%s contains no device type, skipping", path)
	}

	return deviceTypes, nil
}

// toModel translates the library schema into the native device type model,
// applying the library's defaults for the fields it allows to be omitted.
func (d *dtlDeviceType) toModel() (*models.DeviceType, error) {
	if d.Manufacturer == "" {
		return nil, fmt.Errorf("manufacturer is required")
	}
	if d.Model == "" {
		return nil, fmt.Errorf("model is required")
	}

	slug := d.Slug
	if slug == "" {
		// The library allows the slug to be omitted; NetBox derives it from
		// the model, so do the same rather than rejecting the file.
		slug = utils.Slugify(d.Model)
	}

	// NetBox defaults: a device type is 1U and full depth unless stated
	// otherwise. Omitting them in a library file means "take the default",
	// not "zero".
	uHeight := 1.0
	if d.UHeight != nil {
		uHeight = *d.UHeight
	}
	isFullDepth := true
	if d.IsFullDepth != nil {
		isFullDepth = *d.IsFullDepth
	}

	dt := &models.DeviceType{
		Model:         d.Model,
		Slug:          slug,
		Manufacturer:  d.Manufacturer,
		PartNumber:    d.PartNumber,
		UHeight:       uHeight,
		IsFullDepth:   isFullDepth,
		SubdeviceRole: d.SubdeviceRole,
		Airflow:       d.Airflow,
		Description:   d.Description,
		Comments:      d.Comments,
		Weight:        d.Weight,
		WeightUnit:    d.WeightUnit,
	}

	for _, i := range d.Interfaces {
		dt.Interfaces = append(dt.Interfaces, models.InterfaceTemplate{
			Name: i.Name, Type: i.Type, MgmtOnly: i.MgmtOnly,
		})
	}
	for _, p := range d.ConsolePorts {
		dt.ConsolePorts = append(dt.ConsolePorts, models.ConsolePortTemplate{Name: p.Name, Type: p.Type})
	}
	for _, p := range d.ConsoleServerPorts {
		dt.ConsoleServerPorts = append(dt.ConsoleServerPorts, models.ConsolePortTemplate{Name: p.Name, Type: p.Type})
	}
	for _, p := range d.PowerPorts {
		dt.PowerPorts = append(dt.PowerPorts, models.PowerPortTemplate{
			Name: p.Name, Type: p.Type, MaximumDraw: p.MaximumDraw, AllocatedDraw: p.AllocatedDraw,
		})
	}
	for _, o := range d.PowerOutlets {
		dt.PowerOutlets = append(dt.PowerOutlets, models.PowerOutletTemplate{
			Name: o.Name, Type: o.Type, PowerPort: o.PowerPort, FeedLeg: o.FeedLeg,
		})
	}
	for _, p := range d.RearPorts {
		dt.RearPorts = append(dt.RearPorts, models.PortTemplate{
			Name: p.Name, Type: p.Type, Positions: p.Positions,
		})
	}
	for _, p := range d.FrontPorts {
		dt.FrontPorts = append(dt.FrontPorts, models.PortTemplate{
			Name: p.Name, Type: p.Type, RearPort: p.RearPort, RearPortPosition: p.RearPortPosition,
		})
	}
	for _, b := range d.ModuleBays {
		dt.ModuleBays = append(dt.ModuleBays, models.ModuleBayTemplate{
			Name: b.Name, Label: b.Label, Description: b.Description, Position: b.Position,
		})
	}
	for _, b := range d.DeviceBays {
		dt.DeviceBays = append(dt.DeviceBays, models.DeviceBayTemplate{
			Name: b.Name, Label: b.Label, Description: b.Description,
		})
	}

	return dt, nil
}

// MergeDeviceTypes combines natively defined device types with library ones.
// A native definition wins over a library entry with the same slug, so a
// vendored library can be customised locally without editing the checkout;
// each override is logged so the shadowing is never silent.
func MergeDeviceTypes(native, library []*models.DeviceType, logger *utils.Logger) []*models.DeviceType {
	bySlug := make(map[string]bool, len(native))
	for _, dt := range native {
		bySlug[dt.Slug] = true
	}

	merged := make([]*models.DeviceType, 0, len(native)+len(library))
	merged = append(merged, native...)
	for _, dt := range library {
		if bySlug[dt.Slug] {
			logger.Warning("Device type %q from the library is overridden by a local definition", dt.Slug)
			continue
		}
		merged = append(merged, dt)
	}
	return merged
}
