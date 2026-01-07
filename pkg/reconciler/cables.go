package reconciler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/braunma/netbox-gitops-controller/pkg/client"
	"github.com/braunma/netbox-gitops-controller/pkg/models"
	"github.com/braunma/netbox-gitops-controller/pkg/utils"
)

// CableReconciler handles cable reconciliation with full idempotency
type CableReconciler struct {
	client        *client.NetBoxClient
	logger        *utils.Logger
	processedPairs map[string]bool // Track processed cable pairs to avoid duplicates
}

// NewCableReconciler creates a new cable reconciler
func NewCableReconciler(c *client.NetBoxClient) *CableReconciler {
	return &CableReconciler{
		client:        c,
		logger:        c.Logger(),
		processedPairs: make(map[string]bool),
	}
}

// cableColorMap maps color names to hex codes (matches Python constants.py:CABLE_COLOR_MAP)
var cableColorMap = map[string]string{
	"purple": "800080",
	"blue":   "0000ff",
	"yellow": "ffff00",
	"red":    "ff0000",
	"white":  "ffffff",
	"black":  "000000",
	"gray":   "808080",
	"grey":   "808080",
	"orange": "ffa500",
	"green":  "008000",
}

// normalizeColor converts color names to hex codes and strips # prefix
// Matches Python utils.py:normalize_color (lines 44-50)
func normalizeColor(colorInput string) string {
	if colorInput == "" {
		return ""
	}

	// Convert to lowercase and trim
	raw := strings.ToLower(strings.TrimSpace(colorInput))

	// Try to map color name to hex
	if hexCode, ok := cableColorMap[raw]; ok {
		return hexCode
	}

	// Already hex format, strip # if present
	raw = strings.TrimPrefix(raw, "#")

	// Return as-is (could be hex without #, or invalid)
	return raw
}

// CableEndpoint represents one end of a cable
type CableEndpoint struct {
	DeviceName string
	PortName   string
	ObjectType string // "dcim.interface", "dcim.frontport", "dcim.rearport"
	ObjectID   int
}

// ReconcileCable reconciles a cable between two endpoints (IDEMPOTENT)
func (cr *CableReconciler) ReconcileCable(aEnd, bEnd *CableEndpoint, link *models.LinkConfig) error {
	if aEnd == nil || bEnd == nil {
		return fmt.Errorf("cable endpoints cannot be nil")
	}

	cr.logger.Debug("┌─ Cable Reconciliation ─────────────────────────")
	cr.logger.Debug("│ A-End: %s [%s] → %s (ID: %d)", aEnd.DeviceName, aEnd.PortName, aEnd.ObjectType, aEnd.ObjectID)
	cr.logger.Debug("│ B-End: %s [%s] → %s (ID: %d)", bEnd.DeviceName, bEnd.PortName, bEnd.ObjectType, bEnd.ObjectID)

	// Create a canonical pair ID (sorted to ensure A->B == B->A)
	pairID := cr.createPairID(aEnd, bEnd)

	if cr.processedPairs[pairID] {
		cr.logger.Debug("│ Status: Already processed (idempotent)")
		cr.logger.Debug("└────────────────────────────────────────────────")
		return nil
	}

	// Mark as processed
	cr.processedPairs[pairID] = true

	// Check if cable already exists
	existing, err := cr.findExistingCable(aEnd, bEnd)
	if err != nil {
		return fmt.Errorf("failed to check existing cable: %w", err)
	}

	if existing != nil {
		cr.logger.Debug("│ Status: Cable exists (ID: %v)", existing["id"])

		// Verify the cable is correct
		if cr.verifyCable(existing, aEnd, bEnd, link) {
			cr.logger.Debug("│ Action: No changes needed")
			cr.logger.Debug("└────────────────────────────────────────────────")
			return nil
		}

		// Update cable if needed
		cr.logger.Info("│ Action: Updating cable configuration")
		if err := cr.updateCable(existing, link); err != nil {
			return fmt.Errorf("failed to update cable: %w", err)
		}
		cr.logger.Success("│ Result: Cable updated successfully")
	} else {
		// No existing cable found between A and B
		cr.logger.Debug("│ No existing cable found")

		// CRITICAL: Check local port (A-end) for existing cables FIRST
		// Python device_controller.py lines 587-605 (Section D)
		cr.logger.Debug("│ Checking local port for existing cables...")
		skipCreation, err := cr.checkAndCleanLocalPort(aEnd, bEnd)
		if err != nil {
			return fmt.Errorf("failed to check local port: %w", err)
		}

		if skipCreation {
			// Local port already has correct cable - idempotent
			cr.logger.Debug("└────────────────────────────────────────────────")
			return nil
		}

		// CRITICAL: Check peer port (B-end) for existing cables
		// Python device_controller.py lines 607-639 (Section E)
		cr.logger.Debug("│ Checking peer port for existing cables...")

		skipCreation, err = cr.checkAndCleanPeerPort(aEnd, bEnd, link)
		if err != nil {
			return fmt.Errorf("failed to check peer port: %w", err)
		}

		if skipCreation {
			// Peer already has correct cable - idempotent, no action needed
			cr.logger.Debug("└────────────────────────────────────────────────")
			return nil
		}

		// Create new cable
		cr.logger.Info("│ Action: Creating new cable")
		if err := cr.createCable(aEnd, bEnd, link); err != nil {
			return fmt.Errorf("failed to create cable: %w", err)
		}
		cr.logger.Success("│ Result: Cable created successfully")
	}

	cr.logger.Debug("└────────────────────────────────────────────────")
	return nil
}

// createPairID creates a canonical identifier for a cable pair (order-independent)
func (cr *CableReconciler) createPairID(aEnd, bEnd *CableEndpoint) string {
	// Create stable IDs for both ends
	aID := fmt.Sprintf("%s:%s:%d", aEnd.ObjectType, aEnd.DeviceName, aEnd.ObjectID)
	bID := fmt.Sprintf("%s:%s:%d", bEnd.ObjectType, bEnd.DeviceName, bEnd.ObjectID)

	// Sort to ensure A->B == B->A
	ids := []string{aID, bID}
	sort.Strings(ids)

	return fmt.Sprintf("%s <-> %s", ids[0], ids[1])
}

// findExistingCable searches for an existing cable between two endpoints
func (cr *CableReconciler) findExistingCable(aEnd, bEnd *CableEndpoint) (client.Object, error) {
	cr.logger.Debug("│ Searching for existing cable...")

	// Try both directions since cables are bidirectional
	cables, err := cr.client.Filter("dcim", "cables", map[string]interface{}{
		"termination_a_type": aEnd.ObjectType,
		"termination_a_id":   aEnd.ObjectID,
	})
	if err != nil {
		return nil, err
	}

	for _, cable := range cables {
		// Check if the B end matches
		if cr.matchesEndpoint(cable, "b", bEnd) {
			cr.logger.Debug("│ Found existing cable: ID %v", cable["id"])
			return cable, nil
		}
	}

	// Try reverse direction
	cables, err = cr.client.Filter("dcim", "cables", map[string]interface{}{
		"termination_a_type": bEnd.ObjectType,
		"termination_a_id":   bEnd.ObjectID,
	})
	if err != nil {
		return nil, err
	}

	for _, cable := range cables {
		// Check if the B end matches
		if cr.matchesEndpoint(cable, "b", aEnd) {
			cr.logger.Debug("│ Found existing cable (reversed): ID %v", cable["id"])
			return cable, nil
		}
	}

	cr.logger.Debug("│ No existing cable found")
	return nil, nil
}

// matchesEndpoint checks if a cable endpoint matches the given endpoint
func (cr *CableReconciler) matchesEndpoint(cable client.Object, side string, endpoint *CableEndpoint) bool {
	typeKey := fmt.Sprintf("termination_%s_type", side)
	idKey := fmt.Sprintf("termination_%s_id", side)

	cableType, _ := cable[typeKey].(string)

	// Handle nested ID structure
	var cableID int
	if idVal, ok := cable[idKey].(float64); ok {
		cableID = int(idVal)
	} else if idMap, ok := cable[idKey].(map[string]interface{}); ok {
		if id, ok := idMap["id"].(float64); ok {
			cableID = int(id)
		}
	}

	return cableType == endpoint.ObjectType && cableID == endpoint.ObjectID
}

// verifyCable checks if an existing cable matches the desired configuration
func (cr *CableReconciler) verifyCable(cable client.Object, aEnd, bEnd *CableEndpoint, link *models.LinkConfig) bool {
	if link == nil {
		return true // No specific config to verify
	}

	// Check cable type
	if link.CableType != "" {
		if cableType, ok := cable["type"].(string); ok {
			if cableType != link.CableType {
				cr.logger.Debug("│ Cable type mismatch: %s != %s", cableType, link.CableType)
				return false
			}
		}
	}

	// Check color (normalize color name to hex for comparison)
	if link.Color != "" {
		normalizedColor := normalizeColor(link.Color)
		if color, ok := cable["color"].(string); ok {
			// NetBox stores color without # prefix
			if color != normalizedColor {
				cr.logger.Debug("│ Cable color mismatch: %s != %s (normalized: %s)", color, link.Color, normalizedColor)
				return false
			}
		}
	}

	// Check length
	if link.Length > 0 {
		if length, ok := cable["length"].(float64); ok {
			if length != link.Length {
				cr.logger.Debug("│ Cable length mismatch: %f != %f", length, link.Length)
				return false
			}
		}
	}

	return true
}

// createCable creates a new cable
func (cr *CableReconciler) createCable(aEnd, bEnd *CableEndpoint, link *models.LinkConfig) error {
	payload := map[string]interface{}{
		"a_terminations": []map[string]interface{}{
			{
				"object_type": aEnd.ObjectType,
				"object_id":   aEnd.ObjectID,
			},
		},
		"b_terminations": []map[string]interface{}{
			{
				"object_type": bEnd.ObjectType,
				"object_id":   bEnd.ObjectID,
			},
		},
		"status": "connected",
	}

	if link != nil {
		if link.CableType != "" {
			payload["type"] = link.CableType
			cr.logger.Debug("│   Type: %s", link.CableType)
		}
		if link.Color != "" {
			normalizedColor := normalizeColor(link.Color)
			payload["color"] = normalizedColor
			cr.logger.Debug("│   Color: %s (normalized: %s)", link.Color, normalizedColor)
		}
		if link.Length > 0 {
			payload["length"] = link.Length
			cr.logger.Debug("│   Length: %.2f %s", link.Length, link.LengthUnit)
		}
		if link.LengthUnit != "" {
			payload["length_unit"] = link.LengthUnit
		}
	}

	if cr.client.IsDryRun() {
		cr.logger.DryRun("CREATE", "Cable: %s[%s] <-> %s[%s]",
			aEnd.DeviceName, aEnd.PortName, bEnd.DeviceName, bEnd.PortName)
		return nil
	}

	_, err := cr.client.Create("dcim", "cables", payload)
	return err
}

// updateCable updates an existing cable
func (cr *CableReconciler) updateCable(cable client.Object, link *models.LinkConfig) error {
	if link == nil {
		return nil
	}

	cableID := utils.GetIDFromObject(cable)
	if cableID == 0 {
		return fmt.Errorf("cable has no ID")
	}

	updates := make(map[string]interface{})

	if link.CableType != "" {
		updates["type"] = link.CableType
	}
	if link.Color != "" {
		updates["color"] = normalizeColor(link.Color)
	}
	if link.Length > 0 {
		updates["length"] = link.Length
	}
	if link.LengthUnit != "" {
		updates["length_unit"] = link.LengthUnit
	}

	if len(updates) == 0 {
		return nil
	}

	if cr.client.IsDryRun() {
		cr.logger.DryRun("UPDATE", "Cable ID %d with %v", cableID, updates)
		return nil
	}

	return cr.client.Update("dcim", "cables", cableID, updates)
}

// checkAndCleanLocalPort checks if the local port (A-end) already has a cable
// Matches Python device_controller.py lines 587-605 (Section D)
// Returns (skipCreation, error) - skipCreation=true means cable already exists correctly
func (cr *CableReconciler) checkAndCleanLocalPort(aEnd, bEnd *CableEndpoint) (bool, error) {
	// Determine the endpoint type for A-end
	var endpoint string
	switch aEnd.ObjectType {
	case "dcim.interface":
		endpoint = "interfaces"
	case "dcim.frontport":
		endpoint = "front-ports"
	case "dcim.rearport":
		endpoint = "rear-ports"
	default:
		return false, fmt.Errorf("unknown endpoint type: %s", aEnd.ObjectType)
	}

	// Fetch the local port (A-end) to check if it has a cable
	localPorts, err := cr.client.Filter("dcim", endpoint, map[string]interface{}{
		"id": aEnd.ObjectID,
	})
	if err != nil || len(localPorts) == 0 {
		cr.logger.Debug("│ Could not fetch local port (may be OK): %v", err)
		return false, nil
	}

	localPort := localPorts[0]

	// Check if local port has an existing cable
	cableRef, hasCable := localPort["cable"]
	if !hasCable || cableRef == nil {
		cr.logger.Debug("│ Local port has no existing cable")
		return false, nil
	}

	// Extract cable ID from the reference
	var cableID int
	switch v := cableRef.(type) {
	case float64:
		cableID = int(v)
	case map[string]interface{}:
		if id, ok := v["id"].(float64); ok {
			cableID = int(id)
		}
	}

	if cableID == 0 {
		cr.logger.Debug("│ Local port cable reference is invalid")
		return false, nil
	}

	// Fetch the existing cable on the local port
	existingCable, err := cr.client.Get("dcim", "cables", cableID)
	if err != nil || existingCable == nil {
		cr.logger.Info("│ Existing cable vanished during fetch (ID: %d) - skipping idempotency check", cableID)
		return false, nil
	}

	cr.logger.Debug("│ Local port already has cable ID: %d", cableID)

	// Check if this cable connects to our B-end (correct cable - idempotent case)
	// Python: if self._cable_connects_to(existing, peer.id)
	if cr.cableConnectsTo(existingCable, bEnd.ObjectID) {
		cr.logger.Info("│ Local port already has correct cable (ID: %d)", cableID)
		cr.logger.Info("│ Action: No changes needed (idempotent)")
		return true, nil
	}

	// The local port has a cable to a DIFFERENT device - delete it
	// Python: self._safe_delete(existing, "wrong peer connection", force=True)
	cr.logger.Warning("│ Local port has cable to DIFFERENT device")
	cr.logger.Warning("│ Existing cable ID %d blocks our connection", cableID)
	cr.logger.Info("│ Deleting wrong cable on local port ID %d (forced)", cableID)

	// Delete the cable (matches Python force=True behavior - no managed check)
	if !cr.client.IsDryRun() {
		if err := cr.client.Delete("dcim", "cables", cableID); err != nil {
			return false, fmt.Errorf("failed to delete wrong cable on local port: %w", err)
		}
		cr.logger.Success("│ Deleted wrong cable on local port")
	} else {
		cr.logger.DryRun("DELETE", "Wrong cable ID %d on local port", cableID)
	}

	return false, nil
}

// checkAndCleanPeerPort checks if the peer port (B-end) already has a cable
// Returns (skipCreation, error) - skipCreation=true means cable already exists correctly
// Matches Python device_controller.py lines 607-639: "Peer-Port prüfen (Stray cables)"
func (cr *CableReconciler) checkAndCleanPeerPort(aEnd, bEnd *CableEndpoint, link *models.LinkConfig) (bool, error) {
	// Determine the endpoint type to query
	var endpoint string
	switch bEnd.ObjectType {
	case "dcim.interface":
		endpoint = "interfaces"
	case "dcim.frontport":
		endpoint = "front-ports"
	case "dcim.rearport":
		endpoint = "rear-ports"
	default:
		return false, fmt.Errorf("unknown endpoint type: %s", bEnd.ObjectType)
	}

	// Fetch the fresh peer port object to check if it has a cable
	peerPorts, err := cr.client.Filter("dcim", endpoint, map[string]interface{}{
		"id": bEnd.ObjectID,
	})
	if err != nil || len(peerPorts) == 0 {
		// If we can't find the port, proceed anyway - it might be a timing issue
		cr.logger.Debug("│ Could not fetch peer port (may be OK): %v", err)
		return false, nil
	}

	peerPort := peerPorts[0]

	// Check if peer port has an existing cable
	cableRef, hasCable := peerPort["cable"]
	if !hasCable || cableRef == nil {
		cr.logger.Debug("│ Peer port has no existing cable")
		return false, nil
	}

	// Extract cable ID from the reference
	var cableID int
	switch v := cableRef.(type) {
	case float64:
		cableID = int(v)
	case map[string]interface{}:
		if id, ok := v["id"].(float64); ok {
			cableID = int(id)
		}
	}

	if cableID == 0 {
		cr.logger.Debug("│ Peer port cable reference is invalid")
		return false, nil
	}

	// Fetch the existing cable on the peer port
	existingCable, err := cr.client.Get("dcim", "cables", cableID)
	if err != nil || existingCable == nil {
		cr.logger.Debug("│ Could not fetch peer cable ID %d: %v", cableID, err)
		return false, nil
	}

	cr.logger.Debug("│ Peer port already has cable ID: %d", cableID)

	// Check if this cable connects to our A-end (correct cable - idempotent case)
	if cr.cableConnectsTo(existingCable, aEnd.ObjectID) {
		cr.logger.Info("│ Peer port already has correct cable (ID: %d)", cableID)
		cr.logger.Info("│ Action: No changes needed (idempotent)")
		// This is OK - the cable already exists correctly, skip creation
		return true, nil
	}

	// The peer has a cable to a DIFFERENT device - this is a conflict
	cr.logger.Warning("│ Peer port has cable to DIFFERENT device")
	cr.logger.Warning("│ Existing cable ID %d blocks our connection", cableID)

	// Python device_controller.py lines 628-637:
	// Special handling for backbone cables (rearport to rearport between patch panels)
	// vs. regular blocking cables - but BOTH use force=True to skip managed check

	// Check if this is a backbone cable scenario (B-end is rearport to patch panel)
	if bEnd.ObjectType == "dcim.rearport" {
		// This would be a patch panel backbone cable
		// Python checks: if term_b_type == "dcim.rearport" and is_dst_pp
		// If the existing cable doesn't connect to our A-end, it's the wrong backbone
		cr.logger.Info("│ Deleting wrong backbone cable ID %d (forced)", cableID)
	} else {
		// Regular blocking cable on frontport or interface
		cr.logger.Info("│ Deleting blocking cable ID %d (forced)", cableID)
	}

	// Delete the cable (matches Python force=True behavior - no managed check)
	// Python: self._safe_delete(peer_cable, reason, force=True)
	// With force=True, Python skips the is_managed_by_gitops check (line 67)
	if !cr.client.IsDryRun() {
		if err := cr.client.Delete("dcim", "cables", cableID); err != nil {
			return false, fmt.Errorf("failed to delete blocking cable: %w", err)
		}
		cr.logger.Success("│ Deleted blocking cable")
	} else {
		cr.logger.DryRun("DELETE", "Blocking cable ID %d", cableID)
	}

	return false, nil
}

// cableConnectsTo checks if a cable has a termination connecting to the specified object ID
// Matches Python _cable_connects_to helper
func (cr *CableReconciler) cableConnectsTo(cable client.Object, targetObjectID int) bool {
	// Check A terminations
	if aTerms, ok := cable["a_terminations"].([]interface{}); ok {
		for _, term := range aTerms {
			if termMap, ok := term.(map[string]interface{}); ok {
				if objID, ok := termMap["object_id"].(float64); ok && int(objID) == targetObjectID {
					return true
				}
			}
		}
	}

	// Check B terminations
	if bTerms, ok := cable["b_terminations"].([]interface{}); ok {
		for _, term := range bTerms {
			if termMap, ok := term.(map[string]interface{}); ok {
				if objID, ok := termMap["object_id"].(float64); ok && int(objID) == targetObjectID {
					return true
				}
			}
		}
	}

	return false
}

// Reset clears the processed pairs cache (call between reconciliation runs)
func (cr *CableReconciler) Reset() {
	cr.processedPairs = make(map[string]bool)
	cr.logger.Debug("Cable reconciler state reset")
}
