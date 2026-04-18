package lens

import (
	"fmt"
	"strings"
)

// ID is the stable machine-readable identifier for a repair lens.
type ID string

const (
	Defensive   ID = "defensive"
	Minimalist  ID = "minimalist"
	Architect   ID = "architect"
	Performance ID = "performance"
)

// Definition describes one repair perspective available to candidate runs.
type Definition struct {
	ID          ID     `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

var registry = []Definition{
	{
		ID:          Defensive,
		Name:        "Defensive",
		Description: "Prioritize robust handling of malformed inputs, edge cases, and failure isolation.",
	},
	{
		ID:          Minimalist,
		Name:        "Minimalist",
		Description: "Prefer the smallest targeted change that addresses the observed failure.",
	},
	{
		ID:          Architect,
		Name:        "Architect",
		Description: "Look for the underlying design boundary and keep the fix aligned with existing abstractions.",
	},
	{
		ID:          Performance,
		Name:        "Performance",
		Description: "Preserve or improve runtime cost, allocation behavior, and scalability while fixing the bug.",
	},
}

// All returns every registered lens in stable artifact order.
func All() []Definition {
	definitions := make([]Definition, len(registry))
	copy(definitions, registry)
	return definitions
}

// IDs returns every registered lens ID in stable artifact order.
func IDs() []ID {
	ids := make([]ID, 0, len(registry))
	for _, definition := range registry {
		ids = append(ids, definition.ID)
	}
	return ids
}

// Lookup returns the definition for id.
func Lookup(id ID) (Definition, bool) {
	for _, definition := range registry {
		if definition.ID == id {
			return definition, true
		}
	}
	return Definition{}, false
}

// ParseID normalizes and validates a lens identifier.
func ParseID(raw string) (ID, error) {
	id := ID(strings.ToLower(strings.TrimSpace(raw)))
	if id == "" {
		return "", fmt.Errorf("lens id is required")
	}
	if _, ok := Lookup(id); !ok {
		return "", fmt.Errorf("unknown lens %q", raw)
	}
	return id, nil
}
