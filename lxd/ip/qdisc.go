package ip

import (
	"github.com/canonical/lxd/shared"
)

// Qdisc represents 'queueing discipline' object.
type Qdisc struct {
	Dev     string
	Handle  string
	Root    bool
	Ingress bool
}

// mainCmd generates command line arguments for configuring a queueing discipline.
func (qdisc *Qdisc) mainCmd() []string {
	cmd := []string{"qdisc", "add", "dev", qdisc.Dev}
	if qdisc.Handle != "" {
		cmd = append(cmd, "handle", qdisc.Handle)
	}

	if qdisc.Root {
		cmd = append(cmd, "root")
	}

	if qdisc.Ingress {
		cmd = append(cmd, "ingress")
	}

	return cmd
}

// Add adds qdisc to a node.
func (qdisc *Qdisc) Add() error {
	cmd := qdisc.mainCmd()
	_, err := shared.RunCommand("tc", cmd...)
	if err != nil {
		return err
	}

	return nil
}

// Delete deletes qdisc from node.
func (qdisc *Qdisc) Delete() error {
	cmd := []string{"qdisc", "del", "dev", qdisc.Dev}
	if qdisc.Root {
		cmd = append(cmd, "root")
	}

	if qdisc.Ingress {
		cmd = append(cmd, "ingress")
	}

	_, err := shared.RunCommand("tc", cmd...)
	if err != nil {
		return err
	}

	return nil
}

// QdiscHTB represents the hierarchy token bucket qdisc object.
type QdiscHTB struct {
	Qdisc
	Default string
}

// Add adds qdisc to a node.
func (qdisc *QdiscHTB) Add() error {
	cmd := qdisc.mainCmd()
	cmd = append(cmd, "htb")

	if qdisc.Default != "" {
		cmd = append(cmd, "default", qdisc.Default)
	}

	_, err := shared.RunCommand("tc", cmd...)
	if err != nil {
		return err
	}

	return nil
}
