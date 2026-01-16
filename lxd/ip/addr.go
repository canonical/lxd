package ip

import (
	"context"

	"github.com/canonical/lxd/shared"
)

// Addr represents arguments for address protocol manipulation.
type Addr struct {
	DevName string
	Address string
	Scope   string
	Family  string
}

// Add adds new protocol address.
func (a *Addr) Add() error {
	_, err := shared.RunCommand(context.TODO(), "ip", a.Family, "addr", "add", "dev", a.DevName, a.Address)
	if err != nil {
		return err
	}

	return nil
}

// Flush flushes protocol addresses.
func (a *Addr) Flush() error {
	cmd := []string{}
	if a.Family != "" {
		cmd = append(cmd, a.Family)
	}

	cmd = append(cmd, "addr", "flush", "dev", a.DevName)
	if a.Scope != "" {
		cmd = append(cmd, "scope", a.Scope)
	}

	_, err := shared.RunCommand(context.TODO(), "ip", cmd...)
	if err != nil {
		return err
	}

	return nil
}
