package reconciler

import (
	"fmt"
	"sort"

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

	// Check color
	if link.Color != "" {
		if color, ok := cable["color"].(string); ok {
			if color != link.Color {
				cr.logger.Debug("│ Cable color mismatch: %s != %s", color, link.Color)
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
			payload["color"] = link.Color
			cr.logger.Debug("│   Color: %s", link.Color)
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
		updates["color"] = link.Color
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

// Reset clears the processed pairs cache (call between reconciliation runs)
func (cr *CableReconciler) Reset() {
	cr.processedPairs = make(map[string]bool)
	cr.logger.Debug("Cable reconciler state reset")
}
