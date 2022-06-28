package ip

// Veth represents arguments for link of type veth.
type Veth struct {
	Link
	PeerName string
}

// additionalArgs generates veth specific arguments.
func (veth *Veth) additionalArgs() []string {
	args := []string{}
	if veth.PeerName != "" {
		args = append(args, "peer", "name", veth.PeerName)
	}

	return args
}

// Add adds new virtual link.
func (veth *Veth) Add() error {
	return veth.Link.add("veth", veth.additionalArgs())
}
