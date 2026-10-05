package bgp

import (
	bgpAPI "github.com/osrg/gobgp/v4/api"
	bgpAPIUtil "github.com/osrg/gobgp/v4/pkg/apiutil"
	bgpPacket "github.com/osrg/gobgp/v4/pkg/packet/bgp"

	"github.com/canonical/lxd/shared/logger"
)

// DebugInfo represents the internal debug state of the BGP server.
type DebugInfo struct {
	Server   DebugInfoServer   `json:"server" yaml:"server"`
	Prefixes []DebugInfoPrefix `json:"prefixes" yaml:"prefixes"`
	Peers    []DebugInfoPeer   `json:"peers" yaml:"peers"`
}

// DebugInfoServer exposes the shared listener configuration.
type DebugInfoServer struct {
	Address  string `json:"address" yaml:"address"`
	ASN      uint32 `json:"asn" yaml:"asn"`
	RouterID string `json:"router_id" yaml:"router_id"`
	Running  bool   `json:"running" yaml:"running"`
}

// DebugInfoPrefix exposes details on a single BGP prefix.
type DebugInfoPrefix struct {
	Owner   string `json:"owner" yaml:"owner"`
	Prefix  string `json:"prefix" yaml:"prefix"`
	Nexthop string `json:"nexthop" yaml:"nexthop"`
}

// DebugInfoPeer exposes details on a single BGP peer.
type DebugInfoPeer struct {
	Address  string            `json:"address" yaml:"address"`
	ASN      uint32            `json:"asn" yaml:"asn"`
	Password string            `json:"password" yaml:"password"`
	Count    int               `json:"count" yaml:"count"`
	HoldTime uint64            `json:"holdtime" yaml:"holdtime"`
	Routes   []DebugInfoPrefix `json:"routes" yaml:"routes"`
}

// Debug returns a dump of the current configuration. When the BGP server is not
// running, it returns a payload with Server.Running set to false and no peers or
// prefixes, rather than an error.
func (s *Server) Debug() DebugInfo {
	// Snapshot in-memory state under lock to avoid blocking BGP configuration changes
	// during external GoBGP RIB queries.
	s.mu.Lock()
	if s.bgp == nil {
		s.mu.Unlock()
		return DebugInfo{
			Server: DebugInfoServer{
				Running: false,
			},
			Peers:    []DebugInfoPeer{},
			Prefixes: []DebugInfoPrefix{},
		}
	}

	debug := DebugInfo{}

	// Fill in server state.
	debug.Server.Running = true
	debug.Server.ASN = s.asn
	debug.Server.Address = s.address
	debug.Server.RouterID = s.routerID.String()

	// Fill in the peers.
	debug.Peers = []DebugInfoPeer{}
	for _, peer := range s.peers {
		entry := DebugInfoPeer{}
		entry.Address = peer.address.String()
		entry.ASN = peer.asn
		entry.Password = peer.password
		entry.Count = peer.count
		entry.HoldTime = peer.holdtime
		entry.Routes = []DebugInfoPrefix{}

		// Populate routes advertised to this peer based on peer's owner network.
		for _, p := range s.paths {
			if peer.owner != "" && ownerMatches(peer.owner, p.owner) {
				entry.Routes = append(entry.Routes, DebugInfoPrefix{
					Owner:   p.owner,
					Prefix:  p.prefix.String(),
					Nexthop: p.nexthop.String(),
				})
			}
		}

		debug.Peers = append(debug.Peers, entry)
	}

	// Build a map of prefix+nexthop to owner from internal tracked paths,
	// with a fallback mapping by prefix alone.
	prefixOwners := map[string]string{}
	for _, path := range s.paths {
		prefixOwners[path.prefix.String()+"_"+path.nexthop.String()] = path.owner
		_, exists := prefixOwners[path.prefix.String()]
		if !exists {
			prefixOwners[path.prefix.String()] = path.owner
		}
	}

	bgpServer := s.bgp
	s.mu.Unlock()

	// Query live paths from GoBGP global RIB outside the lock.
	debug.Prefixes = []DebugInfoPrefix{}
	families := []bgpPacket.Family{bgpPacket.RF_IPv4_UC, bgpPacket.RF_IPv6_UC}
	for _, family := range families {
		err := bgpServer.ListPath(bgpAPIUtil.ListPathRequest{
			TableType: bgpAPI.TableType_TABLE_TYPE_GLOBAL,
			Family:    family,
		}, func(nlri bgpPacket.NLRI, paths []*bgpAPIUtil.Path) {
			for _, p := range paths {
				prefixStr := nlri.String()
				nexthopStr := ""

				for _, attr := range p.Attrs {
					switch a := attr.(type) {
					case *bgpPacket.PathAttributeNextHop:
						nexthopStr = a.Value.String()
					case *bgpPacket.PathAttributeMpReachNLRI:
						nexthopStr = a.Nexthop.String()
					}
				}

				owner := prefixOwners[prefixStr+"_"+nexthopStr]
				if owner == "" {
					owner = prefixOwners[prefixStr]
				}

				entry := DebugInfoPrefix{
					Prefix:  prefixStr,
					Owner:   owner,
					Nexthop: nexthopStr,
				}

				debug.Prefixes = append(debug.Prefixes, entry)
			}
		})
		if err != nil {
			// Log the error and return what we have so far rather than failing the
			// whole debug dump.
			logger.Warn("Failed querying GoBGP RIB for debug output", logger.Ctx{"family": family, "err": err})
			break
		}
	}

	return debug
}
