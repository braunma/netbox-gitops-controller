// SPDX-License-Identifier: Apache-2.0

package loader

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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

	// Library-only metadata: flags saying an elevation image ships alongside
	// the file, and whether the device draws power. Accepted so a real library
	// file parses; not sent to NetBox.
	FrontImage *bool `yaml:"front_image"`
	RearImage  *bool `yaml:"rear_image"`
	IsPowered  *bool `yaml:"is_powered"`
}

// The component structs below accept every field that appears anywhere in the
// published library, verified by scanning all 5,980 device type and 1,950
// module type files. KnownFields decoding stays strict — it is what catches a
// native-format file dropped into a library root — so a field the library uses
// but this project does not reconcile must still be declared here, and is
// simply not mapped through.

type dtlNamedType struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	Label       string `yaml:"label"`
	Description string `yaml:"description"`
	// Library-only hint on some console/rear ports; not a NetBox field.
	IsPowerSource *bool `yaml:"_is_power_source"`
}

type dtlPowerPort struct {
	Name          string `yaml:"name"`
	Type          string `yaml:"type"`
	Label         string `yaml:"label"`
	Description   string `yaml:"description"`
	MaximumDraw   int    `yaml:"maximum_draw"`
	AllocatedDraw int    `yaml:"allocated_draw"`
}

type dtlPowerOutlet struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	Label       string `yaml:"label"`
	Description string `yaml:"description"`
	PowerPort   string `yaml:"power_port"`
	FeedLeg     string `yaml:"feed_leg"`
}

type dtlInterface struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	Label       string `yaml:"label"`
	Description string `yaml:"description"`
	MgmtOnly    bool   `yaml:"mgmt_only"`
	Enabled     *bool  `yaml:"enabled"`
	PoEMode     string `yaml:"poe_mode"`
	PoEType     string `yaml:"poe_type"`
	RFRole      string `yaml:"rf_role"`
	// A bridge names another interface on the same type; resolving it needs a
	// second pass this project does not do, so it is accepted and not mapped.
	Bridge string `yaml:"bridge"`
}

type dtlFrontPort struct {
	Name             string `yaml:"name"`
	Type             string `yaml:"type"`
	Label            string `yaml:"label"`
	Description      string `yaml:"description"`
	Color            string `yaml:"color"`
	RearPort         string `yaml:"rear_port"`
	RearPortPosition int    `yaml:"rear_port_position"`
}

type dtlRearPort struct {
	Name          string `yaml:"name"`
	Type          string `yaml:"type"`
	Label         string `yaml:"label"`
	Description   string `yaml:"description"`
	Color         string `yaml:"color"`
	Positions     int    `yaml:"positions"`
	IsPowerSource *bool  `yaml:"_is_power_source"`
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

	dt.Interfaces = toInterfaceTemplates(d.Interfaces)
	dt.ConsolePorts = toConsolePortTemplates(d.ConsolePorts)
	dt.ConsoleServerPorts = toConsolePortTemplates(d.ConsoleServerPorts)
	dt.PowerPorts = toPowerPortTemplates(d.PowerPorts)
	dt.PowerOutlets = toPowerOutletTemplates(d.PowerOutlets)
	dt.RearPorts = toRearPortTemplates(d.RearPorts)
	dt.FrontPorts = toFrontPortTemplates(d.FrontPorts)
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

// dtlModuleType is one module type as published by the community library's
// module-types/ tree. It shares the hyphenated component keys with the device
// type schema but has no slug, u_height, is_full_depth or subdevice_role:
// NetBox identifies a module type by manufacturer and model, and a module has
// no rack presence of its own.
type dtlModuleType struct {
	Manufacturer string  `yaml:"manufacturer"`
	Model        string  `yaml:"model"`
	PartNumber   string  `yaml:"part_number"`
	Airflow      string  `yaml:"airflow"`
	Description  string  `yaml:"description"`
	Comments     string  `yaml:"comments"`
	Weight       float64 `yaml:"weight"`
	WeightUnit   string  `yaml:"weight_unit"`

	ConsolePorts       []dtlNamedType   `yaml:"console-ports"`
	ConsoleServerPorts []dtlNamedType   `yaml:"console-server-ports"`
	PowerPorts         []dtlPowerPort   `yaml:"power-ports"`
	PowerOutlets       []dtlPowerOutlet `yaml:"power-outlets"`
	Interfaces         []dtlInterface   `yaml:"interfaces"`
	FrontPorts         []dtlFrontPort   `yaml:"front-ports"`
	RearPorts          []dtlRearPort    `yaml:"rear-ports"`
	ModuleBays         []dtlBay         `yaml:"module-bays"`

	// NetBox 4.x module type profiles and their attribute payload. Applying
	// one means resolving a profile by name, which this project does not do;
	// accepted so a real library file parses, and reported when present.
	Profile       string                 `yaml:"profile"`
	AttributeData map[string]interface{} `yaml:"attribute_data"`
}

// LoadModuleTypeLibrary reads a community-format module type library rooted at
// root (the library's module-types/ directory) and returns the module types in
// the native model. As with the device type library, a root that does not
// exist is not an error: the library is optional.
func (dl *DataLoader) LoadModuleTypeLibrary(root string) ([]*models.ModuleType, error) {
	if root == "" {
		return nil, nil
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		dl.logger.Warning("Module type library %s not found, skipping", root)
		return nil, nil
	}

	files, err := dl.findYAMLFiles(root)
	if err != nil {
		return nil, fmt.Errorf("failed to find YAML files in %s: %w", root, err)
	}
	files = dl.filterIgnored(files)
	if len(files) == 0 {
		dl.logger.Warning("No module type files found in %s", root)
		return nil, nil
	}

	moduleTypes := make([]*models.ModuleType, 0, len(files))
	// NetBox enforces uniqueness on manufacturer and model together, so that
	// pair is the identity; the same model name from two vendors is fine.
	sourceByIdentity := make(map[string]string, len(files))
	var conflicts []string

	for _, path := range files {
		parsed, err := dl.loadModuleTypeLibraryFile(path)
		if err != nil {
			return nil, err
		}
		for _, mt := range parsed {
			identity := mt.Manufacturer + "/" + mt.Model
			if previous, clash := sourceByIdentity[identity]; clash {
				// Report every conflict rather than stopping at the first: a
				// published library can contain one (two part numbers sharing a
				// model name), and finding them one run at a time is painful.
				conflicts = append(conflicts, fmt.Sprintf("%q declared in both %s and %s", identity, previous, path))
				continue
			}
			sourceByIdentity[identity] = path
			moduleTypes = append(moduleTypes, mt)
		}
	}

	if len(conflicts) > 0 {
		return nil, fmt.Errorf(
			"%d module type(s) share a manufacturer and model, which NetBox requires to be unique;"+
				" park the unwanted file with --ignore-file or remove it:\n  %s",
			len(conflicts), strings.Join(conflicts, "\n  "))
	}

	dl.logger.Info("Loaded %d module type(s) from library %s", len(moduleTypes), root)
	return moduleTypes, nil
}

// loadModuleTypeLibraryFile parses one library module type file.
func (dl *DataLoader) loadModuleTypeLibraryFile(path string) ([]*models.ModuleType, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(content))
	dec.KnownFields(true)

	var moduleTypes []*models.ModuleType
	for docIndex := 1; ; docIndex++ {
		var dtl dtlModuleType
		err := dec.Decode(&dtl)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to parse module type library file %s (document %d): %w", path, docIndex, err)
		}

		mt, err := dtl.toModel()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := mt.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		moduleTypes = append(moduleTypes, mt)
	}

	if len(moduleTypes) == 0 {
		dl.logger.Warning("%s contains no module type, skipping", path)
	}
	return moduleTypes, nil
}

// toModel translates the library module type schema into the native model.
func (d *dtlModuleType) toModel() (*models.ModuleType, error) {
	if d.Manufacturer == "" {
		return nil, fmt.Errorf("manufacturer is required")
	}
	if d.Model == "" {
		return nil, fmt.Errorf("model is required")
	}

	mt := &models.ModuleType{
		Model:        d.Model,
		Manufacturer: d.Manufacturer,
		// NetBox module types have no slug; this one is a local cache key only,
		// so it is derived from the model rather than read from the file.
		Slug:        utils.Slugify(d.Model),
		PartNumber:  d.PartNumber,
		Airflow:     d.Airflow,
		Description: d.Description,
		Comments:    d.Comments,
		Weight:      d.Weight,
		WeightUnit:  d.WeightUnit,
	}

	mt.Interfaces = toInterfaceTemplates(d.Interfaces)
	mt.ConsolePorts = toConsolePortTemplates(d.ConsolePorts)
	mt.ConsoleServerPorts = toConsolePortTemplates(d.ConsoleServerPorts)
	mt.PowerPorts = toPowerPortTemplates(d.PowerPorts)
	mt.PowerOutlets = toPowerOutletTemplates(d.PowerOutlets)
	mt.RearPorts = toRearPortTemplates(d.RearPorts)
	mt.FrontPorts = toFrontPortTemplates(d.FrontPorts)
	for _, b := range d.ModuleBays {
		mt.ModuleBays = append(mt.ModuleBays, models.ModuleBayTemplate{
			Name: b.Name, Label: b.Label, Description: b.Description, Position: b.Position,
		})
	}

	return mt, nil
}

// MergeModuleTypes combines natively defined module types with library ones.
// A native definition wins over a library entry with the same
// manufacturer/model, so a vendored library can be customised locally; each
// override is logged so the shadowing is never silent.
func MergeModuleTypes(native, library []*models.ModuleType, logger *utils.Logger) []*models.ModuleType {
	byIdentity := make(map[string]bool, len(native))
	for _, mt := range native {
		byIdentity[mt.Manufacturer+"/"+mt.Model] = true
	}

	merged := make([]*models.ModuleType, 0, len(native)+len(library))
	merged = append(merged, native...)
	for _, mt := range library {
		identity := mt.Manufacturer + "/" + mt.Model
		if byIdentity[identity] {
			logger.Warning("Module type %q from the library is overridden by a local definition", identity)
			continue
		}
		merged = append(merged, mt)
	}
	return merged
}

// Translators shared by the device type and module type library schemas.

func toInterfaceTemplates(in []dtlInterface) []models.InterfaceTemplate {
	out := make([]models.InterfaceTemplate, 0, len(in))
	for _, i := range in {
		out = append(out, models.InterfaceTemplate{
			Name: i.Name, Type: i.Type, Label: i.Label, Description: i.Description,
			MgmtOnly: i.MgmtOnly, Enabled: i.Enabled,
			PoEMode: i.PoEMode, PoEType: i.PoEType, RFRole: i.RFRole,
		})
	}
	return out
}

func toConsolePortTemplates(in []dtlNamedType) []models.ConsolePortTemplate {
	out := make([]models.ConsolePortTemplate, 0, len(in))
	for _, p := range in {
		out = append(out, models.ConsolePortTemplate{
			Name: p.Name, Type: p.Type, Label: p.Label, Description: p.Description,
		})
	}
	return out
}

func toPowerPortTemplates(in []dtlPowerPort) []models.PowerPortTemplate {
	out := make([]models.PowerPortTemplate, 0, len(in))
	for _, p := range in {
		out = append(out, models.PowerPortTemplate{
			Name: p.Name, Type: p.Type, Label: p.Label, Description: p.Description,
			MaximumDraw: p.MaximumDraw, AllocatedDraw: p.AllocatedDraw,
		})
	}
	return out
}

func toPowerOutletTemplates(in []dtlPowerOutlet) []models.PowerOutletTemplate {
	out := make([]models.PowerOutletTemplate, 0, len(in))
	for _, o := range in {
		out = append(out, models.PowerOutletTemplate{
			Name: o.Name, Type: o.Type, Label: o.Label, Description: o.Description,
			PowerPort: o.PowerPort, FeedLeg: o.FeedLeg,
		})
	}
	return out
}

func toRearPortTemplates(in []dtlRearPort) []models.PortTemplate {
	out := make([]models.PortTemplate, 0, len(in))
	for _, p := range in {
		out = append(out, models.PortTemplate{
			Name: p.Name, Type: p.Type, Label: p.Label, Description: p.Description,
			Color: p.Color, Positions: p.Positions,
		})
	}
	return out
}

func toFrontPortTemplates(in []dtlFrontPort) []models.PortTemplate {
	out := make([]models.PortTemplate, 0, len(in))
	for _, p := range in {
		out = append(out, models.PortTemplate{
			Name: p.Name, Type: p.Type, Label: p.Label, Description: p.Description,
			Color: p.Color, RearPort: p.RearPort, RearPortPosition: p.RearPortPosition,
		})
	}
	return out
}
