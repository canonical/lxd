package auth

//go:generate go run ./generate/main.go

import (
	"fmt"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/entity"
)

// ValidateEntitlement returns an error if the given Entitlement does not apply to the entity.Type.
func ValidateEntitlement(entityType entity.Type, entitlement Entitlement) error {
	entitlements := EntitlementsByEntityType(entityType)
	if len(entitlements) == 0 {
		return fmt.Errorf("No entitlements can be granted against entities of type %q", entityType)
	}

	if !shared.ValueInSlice(entitlement, entitlements) {
		return fmt.Errorf("Entitlement %q not valid for entity type %q", entitlement, entityType)
	}

	return nil
}

// EntitlementsByEntityType returns a list of available Entitlement for the entity.Type.
func EntitlementsByEntityType(entityType entity.Type) []Entitlement {
	return entityTypeToEntitlements[entityType]
}
