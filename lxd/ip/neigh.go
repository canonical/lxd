package ip

import (
	"github.com/lxc/lxd/shared"
)

// Neigh represents arguments for neighbour manipulation
type Neigh struct {
	DevName string
}

// Show list neighbour entries
func (n *Neigh) Show() (string, error) {
	out, err := shared.RunCommand("ip", "neigh", "show", "dev", n.DevName)
	if err != nil {
		return "", err
	}

	return out, nil
}
