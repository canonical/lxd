package connectors

import (
	"slices"
	"strings"

	"github.com/canonical/lxd/shared"
)

// Target represent a connection target.
type Target struct {
	QualifiedName string
	Address       string
}

// CompareTargets compares two provided targets.
func CompareTargets(x, y Target) int {
	cmp := strings.Compare(x.QualifiedName, y.QualifiedName)

	if cmp == 0 {
		cmp = strings.Compare(x.Address, y.Address)
	}

	return cmp
}

// MatchTargetAddresses filters the provided targets to only ones that
// addresses match the provided ones. The address compassion is done according
// to rules of the specified connector type.
func MatchTargetAddresses(connectorType ConnectorType, targets []Target, expectedAddress ...string) []Target {
	switch connectorType {
	case TypeISCSI:
		expectedAddress = slices.Clone(expectedAddress)
		for i := range expectedAddress {
			expectedAddress[i] = shared.EnsurePort(expectedAddress[i], iscsiDefaultPort)
		}

	case TypeNVME:
		expectedAddress = slices.Clone(expectedAddress)
		for i := range expectedAddress {
			expectedAddress[i] = shared.EnsurePort(expectedAddress[i], nvmeDefaultTransportPort)
		}
	}

	filtered := make([]Target, 0, len(targets))
	for _, target := range targets {
		if !slices.Contains(expectedAddress, target.Address) {
			continue
		}

		filtered = append(filtered, target)
	}

	return filtered
}

// targetsQualifiedNames returns list of all unique qualified names among
// the provided targets.
func targetsQualifiedNames(targets ...Target) []string {
	qns := make([]string, 0, len(targets))
	for _, target := range targets {
		qns = append(qns, target.QualifiedName)
	}

	return shared.Unique(qns)
}

// targetsAddresses returns list of all unique addresses among the provided
// targets.
func targetsAddresses(targets ...Target) []string {
	addresses := make([]string, 0, len(targets))
	for _, target := range targets {
		addresses = append(addresses, target.Address)
	}

	return shared.Unique(addresses)
}
