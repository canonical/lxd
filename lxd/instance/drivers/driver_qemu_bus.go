package drivers

import (
	"fmt"
	"strings"
)

type qemuBusEntry struct {
	bridgeDev int // Device number on the root bridge.
	bridgeFn  int // Function number on the root bridge.

	dev string // Existing device name.
	fn  int    // Function number on the existing device.
}

type qemuBus struct {
	name string           // Bus type.
	sb   *strings.Builder // String builder to use.

	portNum int // Next available port/chassis on the bridge.
	devNum  int // Next available device number on the bridge.

	rootPort *qemuBusEntry // Current root port.

	entries map[string]*qemuBusEntry // Map of qemuBusEntry for a particular shared device.
}

func (a *qemuBus) allocateRoot() *qemuBusEntry {
	if a.rootPort == nil {
		a.rootPort = &qemuBusEntry{
			bridgeDev: a.devNum,
		}
		a.devNum++
	} else {
		if a.rootPort.bridgeFn == 7 {
			a.rootPort.bridgeFn = 0
			a.rootPort.bridgeDev = a.devNum
			a.devNum++
		} else {
			a.rootPort.bridgeFn++
		}
	}

	return a.rootPort
}

// allocate() does any needed port allocation and  returns the bus name,
// address and whether the device needs to be configured as multi-function.
//
// The group parameter allows for grouping devices together as a single
// multi-function device. It automatically keeps track of the number of
// functions already used and will allocate a new device as needed.
func (a *qemuBus) allocate(group string) (string, string, bool) {
	if a.name == "ccw" {
		return "", "", false
	}

	// Find a device group if specified.
	var p *qemuBusEntry
	if group != "" {
		var ok bool
		p, ok = a.entries[group]
		if ok {
			// Check if group is full.
			if p.fn == 7 {
				p.fn = 0
				if a.name == "pci" {
					p.bridgeDev = a.devNum
					a.devNum++
				} else if a.name == "pcie" {
					r := a.allocateRoot()
					p.bridgeDev = r.bridgeDev
					p.bridgeFn = r.bridgeFn
				}
			} else {
				p.fn++
			}
		} else {
			// Create a new group.
			p = &qemuBusEntry{}

			if a.name == "pci" {
				p.bridgeDev = a.devNum
				a.devNum++
			} else if a.name == "pcie" {
				r := a.allocateRoot()
				p.bridgeDev = r.bridgeDev
				p.bridgeFn = r.bridgeFn
			}

			a.entries[group] = p
		}
	} else {
		// Create a new temporary group.
		p = &qemuBusEntry{}

		if a.name == "pci" {
			p.bridgeDev = a.devNum
			a.devNum++
		} else if a.name == "pcie" {
			r := a.allocateRoot()
			p.bridgeDev = r.bridgeDev
			p.bridgeFn = r.bridgeFn
		}
	}

	multi := p.fn == 0 && group != ""

	if a.name == "pci" {
		return "pci.0", fmt.Sprintf("%x.%d", p.bridgeDev, p.fn), multi
	}

	if a.name == "pcie" {
		if p.fn == 0 {
			qemuPCIe.Execute(a.sb, map[string]interface{}{
				"index":         a.portNum,
				"addr":          fmt.Sprintf("%x.%d", p.bridgeDev, p.bridgeFn),
				"multifunction": p.bridgeFn == 0,
			})
			p.dev = fmt.Sprintf("qemu_pcie%d", a.portNum)
			a.portNum++
		}

		return p.dev, fmt.Sprintf("00.%d", p.fn), multi
	}

	return "", "", false
}

func qemuNewBus(name string, sb *strings.Builder) *qemuBus {
	a := &qemuBus{
		name: name,
		sb:   sb,

		portNum: 0, // No PCIe ports are used in the default config.
		devNum:  1, // Address 0 is used by the DRAM controller.

		entries: map[string]*qemuBusEntry{},
	}

	return a
}
